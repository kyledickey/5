package quack_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

type fakeEvidenceClient struct {
	message   quack.DiscordMessageSnapshot
	preserved quack.PreservedDiscordAttachment
}

func (f *fakeEvidenceClient) FetchMessageEvidence(context.Context, quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	copyValue := f.message
	return &copyValue, nil
}
func (f *fakeEvidenceClient) PreserveEvidenceAttachment(context.Context, string, string, quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	copyValue := f.preserved
	return &copyValue, nil
}
func (f *fakeEvidenceClient) EnsureEvidenceChannel(context.Context, string, string) (string, error) {
	return "999999999999999999", nil
}

func TestTemplateContextLifecycleAndPolicyPortability(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	input := validTemplateInput("context-policy")
	input.ContextFields = []quack.TemplateContextFieldInput{{Key: "summary", Label: "Summary", FieldType: model.ContextFieldShortText, Position: 1, Required: true}, {Key: "details", Label: "Details", FieldType: model.ContextFieldLongText, Position: 2}, {Key: "confirmed", Label: "Confirmed", FieldType: model.ContextFieldBoolean, Position: 3}, {Key: "count", Label: "Count", FieldType: model.ContextFieldNumber, Position: 4}, {Key: "message", Label: "Message", FieldType: model.ContextFieldMessageLink, Position: 5}}
	service := quack.NewTemplateService(store)
	created, err := service.Create(ctx, admin, input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if len(created.ContextFields) != 5 || created.Version != 1 {
		t.Fatalf("unexpected context template: %+v", created)
	}
	policy, err := service.Export(ctx, admin, created.ID)
	if err != nil {
		t.Fatalf("export policy: %v", err)
	}
	body, _ := json.Marshal(policy)
	for _, forbidden := range []string{admin.Guild.ID, "guild-1", "audit", "channel_id", "secret"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("policy export leaked %q: %s", forbidden, body)
		}
	}
	if _, err := service.Archive(ctx, admin, created.ID); err != nil {
		t.Fatal(err)
	}
	active, err := service.ListActive(ctx, admin)
	if err != nil || len(active) != 0 {
		t.Fatalf("archived template remained active: %+v err=%v", active, err)
	}
	all, err := service.List(ctx, admin)
	if err != nil || len(all) != 1 || all[0].ArchivedAt == nil {
		t.Fatalf("archived template not readable: %+v err=%v", all, err)
	}
	restored, err := service.Restore(ctx, admin, created.ID)
	if err != nil || restored.ArchivedAt != nil {
		t.Fatalf("restore failed: %+v err=%v", restored, err)
	}
	policy.Slug = "imported-policy"
	imported, err := service.Import(ctx, admin, quack.TemplateImportInput{Confirm: true, Policy: *policy})
	if err != nil {
		t.Fatalf("import policy: %v", err)
	}
	if imported.ID == created.ID || imported.GuildID != admin.Guild.ID || imported.ArchivedAt != nil {
		t.Fatalf("import did not create active guild-owned identity: %+v", imported)
	}
}

func TestCaseContextEvidenceVoidReplacementAndMemberProjection(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildDiscordID := "111111111111111111"
	admin := templateGuildContext(t, store, guildDiscordID, "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, guildDiscordID, "mod-1", uint64(discordgo.PermissionModerateMembers))
	settings := model.GuildSettings{GuildID: admin.Guild.ID, ManagedEvidenceChannelDiscordID: "999999999999999999"}
	if err := store.DB().Create(&settings).Error; err != nil {
		t.Fatalf("create evidence settings: %v", err)
	}
	input := validTemplateInput("evidence-policy")
	input.ContextFields = []quack.TemplateContextFieldInput{{Key: "summary", Label: "Summary", FieldType: model.ContextFieldShortText, Position: 1, Required: true}, {Key: "message", Label: "Message", FieldType: model.ContextFieldMessageLink, Position: 2, Required: true}, {Key: "details", Label: "Details", FieldType: model.ContextFieldLongText, Position: 3}}
	template := createAppTemplate(t, ctx, store, admin, input)
	link := "https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"
	evidenceClient := &fakeEvidenceClient{message: quack.DiscordMessageSnapshot{GuildID: guildDiscordID, ChannelID: "222222222222222222", MessageID: "333333333333333333", AuthorDiscordUserID: "target-1", URL: link, Content: "original text", CreatedAt: time.Now().UTC(), Attachments: []quack.DiscordAttachmentSnapshot{{ID: "a1", Filename: "proof.png", ContentType: "image/png", SizeBytes: 100, URL: "https://cdn.discordapp.com/proof"}}}, preserved: quack.PreservedDiscordAttachment{URL: "https://cdn.discordapp.com/copy", MessageID: "copy-message", AttachmentID: "copy-attachment"}}
	service := quack.NewCaseService(store).WithEvidenceCapture(quack.NewEvidenceService(evidenceClient, store))
	summary, _ := json.Marshal("visible summary")
	message, _ := json.Marshal(link)
	created, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", ContextValues: []quack.CaseContextValueInput{{Key: "summary", Value: summary}, {Key: "message", Value: message}, {Key: "details", Value: json.RawMessage("null")}}})
	if err != nil {
		t.Fatalf("create evidence case: %v", err)
	}
	detail, err := service.Get(ctx, moderator, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.ContextValues) != 3 || detail.ContextValues[2].Value != nil || len(detail.Evidence) != 1 || len(detail.Evidence[0].Attachments) != 1 || detail.Evidence[0].Attachments[0].CopyOutcome != "preserved" {
		t.Fatalf("case snapshot incomplete: %+v", detail)
	}
	voided, err := service.Void(ctx, moderator, created.ID, "wrong policy", nil)
	if err != nil || voided.Validity != model.CaseValidityVoided {
		t.Fatalf("void failed: %+v err=%v", voided, err)
	}
	replacement, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", ReplacesCaseID: created.ID, ContextValues: []quack.CaseContextValueInput{{Key: "summary", Value: summary}, {Key: "message", Value: message}, {Key: "details", Value: json.RawMessage("null")}}})
	if err != nil {
		t.Fatalf("replacement: %v", err)
	}
	if replacement.ReplacesCaseID == nil || *replacement.ReplacesCaseID != created.ID {
		t.Fatalf("replacement link missing: %+v", replacement)
	}
	member, err := service.GetMemberCase(ctx, replacement.ID, "target-1")
	if err != nil {
		t.Fatalf("member detail: %v", err)
	}
	if member.Reason != "No spam" || member.TemplateName != "Spam" {
		t.Fatalf("member projection incomplete: %+v", member)
	}
	memberJSON, err := json.Marshal(member)
	if err != nil {
		t.Fatal(err)
	}
	var memberFields map[string]any
	if err := json.Unmarshal(memberJSON, &memberFields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"context", "evidence", "history", "selected_outcome", "moderator_discord_user_id", "voided_reason", "notification"} {
		if _, exposed := memberFields[field]; exposed {
			t.Fatalf("staff field %s exposed to member: %s", field, memberJSON)
		}
	}
	if _, err := service.GetMemberCase(ctx, replacement.ID, "other-user"); err != quack.ErrCaseNotFound {
		t.Fatalf("cross-user enumeration was not hidden: %v", err)
	}
}

func TestCaseEvidenceResponsesUseStableJSONNames(t *testing.T) {
	body, err := json.Marshal(quack.CaseEvidenceResponse{
		ID:                  "evidence-1",
		AuthorDiscordUserID: "member-1",
		MessageURL:          "https://discord.com/channels/1/2/3",
		MessageCreatedAt:    time.Now().UTC(),
		Attachments: []quack.CaseEvidenceAttachmentResponse{{
			Filename: "proof.png", ContentType: "image/png", OriginalURL: "https://cdn.example/original", SizeBytes: 10,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(body)
	for _, key := range []string{`"author_discord_user_id"`, `"message_url"`, `"message_created_at"`, `"content_type"`, `"original_url"`, `"size_bytes"`} {
		if !strings.Contains(encoded, key) {
			t.Fatalf("evidence response omitted stable JSON key %s: %s", key, encoded)
		}
	}
	if strings.Contains(encoded, "AuthorDiscordUserID") || strings.Contains(encoded, "OriginalURL") {
		t.Fatalf("evidence response leaked Go field names: %s", encoded)
	}
}

type fakeEnforcementClient struct {
	calls                   []string
	duration, deleteSeconds int
	reason                  string
	dashboardBaseURL        string
	notificationGuildID     string
	notificationCaseID      string
}

func (f *fakeEnforcementClient) SendDM(context.Context, string, string) (map[string]any, error) {
	f.calls = append(f.calls, "send_dm")
	return map[string]any{"message_id": "dm"}, nil
}
func (f *fakeEnforcementClient) PrepareDM(context.Context, string) (string, error) {
	f.calls = append(f.calls, "prepare_dm")
	return "dm-channel", nil
}
func (f *fakeEnforcementClient) SendPreparedDM(context.Context, string, string) (map[string]any, error) {
	f.calls = append(f.calls, "send_prepared_dm")
	return map[string]any{"message_id": "dm"}, nil
}
func (f *fakeEnforcementClient) SendCaseNotification(_ context.Context, _, _ string, _ string, dashboardBaseURL, guildID, caseID string) (map[string]any, error) {
	f.calls = append(f.calls, "send_case_notification")
	f.dashboardBaseURL, f.notificationGuildID, f.notificationCaseID = dashboardBaseURL, guildID, caseID
	return map[string]any{"message_id": "dm"}, nil
}
func (f *fakeEnforcementClient) TimeoutMember(_ context.Context, _, _ string, duration int, reason string) (map[string]any, error) {
	f.calls = append(f.calls, "timeout")
	f.duration = duration
	f.reason = reason
	return map[string]any{"ok": true}, nil
}
func (f *fakeEnforcementClient) KickMember(context.Context, string, string, string) (map[string]any, error) {
	f.calls = append(f.calls, "kick")
	return map[string]any{"ok": true}, nil
}
func (f *fakeEnforcementClient) BanMember(_ context.Context, _, _ string, seconds int, reason string) (map[string]any, error) {
	f.calls = append(f.calls, "ban")
	f.deleteSeconds = seconds
	f.reason = reason
	return map[string]any{"ok": true}, nil
}
func (f *fakeEnforcementClient) RemoveMemberTimeout(context.Context, string, string, string) (map[string]any, error) {
	f.calls = append(f.calls, "remove_timeout")
	return map[string]any{"ok": true}, nil
}
func (f *fakeEnforcementClient) UnbanMember(context.Context, string, string, string) (map[string]any, error) {
	f.calls = append(f.calls, "unban")
	return map[string]any{"ok": true}, nil
}

func TestEnforcementUsesExactSettingsAndNotificationOrder(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	input := validTemplateInput("timeout-policy")
	input.Levels[0].Actions = []quack.TemplateActionInput{{ActionType: model.ActionTimeoutUser, TimeoutDurationSeconds: 937, MaxRetries: 2}}
	template := createAppTemplate(t, ctx, store, admin, input)
	created, err := quack.NewCaseService(store).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeEnforcementClient{}
	if err := quack.NewActionService(store, client).ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(client.calls, ",") != "timeout,send_case_notification" || client.duration != 937 || !strings.Contains(client.reason, "case #1") || !strings.Contains(client.reason, "No spam") {
		t.Fatalf("unexpected execution calls/settings: calls=%v duration=%d reason=%q", client.calls, client.duration, client.reason)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil || len(actions) != 1 || actions[0].Status != model.ActionExecutionSucceeded {
		t.Fatalf("action did not succeed: %+v err=%v", actions, err)
	}
	notification, err := store.GetCaseNotification(ctx, created.ID)
	if err != nil || notification == nil || notification.Status != model.NotificationSent {
		t.Fatalf("notification not sent after outcome: %+v err=%v", notification, err)
	}
}

func TestBanPreparesDMAndUsesExactHistoryDeletion(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	input := validTemplateInput("ban-policy")
	input.Levels[0].Actions = []quack.TemplateActionInput{{ActionType: model.ActionBanUser, DeleteMessageSeconds: 86400}}
	template := createAppTemplate(t, ctx, store, admin, input)
	created, err := quack.NewCaseService(store).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeEnforcementClient{}
	if err := quack.NewActionService(store, client).ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(client.calls, ",") != "prepare_dm,ban,send_case_notification" || client.deleteSeconds != 86400 {
		t.Fatalf("ban/notification order or setting mismatch: calls=%v delete=%d", client.calls, client.deleteSeconds)
	}
}

func TestAppealableCaseNotificationUsesSecureDashboardControlContract(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, admin, validTemplateInput("appeal-link"))
	created, err := quack.NewCaseService(store).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeEnforcementClient{}
	if err := quack.NewActionService(store, client).WithDashboardBaseURL("https://dashboard.example").ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(client.calls, ",") != "send_case_notification" || client.dashboardBaseURL != "https://dashboard.example" || client.notificationGuildID != moderator.Guild.ID || client.notificationCaseID != created.ID {
		t.Fatalf("secure appeal notification contract was not used: %+v", client)
	}
}

func TestEscalationExcludesImportedV4HistoryAcrossTemplateVersions(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewTemplateService(store)
	template := createAppTemplate(t, ctx, store, admin, validTemplateInput("versioned-policy"))
	templateID := template.ID
	if _, err := store.CreateCase(ctx, model.CreateCaseParams{Case: model.Case{GuildID: moderator.Guild.ID, TemplateID: &templateID, TemplateVersion: 1, TemplateSnapshotJSON: "{}", TargetDiscordUserID: "target-1", ModeratorDiscordUserID: "importer", Reason: "legacy", Validity: model.CaseValidityValid, Source: model.CaseSourceV4Import, MetadataJSON: "{}", ContextValuesJSON: "[]"}, Event: model.CaseEvent{EventType: model.CaseEventCreated, Body: "imported", MetadataJSON: "{}"}}); err != nil {
		t.Fatal(err)
	}
	update := validTemplateInput("versioned-policy")
	update.Description = "version two"
	updated, err := service.Update(ctx, admin, template.ID, update)
	if err != nil || updated.ID != template.ID || updated.Version != 2 {
		t.Fatalf("version update: %+v err=%v", updated, err)
	}
	created, err := quack.NewCaseService(store).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	if created.SelectedLevel == nil || created.SelectedLevel.MatchedCaseCount != 1 || !created.SelectedLevel.IsDefault {
		t.Fatalf("v4 history affected escalation: %+v", created.SelectedLevel)
	}
}

func TestCaseCreationIdempotencyPreventsDuplicateWork(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, admin, validTemplateInput("idempotent-policy"))
	service := quack.NewCaseService(store)
	input := quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", IdempotencyKey: "discord-interaction-1"}
	first, err := service.Create(ctx, moderator, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, moderator, input)
	if err != nil || second.ID != first.ID {
		t.Fatalf("duplicate request created another result: first=%s second=%+v err=%v", first.ID, second, err)
	}
	cases, err := store.ListCases(ctx, moderator.Guild.ID)
	if err != nil || len(cases) != 1 {
		t.Fatalf("duplicate case rows: %+v err=%v", cases, err)
	}
	input.TargetDiscordUserID = "target-2"
	if _, err := service.Create(ctx, moderator, input); !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("idempotency key collision accepted: %v", err)
	}
}
