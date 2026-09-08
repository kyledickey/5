package store_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack/model"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestGuildBootstrapCreatesExactStarterAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repositories := testutil.NewSQLiteStore(t)
	migrateStore(t, repositories)
	input := model.BootstrapGuildParams{
		DiscordGuildID: "guild-bootstrap", Name: "Bootstrap", IconURL: "icon-url",
		OwnerDiscordUserID: "owner-1", KnownChannelDiscordIDs: []string{"channel-1"},
	}
	first, err := repositories.BootstrapGuild(ctx, input)
	if err != nil {
		t.Fatalf("bootstrap guild: %v", err)
	}
	if !first.GuildCreated || !first.StarterTemplateCreated || !first.Guild.IsActive {
		t.Fatalf("unexpected first bootstrap flags: %+v", first)
	}
	if first.Settings.StarterPolicyTemplateID != first.StarterTemplate.Template.ID || !first.Settings.StarterPolicyNoticePending {
		t.Fatalf("starter policy was not bound to pending settings: %+v", first.Settings)
	}
	assertExactStarterPolicy(t, first.StarterTemplate)

	second, err := repositories.BootstrapGuild(ctx, input)
	if err != nil {
		t.Fatalf("rerun bootstrap: %v", err)
	}
	if second.GuildCreated || second.StarterTemplateCreated || second.Guild.ID != first.Guild.ID || second.Settings.ID != first.Settings.ID || second.StarterTemplate.Template.ID != first.StarterTemplate.Template.ID {
		t.Fatalf("bootstrap duplicated durable state: first=%+v second=%+v", first, second)
	}
	var templateCount int64
	if err := repositories.DB().Table("case_templates").Where("guild_id = ?", first.Guild.ID).Count(&templateCount).Error; err != nil || templateCount != 1 {
		t.Fatalf("expected one starter template, count=%d err=%v", templateCount, err)
	}
}

func TestGuildSettingsLifecyclePreservesHistoryAndRepairsChannels(t *testing.T) {
	ctx := context.Background()
	repositories := testutil.NewSQLiteStore(t)
	migrateStore(t, repositories)
	bootstrap, err := repositories.BootstrapGuild(ctx, model.BootstrapGuildParams{
		DiscordGuildID: "guild-lifecycle", Name: "Before", OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("bootstrap guild: %v", err)
	}
	settings := bootstrap.Settings
	settings.AuditMirrorChannelDiscordID = "audit-channel"
	settings.ManagedEvidenceChannelDiscordID = "evidence-channel"
	settings.NotificationIntroduction = "Welcome"
	settings.NotificationFooter = "Footer"
	for _, id := range []modules.ID{modules.Tickets, modules.GeneralLogging, modules.Honeypots} {
		if _, err := modules.NewSQLSettingsStore(repositories.DB()).PutModuleConfiguration(ctx, modules.Configuration{GuildID: bootstrap.Guild.ID, ModuleID: id, Enabled: true, ConfigJSON: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	updated, err := repositories.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{Settings: settings, Audit: &model.AuditLogEntry{
		GuildID: bootstrap.Guild.ID, ActorDiscordUserID: "owner-1", Source: model.AuditSourceAPI,
		Action: "guild_settings.update", ResourceType: "guild_settings", Result: model.AuditResultSuccess, MetadataJSON: "{}",
	}})
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}
	if !updated.TicketsEnabled || !updated.GeneralLoggingEnabled || !updated.HoneypotEnabled {
		t.Fatalf("independent module toggles were not stored: %+v", updated)
	}
	if _, err := repositories.DeactivateGuild(ctx, bootstrap.Guild.DiscordGuildID, &model.AuditLogEntry{
		ActorDiscordUserID: "quack-system", Source: model.AuditSourceDiscord, Action: "guild.lifecycle.leave",
		ResourceType: "guild", Result: model.AuditResultSuccess, MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("deactivate guild: %v", err)
	}
	departed, err := repositories.GetGuildByDiscordID(ctx, bootstrap.Guild.DiscordGuildID)
	if err != nil || departed == nil || departed.IsActive {
		t.Fatalf("expected inactive preserved guild, guild=%+v err=%v", departed, err)
	}

	rejoined, err := repositories.BootstrapGuild(ctx, model.BootstrapGuildParams{
		DiscordGuildID: bootstrap.Guild.DiscordGuildID, Name: "After", IconURL: "new-icon",
		OwnerDiscordUserID: "owner-2", KnownChannelDiscordIDs: []string{"audit-channel"},
	})
	if err != nil {
		t.Fatalf("rejoin guild: %v", err)
	}
	if rejoined.Guild.ID != bootstrap.Guild.ID || !rejoined.Guild.IsActive || rejoined.Guild.Name != "After" || rejoined.Guild.OwnerDiscordUserID != "owner-2" {
		t.Fatalf("rejoin did not refresh preserved guild: %+v", rejoined.Guild)
	}
	if rejoined.StarterTemplate.Template.ID != bootstrap.StarterTemplate.Template.ID || rejoined.StarterTemplateCreated {
		t.Fatalf("rejoin duplicated starter template: %+v", rejoined)
	}
	if rejoined.Settings.AuditMirrorChannelDiscordID != "audit-channel" || rejoined.Settings.ManagedEvidenceChannelDiscordID != "" {
		t.Fatalf("rejoin channel repair was unsafe: %+v", rejoined.Settings)
	}
	if rejoined.Settings.NotificationIntroduction != "Welcome" || !rejoined.Settings.TicketsEnabled {
		t.Fatalf("rejoin lost non-channel settings: %+v", rejoined.Settings)
	}
	if _, err := repositories.ClearGuildChannelReferences(ctx, bootstrap.Guild.ID, "audit-channel", &model.AuditLogEntry{
		GuildID: bootstrap.Guild.ID, ActorDiscordUserID: "quack-system", Source: model.AuditSourceDiscord,
		Action: "guild_settings.channel_reference.cleared", ResourceType: "guild_settings", Result: model.AuditResultSuccess, MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("clear deleted channel: %v", err)
	}
	cleared, err := repositories.GetGuildSettings(ctx, bootstrap.Guild.ID)
	if err != nil || cleared.AuditMirrorChannelDiscordID != "" {
		t.Fatalf("deleted channel reference remained: settings=%+v err=%v", cleared, err)
	}
}

func assertExactStarterPolicy(t *testing.T, template model.ExpandedCaseTemplate) {
	t.Helper()
	if template.Template.Name != "General rule violation" || template.Template.ReasonTemplate != "General rule violation" || !template.Template.Appealable || template.Template.ArchivedAt != nil || len(template.Levels) != 3 {
		t.Fatalf("unexpected starter template: %+v", template)
	}
	if !template.Levels[0].Level.IsDefault || template.Levels[0].Level.TriggerCaseCount != 0 || !template.Levels[0].Level.NotifyUser || len(template.Levels[0].Actions) != 0 {
		t.Fatalf("unexpected starter default: %+v", template.Levels[0])
	}
	if template.Levels[1].Level.TriggerCaseCount != 3 || !template.Levels[1].Level.NotifyUser || len(template.Levels[1].Actions) != 1 || template.Levels[1].Actions[0].ActionType != model.ActionTimeoutUser || template.Levels[1].Actions[0].ConfigJSON != `{"duration_seconds":86400}` {
		t.Fatalf("unexpected starter timeout: %+v", template.Levels[1])
	}
	if template.Levels[2].Level.TriggerCaseCount != 5 || !template.Levels[2].Level.NotifyUser || len(template.Levels[2].Actions) != 1 || template.Levels[2].Actions[0].ActionType != model.ActionBanUser || template.Levels[2].Actions[0].ConfigJSON != `{"delete_message_seconds":86400}` {
		t.Fatalf("unexpected starter ban: %+v", template.Levels[2])
	}
}

// TestGuildModuleTogglesUseCanonicalRowsAndRollbackOnDrift covers native setup
// visibility, explicit partial updates, and all-or-nothing config conflict writes.
func TestGuildModuleTogglesUseCanonicalRowsAndRollbackOnDrift(t *testing.T) {
	ctx := context.Background()
	repository := testutil.NewSQLiteStore(t)
	migrateStore(t, repository)
	bootstrap, err := repository.BootstrapGuild(ctx, model.BootstrapGuildParams{DiscordGuildID: "canonical", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	moduleStore := modules.NewSQLSettingsStore(repository.DB())
	for _, id := range []modules.ID{modules.Tickets, modules.GeneralLogging} {
		if _, err := moduleStore.PutModuleConfiguration(ctx, modules.Configuration{GuildID: bootstrap.Guild.ID, ModuleID: id, Enabled: true, ConfigJSON: `{"kept":"native setup"}`}); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := repository.GetGuildSettings(ctx, bootstrap.Guild.ID)
	if err != nil || !settings.TicketsEnabled || !settings.GeneralLoggingEnabled || settings.HoneypotEnabled {
		t.Fatalf("native setup not projected: %+v %v", settings, err)
	}
	// A normal core write never copies old shadow flags back over newer setup.
	settings.GeneralLoggingEnabled = false
	updated, err := repository.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{Settings: *settings, ModuleToggles: []model.GuildModuleToggle{{ModuleID: "tickets", Enabled: false}}})
	if err != nil || updated.TicketsEnabled || !updated.GeneralLoggingEnabled {
		t.Fatalf("partial toggle changed another module: %+v %v", updated, err)
	}
	config, err := moduleStore.GetModuleConfiguration(ctx, bootstrap.Guild.ID, modules.Tickets)
	if err != nil || config.Enabled || config.ConfigJSON != `{"kept":"native setup"}` {
		t.Fatalf("canonical disable lost config: %+v %v", config, err)
	}
	// Re-enabling exactly the retained validated configuration controls runtime.
	if _, err := repository.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{Settings: *settings, ModuleToggles: []model.GuildModuleToggle{{ModuleID: "tickets", Enabled: true, ExpectedConfigJSON: config.ConfigJSON}}}); err != nil {
		t.Fatal(err)
	}
	config, _ = moduleStore.GetModuleConfiguration(ctx, bootstrap.Guild.ID, modules.Tickets)
	if !config.Enabled {
		t.Fatal("core toggle did not reach canonical runtime state")
	}
	settings.NotificationFooter = "must rollback"
	_, err = repository.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{Settings: *settings, ModuleToggles: []model.GuildModuleToggle{{ModuleID: "tickets", Enabled: false}, {ModuleID: "general_logging", Enabled: true, ExpectedConfigJSON: `{"stale":true}`}}, Audit: &model.AuditLogEntry{GuildID: bootstrap.Guild.ID, ActorDiscordUserID: "owner", Action: "guild_settings.update", ResourceType: "guild_settings", Result: model.AuditResultSuccess}})
	if err == nil {
		t.Fatal("changed configuration accepted")
	}
	config, _ = moduleStore.GetModuleConfiguration(ctx, bootstrap.Guild.ID, modules.Tickets)
	settings, _ = repository.GetGuildSettings(ctx, bootstrap.Guild.ID)
	if !config.Enabled || settings.NotificationFooter != "" {
		t.Fatal("failed toggle partially committed core/module state")
	}
	audits, err := repository.ListAuditLogEntriesFiltered(ctx, model.ListAuditLogEntriesParams{GuildID: bootstrap.Guild.ID, Action: "guild_settings.update"})
	if err != nil || audits.Total != 0 {
		t.Fatalf("failed toggle wrote success audit: %+v %v", audits, err)
	}
	if _, err := repository.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{Settings: *settings, ModuleToggles: []model.GuildModuleToggle{{ModuleID: "honeypots", Enabled: false}}}); err != nil {
		t.Fatal("unconfigured module could not be disabled:", err)
	}
}
