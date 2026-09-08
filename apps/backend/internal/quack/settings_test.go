package quack_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

func TestGuildSettingsServiceAuthorizationAuditAndNotice(t *testing.T) {
	ctx := context.Background()
	repositories := newMigratedStore(t)
	bootstrap, err := repositories.BootstrapGuild(ctx, model.BootstrapGuildParams{
		DiscordGuildID: "settings-guild", Name: "Settings Guild", OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("bootstrap guild: %v", err)
	}
	manager := templateGuildContext(t, repositories, "settings-guild", "manager-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, repositories, "settings-guild", "moderator-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewGuildSettingsService(repositories).WithStaffChannelValidator(allowStaffChannel{}).WithModuleEnablementValidator(settingsModuleValidator{})
	for _, id := range []modules.ID{modules.Tickets, modules.GeneralLogging} {
		if _, err := modules.NewSQLSettingsStore(repositories.DB()).PutModuleConfiguration(ctx, modules.Configuration{GuildID: bootstrap.Guild.ID, ModuleID: id, ConfigJSON: "{}"}); err != nil {
			t.Fatal(err)
		}
	}

	auditChannel := "100000000000000001"
	intro, footer := "Welcome to this guild", "Review case details in Quack"
	tickets, logging, honeypot := true, true, false
	updated, err := service.Update(ctx, manager, quack.GuildSettingsInput{
		AuditMirrorChannelDiscordID: &auditChannel,
		NotificationIntroduction:    &intro, NotificationFooter: &footer,
		TicketsEnabled: &tickets, GeneralLoggingEnabled: &logging, HoneypotEnabled: &honeypot,
	})
	if err != nil {
		t.Fatalf("update guild settings: %v", err)
	}
	if updated.AuditMirrorChannelDiscordID != auditChannel || updated.ManagedEvidenceChannelDiscordID != "" || !updated.TicketsEnabled || !updated.GeneralLoggingEnabled || updated.HoneypotEnabled {
		t.Fatalf("unexpected settings response: %+v", updated)
	}

	evidenceChannel := "100000000000000002"
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{ManagedEvidenceChannelDiscordID: &evidenceChannel}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("manual evidence destination accepted: %v", err)
	}
	unvalidated := quack.NewGuildSettingsService(repositories)
	if _, err := unvalidated.Update(ctx, manager, quack.GuildSettingsInput{AuditMirrorChannelDiscordID: &auditChannel}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("unvalidated audit destination accepted: %v", err)
	}
	deniedValue := "forbidden"
	if _, err := service.Update(ctx, moderator, quack.GuildSettingsInput{NotificationFooter: &deniedValue}); !errors.Is(err, quack.ErrGuildSettingsPermissionDenied) {
		t.Fatalf("expected denied moderator write, got %v", err)
	}
	tooLong := strings.Repeat("x", 2001)
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{NotificationIntroduction: &tooLong}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("expected validation failure, got %v", err)
	}
	invalidChannel := "not-a-channel"
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{AuditMirrorChannelDiscordID: &invalidChannel}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("expected non-snowflake channel rejection, got %v", err)
	}

	audits, err := repositories.ListAuditLogEntries(ctx, bootstrap.Guild.ID)
	if err != nil {
		t.Fatalf("list settings audits: %v", err)
	}
	results := map[model.AuditResult]bool{}
	for _, audit := range audits {
		if audit.Action == "guild_settings.update" {
			results[audit.Result] = true
		}
	}
	for _, result := range []model.AuditResult{model.AuditResultSuccess, model.AuditResultFailure, model.AuditResultDenied} {
		if !results[result] {
			t.Fatalf("missing %s settings audit in %+v", result, audits)
		}
	}

	acknowledged, err := service.AcknowledgeStarterPolicyNotice(ctx, manager)
	if err != nil {
		t.Fatalf("acknowledge starter notice: %v", err)
	}
	if acknowledged.StarterPolicyReviewRequired || acknowledged.StarterPolicyNoticeAcknowledgedAt == nil {
		t.Fatalf("starter notice did not become one-time acknowledged state: %+v", acknowledged)
	}
	starter, err := repositories.GetCaseTemplateExpanded(ctx, bootstrap.Guild.ID, bootstrap.StarterTemplate.Template.ID)
	if err != nil || starter == nil || starter.Template.ArchivedAt != nil {
		t.Fatalf("acknowledgement changed starter policy availability: starter=%+v err=%v", starter, err)
	}
	read, err := service.Get(ctx, manager)
	if err != nil || read.StarterPolicyReviewRequired {
		t.Fatalf("read did not expose acknowledged setup state: read=%+v err=%v", read, err)
	}
}

// allowStaffChannel isolates settings persistence tests from the live Discord adapter.
type allowStaffChannel struct{}

func (allowStaffChannel) ValidateStaffChannel(context.Context, string, string) error { return nil }

// TestAppealQueueSettingIsIndependentAndRequiresValidation covers the dedicated
// queue's permission, persistence and channel-deletion boundary.
func TestAppealQueueSettingIsIndependentAndRequiresValidation(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	bootstrap, err := repository.BootstrapGuild(ctx, model.BootstrapGuildParams{DiscordGuildID: "queue-settings", Name: "Pond", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	manager := templateGuildContext(t, repository, "queue-settings", "manager", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, repository, "queue-settings", "mod", uint64(discordgo.PermissionModerateMembers))
	queue, audit := "100000000000000003", "100000000000000004"
	service := quack.NewGuildSettingsService(repository).WithStaffChannelValidator(allowStaffChannel{})
	if _, err := service.Update(ctx, moderator, quack.GuildSettingsInput{AppealQueueChannelDiscordID: &queue}); !errors.Is(err, quack.ErrGuildSettingsPermissionDenied) {
		t.Fatalf("moderator changed setup: %v", err)
	}
	if _, err := quack.NewGuildSettingsService(repository).Update(ctx, manager, quack.GuildSettingsInput{AppealQueueChannelDiscordID: &queue}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("unvalidated channel saved: %v", err)
	}
	saved, err := service.Update(ctx, manager, quack.GuildSettingsInput{AppealQueueChannelDiscordID: &queue, AuditMirrorChannelDiscordID: &audit})
	if err != nil || saved.AppealQueueChannelDiscordID != queue {
		t.Fatalf("queue response: %+v %v", saved, err)
	}
	loaded, err := repository.GetGuildSettings(ctx, bootstrap.Guild.ID)
	if err != nil || loaded.AppealQueueChannelDiscordID != queue || loaded.AuditMirrorChannelDiscordID != audit {
		t.Fatalf("independent channels not persisted: %+v %v", loaded, err)
	}
	if _, err := repository.ClearGuildChannelReferences(ctx, bootstrap.Guild.ID, queue, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err = repository.GetGuildSettings(ctx, bootstrap.Guild.ID)
	if err != nil || loaded.AppealQueueChannelDiscordID != "" || loaded.AuditMirrorChannelDiscordID != audit {
		t.Fatalf("deletion cleared wrong destination: %+v %v", loaded, err)
	}
}

// TestAppealRejoinSettingValidatesAndPersists checks partial updates and explicit
// removal without permitting arbitrary destinations in member-facing notices.
func TestAppealRejoinSettingValidatesAndPersists(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	_, err := repository.BootstrapGuild(ctx, model.BootstrapGuildParams{DiscordGuildID: "rejoin-settings", Name: "Pond", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	manager := templateGuildContext(t, repository, "rejoin-settings", "manager", uint64(discordgo.PermissionManageGuild))
	service := quack.NewGuildSettingsService(repository)
	invite := "https://discord.com/invite/pond-code"
	saved, err := service.Update(ctx, manager, quack.GuildSettingsInput{AppealRejoinURL: &invite})
	if err != nil || saved.AppealRejoinURL != "https://discord.gg/pond-code" {
		t.Fatalf("invite not normalized: %+v %v", saved, err)
	}
	for _, invalid := range []string{"https://example.com/invite/a", "http://discord.gg/a", "https://discord.gg.evil/a", "https://discord.gg/a?x=1", "https://discord.gg/a#fragment", "https://discord.gg/a/b", "https://name@discord.gg/a"} {
		if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{AppealRejoinURL: &invalid}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
			t.Fatalf("accepted %q: %v", invalid, err)
		}
	}
	saved, err = service.Update(ctx, manager, quack.GuildSettingsInput{})
	if err != nil || saved.AppealRejoinURL != "https://discord.gg/pond-code" {
		t.Fatalf("partial update lost invite: %+v %v", saved, err)
	}
	empty := ""
	saved, err = service.Update(ctx, manager, quack.GuildSettingsInput{AppealRejoinURL: &empty})
	if err != nil || saved.AppealRejoinURL != "" {
		t.Fatalf("invite not removed: %+v %v", saved, err)
	}
}

// settingsModuleValidator substitutes only the external module validation step.
// Canonical persistence and transactional configuration comparison remain real.
type settingsModuleValidator struct{}

// ValidateGuildModuleEnablement approves the exact test configuration bytes.
func (settingsModuleValidator) ValidateGuildModuleEnablement(context.Context, *quack.GuildStaffContext, string) (string, error) {
	return "{}", nil
}
