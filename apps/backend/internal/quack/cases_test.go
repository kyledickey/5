package quack_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	storage "github.com/quackdiscord/bot/internal/store"
)

func TestCaseServiceCreateFromTemplate(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	service := quack.NewCaseService(store, nil)

	created, err := service.Create(ctx, modContext, quack.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
		Metadata:            json.RawMessage(`{"message_id":"123"}`),
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.ID == "" || created.CaseNumber != 1 {
		t.Fatalf("unexpected case response: %+v", created)
	}
	if created.Reason != "No spam" || created.Validity != model.CaseValidityValid || created.Source != model.CaseSourceDashboard {
		t.Fatalf("unexpected case fields: %+v", created)
	}
	if len(created.Actions) != 0 {
		t.Fatalf("expected notification to be separate from enforcement actions, got %+v", created.Actions)
	}
	if created.SelectedLevel == nil || !created.SelectedLevel.IsDefault || created.SelectedLevel.MatchedCaseCount != 1 {
		t.Fatalf("expected selected default level, got %+v", created.SelectedLevel)
	}
	notification, err := store.GetCaseNotification(ctx, created.ID)
	if err != nil || notification == nil || notification.Status != model.NotificationPending {
		t.Fatalf("expected pending case notification, got %+v err=%v", notification, err)
	}

	cases, err := store.ListCases(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	var snapshot struct {
		Template struct {
			ID             string `json:"id"`
			ReasonTemplate string `json:"reason_template"`
		} `json:"template"`
		Actions []struct {
			ActionType model.ActionType `json:"action_type"`
		} `json:"actions"`
		SelectedLevel struct {
			ID               string `json:"id"`
			IsDefault        bool   `json:"is_default"`
			MatchedCaseCount int64  `json:"matched_case_count"`
		} `json:"selected_level"`
	}
	if err := json.Unmarshal([]byte(cases[0].TemplateSnapshotJSON), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Template.ID != template.ID || snapshot.Template.ReasonTemplate != created.Reason || len(snapshot.Actions) != 0 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.SelectedLevel.ID == "" || !snapshot.SelectedLevel.IsDefault || snapshot.SelectedLevel.MatchedCaseCount != 1 {
		t.Fatalf("unexpected selected level snapshot: %+v", snapshot.SelectedLevel)
	}
}

func TestCaseServiceRejectsUnavailableTemplates(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	templateService := quack.NewTemplateService(store)
	caseService := quack.NewCaseService(store, nil)

	archivedTemplate := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("archived"))
	if _, err := templateService.Archive(ctx, adminContext, archivedTemplate.ID); err != nil {
		t.Fatalf("archive template: %v", err)
	}

	tests := []struct {
		name       string
		templateID string
	}{
		{name: "missing", templateID: "missing-template"},
		{name: "archived", templateID: archivedTemplate.ID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := caseService.Create(ctx, modContext, quack.CaseInput{
				TemplateID:          tt.templateID,
				TargetDiscordUserID: "target-1",
			})
			if !errors.Is(err, quack.ErrCaseTemplateNotAvailable) {
				t.Fatalf("expected unavailable template error, got %v", err)
			}
		})
	}
}

func TestCaseServiceValidationFailures(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	service := quack.NewCaseService(store, nil)

	tests := []struct {
		name  string
		input quack.CaseInput
	}{
		{name: "missing template", input: quack.CaseInput{TargetDiscordUserID: "target-1"}},
		{name: "missing target", input: quack.CaseInput{TemplateID: template.ID}},
		{name: "invalid source", input: quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", Source: "invalid"}},
		{name: "invalid metadata", input: quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", Metadata: json.RawMessage(`[]`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.Create(ctx, modContext, tt.input)
			if !errors.Is(err, quack.ErrCaseValidation) {
				t.Fatalf("expected validation error, got %v", err)
			}
		})
	}
}

func TestCaseServiceRejectsEmptyFinalReason(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	created, err := store.CreateCaseTemplate(ctx, storage.CreateCaseTemplateParams{
		Template: model.CaseTemplate{
			GuildID:                guildContext.Guild.ID,
			Slug:                   "empty-reason",
			Name:                   "Empty Reason",
			ReasonTemplate:         " ",
			CreatedByDiscordUserID: "admin-1",
			UpdatedByDiscordUserID: "admin-1",
		},
		Levels: []storage.ExpandedCaseTemplateLevel{
			{
				Level: model.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	_, err = quack.NewCaseService(store, nil).Create(ctx, guildContext, quack.CaseInput{
		TemplateID:          created.Template.ID,
		TargetDiscordUserID: "target-1",
	})
	if !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestCaseServiceCreatesActionlessWarningCase(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil)

	input := validTemplateInput("silent-warning")
	input.Levels[0].NotifyUser = false
	template := createAppTemplate(t, ctx, store, adminContext, input)

	created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if len(created.Actions) != 0 {
		t.Fatalf("expected no action rows for silent warning case, got %+v", created.Actions)
	}
	events, err := store.ListCaseEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("list case events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != model.CaseEventCreated {
		t.Fatalf("expected only case created event, got %+v", events)
	}
	audits, err := store.ListAuditLogEntries(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if audits[len(audits)-1].Action != "case.create" {
		t.Fatalf("expected case.create as warning audit trail, got %+v", audits)
	}
}

func TestCaseServicePermissionFailures(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil)

	noCreateContext := *modContext
	noCreateContext.Permissions = map[model.PermissionAction]bool{model.PermissionActionCaseCreate: false}
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	_, err := service.Create(ctx, &noCreateContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatalf("expected case.create permission error, got %v", err)
	}
}

func TestCaseServiceSelectsEscalationLevelFromSameTemplateHistory(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil)

	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	for i := 0; i < 2; i++ {
		created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
		if err != nil {
			t.Fatalf("create prior case %d: %v", i+1, err)
		}
		if created.SelectedLevel == nil || !created.SelectedLevel.IsDefault {
			t.Fatalf("expected prior case to use default level, got %+v", created.SelectedLevel)
		}
	}

	created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.SelectedLevel == nil || created.SelectedLevel.IsDefault || created.SelectedLevel.TriggerCaseCount != 3 || created.SelectedLevel.MatchedCaseCount != 3 {
		t.Fatalf("expected repeat spam level, got %+v", created.SelectedLevel)
	}
	if len(created.Actions) != 1 || created.Actions[0].ActionType != model.ActionTimeoutUser {
		t.Fatalf("expected selected escalation action only, got %+v", created.Actions)
	}

	cases, err := store.ListCases(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	var snapshot struct {
		SelectedLevel struct {
			TriggerCaseCount int   `json:"trigger_case_count"`
			MatchedCaseCount int64 `json:"matched_case_count"`
		} `json:"selected_level"`
		Actions []struct {
			ActionType model.ActionType `json:"action_type"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(cases[2].TemplateSnapshotJSON), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.SelectedLevel.TriggerCaseCount != 3 || snapshot.SelectedLevel.MatchedCaseCount != 3 || len(snapshot.Actions) != 1 || snapshot.Actions[0].ActionType != model.ActionTimeoutUser {
		t.Fatalf("unexpected escalation snapshot: %+v", snapshot)
	}
	assertSimplifiedCaseSnapshot(t, cases[2].TemplateSnapshotJSON)
}

func assertSimplifiedCaseSnapshot(t *testing.T, body string) {
	t.Helper()
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
		t.Fatalf("decode simplified case snapshot: %v", err)
	}
	template := snapshot["template"].(map[string]any)
	if _, exists := template["default_severity"]; exists {
		t.Fatalf("case snapshot leaked template severity: %s", body)
	}
	level := snapshot["selected_level"].(map[string]any)
	for _, retired := range []string{"window_minutes", "notification_type", "enabled"} {
		if _, exists := level[retired]; exists {
			t.Fatalf("case snapshot leaked level field %s: %s", retired, body)
		}
	}
	for _, rawAction := range snapshot["actions"].([]any) {
		action := rawAction.(map[string]any)
		for _, retired := range []string{"position", "config", "notify_user", "notification_type", "continue_on_error", "retry_backoff_ms", "timeout_ms", "idempotency_scope", "enabled"} {
			if _, exists := action[retired]; exists {
				t.Fatalf("case snapshot leaked action field %s: %s", retired, body)
			}
		}
	}
}

func TestCaseServiceHighestMatchingLevelWins(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil)

	input := validTemplateInput("spam")
	input.Levels = append(input.Levels, quack.TemplateLevelInput{
		Name:             "Early repeat",
		Position:         3,
		TriggerCaseCount: 2,
	})
	template := createAppTemplate(t, ctx, store, adminContext, input)

	for i := 0; i < 2; i++ {
		if _, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"}); err != nil {
			t.Fatalf("create prior case %d: %v", i+1, err)
		}
	}
	created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.SelectedLevel == nil || created.SelectedLevel.TriggerCaseCount != 3 {
		t.Fatalf("expected highest trigger threshold to win, got %+v", created.SelectedLevel)
	}
}

func TestCaseServiceEscalationUsesAllTimeMatchingHistory(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil)

	input := validTemplateInput("spam")
	input.Levels[1].TriggerCaseCount = 2
	template := createAppTemplate(t, ctx, store, adminContext, input)

	old, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create old case: %v", err)
	}
	oldTime := time.Now().UTC().Add(-2 * time.Hour)
	if err := store.DB().Model(&model.Case{}).Where("id = ?", old.ID).Update("created_at", oldTime).Error; err != nil {
		t.Fatalf("age old case: %v", err)
	}

	otherTemplate := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("other-template"))
	if _, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: otherTemplate.ID, TargetDiscordUserID: "target-1"}); err != nil {
		t.Fatalf("create other template case: %v", err)
	}
	if _, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-2"}); err != nil {
		t.Fatalf("create other target case: %v", err)
	}

	created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.SelectedLevel == nil || created.SelectedLevel.IsDefault || created.SelectedLevel.MatchedCaseCount != 2 {
		t.Fatalf("expected old matching history to count without a time window, got %+v", created.SelectedLevel)
	}
}

func TestCaseServiceVoidedCasesDoNotCount(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil)

	input := validTemplateInput("spam")
	input.Levels[1].TriggerCaseCount = 2
	template := createAppTemplate(t, ctx, store, adminContext, input)

	prior, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create prior case: %v", err)
	}
	if err := store.DB().Model(&model.Case{}).Where("id = ?", prior.ID).Update("status", model.CaseValidityVoided).Error; err != nil {
		t.Fatalf("void prior case: %v", err)
	}

	created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.SelectedLevel == nil || !created.SelectedLevel.IsDefault || created.SelectedLevel.MatchedCaseCount != 1 {
		t.Fatalf("expected voided prior case to be ignored, got %+v", created.SelectedLevel)
	}
}

func TestCaseServiceDashboardReads(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil)
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))

	first, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create first case: %v", err)
	}
	second, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-2"})
	if err != nil {
		t.Fatalf("create second case: %v", err)
	}

	list, err := service.List(ctx, modContext, quack.CaseListInput{Limit: "10"})
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if list.Total != 2 || len(list.Cases) != 2 || list.Cases[0].ID != second.ID || list.Cases[1].ID != first.ID {
		t.Fatalf("expected newest-first case list, got %+v", list)
	}
	if list.Cases[0].SelectedLevel == nil {
		t.Fatalf("expected selected level in case list response")
	}
	legacySnapshot := fmt.Sprintf(`{"template":{"id":%q,"slug":"spam","name":"Spam","version":1,"reason_template":"No spam","default_severity":"medium"},"selected_level":{"id":"level-1","name":"Default","position":1,"is_default":true,"trigger_case_count":0,"window_minutes":0,"notify_user":true,"notification_type":"warning","matched_case_count":1},"actions":[{"id":"action-1","position":1,"action_type":"timeout_user","config":{"duration_minutes":60},"notify_user":false,"continue_on_error":false,"max_retries":2,"retry_backoff_ms":1000,"timeout_ms":0,"idempotency_scope":"case"}]}`, template.ID)
	if err := store.DB().Model(&model.Case{}).Where("id = ?", first.ID).Update("template_snapshot_json", legacySnapshot).Error; err != nil {
		t.Fatalf("install legacy snapshot fixture: %v", err)
	}

	detail, err := service.Get(ctx, modContext, "1")
	if err != nil {
		t.Fatalf("get case detail: %v", err)
	}
	if detail.ID != first.ID || detail.TemplateSnapshot == nil || len(detail.Events) != 1 || len(detail.Actions) != 0 || detail.Notification == nil {
		t.Fatalf("unexpected case detail: %+v", detail)
	}
	if len(detail.TemplateSnapshot.Actions) != 1 || detail.TemplateSnapshot.Actions[0].TimeoutDurationSeconds != 3600 || detail.TemplateSnapshot.Actions[0].MaxRetries != 2 {
		t.Fatalf("expected legacy snapshot settings to remain readable, got %+v", detail.TemplateSnapshot.Actions)
	}

	profile, err := service.UserHistory(ctx, modContext, "target-1", quack.CaseListInput{Limit: "10"})
	if err != nil {
		t.Fatalf("user history: %v", err)
	}
	if profile.Total != 1 || len(profile.Cases) != 1 || profile.Summary.Total != 1 || profile.Summary.ByValidity[string(model.CaseValidityValid)] != 1 {
		t.Fatalf("unexpected profile response: %+v", profile)
	}
	if profile.Summary.ByTemplate[template.ID] != 1 {
		t.Fatalf("expected profile summary by template, got %+v", profile.Summary.ByTemplate)
	}
}

func TestCaseServiceReadValidationAndPermissions(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil)
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	if _, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"}); err != nil {
		t.Fatalf("create case: %v", err)
	}

	_, err := service.List(ctx, modContext, quack.CaseListInput{Limit: "0"})
	if !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("expected limit validation error, got %v", err)
	}
	_, err = service.List(ctx, modContext, quack.CaseListInput{Validity: "not-a-validity"})
	if !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("expected status validation error, got %v", err)
	}
	_, err = service.Get(ctx, modContext, "missing")
	if !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatalf("expected not found error, got %v", err)
	}

	noReadContext := *modContext
	noReadContext.Permissions = map[model.PermissionAction]bool{model.PermissionActionCaseCreate: false}
	_, err = service.List(ctx, &noReadContext, quack.CaseListInput{})
	if !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatalf("expected permission error, got %v", err)
	}
}

func TestCaseServiceTraceIDsPropagateToCaseActionsAndAudit(t *testing.T) {
	ctx := quack.ContextWithTrace(context.Background(), "req-case-1", "corr-case-1")
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("trace-spam"))

	created, err := quack.NewCaseService(store, nil).Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create traced case: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("expected created case id")
	}

	caseModel, err := store.GetCaseByIDOrNumber(ctx, modContext.Guild.ID, created.ID)
	if err != nil {
		t.Fatalf("get traced case: %v", err)
	}
	if caseModel == nil || caseModel.CorrelationID != "corr-case-1" {
		t.Fatalf("expected case correlation id, got %+v", caseModel)
	}

	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil {
		t.Fatalf("list traced actions: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("expected case-level notification instead of a synthetic action, got %+v", actions)
	}

	audits, err := store.ListAuditLogEntries(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	var foundCaseAudit bool
	for _, audit := range audits {
		if audit.Action == "case.create" {
			foundCaseAudit = true
			if audit.RequestID != "req-case-1" || audit.CorrelationID != "corr-case-1" {
				t.Fatalf("expected traced case audit, got %+v", audit)
			}
		}
	}
	if !foundCaseAudit {
		t.Fatalf("expected case.create audit in %+v", audits)
	}
}

func createAppTemplate(t *testing.T, ctx context.Context, store *storage.Store, guildContext *quack.GuildStaffContext, input quack.TemplateInput) *quack.TemplateResponse {
	t.Helper()

	created, err := quack.NewTemplateService(store).Create(ctx, guildContext, input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return created
}

// TestCaseDecayChangesFutureCountsWithoutErasingHistory applies a rolling rule
// window, then turns it off while preserving the earlier case's policy snapshot.
func TestCaseDecayChangesFutureCountsWithoutErasingHistory(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	admin := templateGuildContext(t, repository, "guild-1", "admin", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, repository, "guild-1", "moderator", uint64(discordgo.PermissionModerateMembers))
	templates, cases := quack.NewTemplateService(repository), quack.NewCaseService(repository, nil)
	input := validTemplateInput("decay")
	input.CaseDecayDays = 30
	template, err := templates.Create(ctx, admin, input)
	if err != nil {
		t.Fatal(err)
	}
	create := func() *quack.CaseResponse {
		t.Helper()
		result, err := cases.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target"})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := create()
	if err := repository.DB().Model(&model.Case{}).Where("id = ?", first.ID).Update("created_at", time.Now().UTC().Add(-31*24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	second, third := create(), create()
	if second.SelectedLevel.MatchedCaseCount != 1 || third.SelectedLevel.MatchedCaseCount != 2 || !third.SelectedLevel.IsDefault {
		t.Fatalf("old case still escalated: second=%+v third=%+v", second.SelectedLevel, third.SelectedLevel)
	}
	var before model.Case
	if err := repository.DB().Where("id = ?", third.ID).First(&before).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Template struct {
			CaseDecayDays int `json:"case_decay_days"`
		} `json:"template"`
	}
	if err := json.Unmarshal([]byte(before.TemplateSnapshotJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Template.CaseDecayDays != 30 {
		t.Fatalf("decay missing from historical policy: %+v", snapshot)
	}
	policy, err := templates.Export(ctx, admin, template.ID)
	if err != nil || policy.CaseDecayDays != 30 {
		t.Fatalf("export lost decay: %+v err=%v", policy, err)
	}
	policy.Slug = "decay-copy"
	imported, err := templates.Import(ctx, admin, quack.TemplateImportInput{Confirm: true, Policy: *policy})
	if err != nil || imported.CaseDecayDays != 30 {
		t.Fatalf("import lost decay: %+v err=%v", imported, err)
	}
	edit := template.EditInput()
	edit.CaseDecayDays = 0
	if _, err := templates.Update(ctx, admin, template.ID, edit); err != nil {
		t.Fatal(err)
	}
	fourth := create()
	if fourth.SelectedLevel.MatchedCaseCount != 4 || fourth.SelectedLevel.IsDefault {
		t.Fatalf("all-time did not restore historical count: %+v", fourth.SelectedLevel)
	}
	var after model.Case
	if err := repository.DB().Where("id = ?", third.ID).First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.TemplateSnapshotJSON != before.TemplateSnapshotJSON {
		t.Fatal("decay edit changed an existing case")
	}
}
