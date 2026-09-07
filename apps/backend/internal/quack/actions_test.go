package quack_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

type fakeActionClient struct {
	dmFailures []error
	dms        []fakeActionMessage
}

type fakeActionMessage struct {
	TargetID string
	Message  string
}

func (f *fakeActionClient) SendDM(ctx context.Context, discordUserID, message string) (map[string]any, error) {
	_ = ctx
	if len(f.dmFailures) > 0 {
		err := f.dmFailures[0]
		f.dmFailures = f.dmFailures[1:]
		if err != nil {
			return nil, err
		}
	}
	f.dms = append(f.dms, fakeActionMessage{TargetID: discordUserID, Message: message})
	return map[string]any{"message_id": "dm-message-1"}, nil
}

func TestActionServiceProcessesSafeActions(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("safe-actions"))
	created, err := quack.NewCaseService(store).Create(ctx, modContext, quack.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	fakeDiscord := &fakeActionClient{}
	if err := quack.NewActionService(store, fakeDiscord).ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process actions: %v", err)
	}

	if len(fakeDiscord.dms) != 1 || fakeDiscord.dms[0].TargetID != "target-1" || !strings.Contains(fakeDiscord.dms[0].Message, "No spam") || !strings.Contains(fakeDiscord.dms[0].Message, "**Spam**") || !strings.Contains(fakeDiscord.dms[0].Message, "Case #1") {
		t.Fatalf("unexpected DMs: %+v", fakeDiscord.dms)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("expected no enforcement action for the default warning, got %+v", actions)
	}
	notification, err := store.GetCaseNotification(ctx, created.ID)
	if err != nil || notification == nil || notification.Status != model.NotificationSent {
		t.Fatalf("expected sent case notification, got %+v err=%v", notification, err)
	}
	cases, err := store.ListCases(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if cases[0].Validity != model.CaseValidityValid {
		t.Fatalf("expected action completion not to change case validity, got %+v", cases[0])
	}
}

func TestActionServiceDoesNotAutomaticallyRetryNotificationFailure(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))

	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("retry-dm"))
	created, err := quack.NewCaseService(store).Create(ctx, modContext, quack.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	fakeDiscord := &fakeActionClient{dmFailures: []error{
		quack.DiscordActionError{Code: "rate_limited", Message: "rate limited", Retryable: true},
		nil,
	}}
	if err := quack.NewActionService(store, fakeDiscord).ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process first attempt: %v", err)
	}
	notification, err := store.GetCaseNotification(ctx, created.ID)
	if err != nil || notification == nil || notification.Status != model.NotificationFailed {
		t.Fatalf("expected terminal notification failure, got %+v err=%v", notification, err)
	}
	if err := quack.NewActionService(store, fakeDiscord).ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process duplicate request: %v", err)
	}
	if len(fakeDiscord.dms) != 0 {
		t.Fatalf("notification retry sent a duplicate: %+v", fakeDiscord.dms)
	}
}

func TestActionServiceDoesNotNotifyForUnsupportedAction(t *testing.T) {
	ctx := quack.ContextWithTrace(context.Background(), "req-action-1", "corr-action-1")
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))

	unsupportedInput := actionTemplateInput("unsupported-notify", []quack.TemplateActionInput{
		{ActionType: model.ActionBanUser},
	})
	unsupportedInput.Levels[0].NotifyUser = false
	template := createAppTemplate(t, ctx, store, adminContext, unsupportedInput)
	created, err := quack.NewCaseService(store).Create(ctx, modContext, quack.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	fakeDiscord := &fakeActionClient{}
	if err := quack.NewActionService(store, fakeDiscord).ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process actions: %v", err)
	}
	if len(fakeDiscord.dms) != 0 {
		t.Fatalf("did not expect DM before unsupported ban action, got %+v", fakeDiscord.dms)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if actions[0].Status != model.ActionExecutionFailed {
		t.Fatalf("expected unsupported action to fail, got %+v", actions[0])
	}
	if actions[0].LastErrorCode != "discord_unavailable" || actions[0].NextRetryAt != nil {
		t.Fatalf("expected visible non-retryable unsupported action, got %+v", actions[0])
	}
	attempts, err := store.ListCaseActionAttempts(ctx, []string{actions[0].ID})
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].ErrorCode != "discord_unavailable" {
		t.Fatalf("expected failed unsupported attempt, got %+v", attempts)
	}
	audits, err := store.ListAuditLogEntries(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	var failureAudit *model.AuditLogEntry
	for i := range audits {
		if audits[i].Action == "case_action.failed" {
			failureAudit = &audits[i]
			break
		}
	}
	if failureAudit == nil || failureAudit.RequestID != "req-action-1" || failureAudit.CorrelationID != "corr-action-1" {
		t.Fatalf("expected traced action failure audit, got %+v", audits)
	}
}

func TestActionServiceReversalResolvesCaseNumber(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, admin, actionTemplateInput("numbered-reversal", []quack.TemplateActionInput{{ActionType: model.ActionTimeoutUser, TimeoutDurationSeconds: 60}}))
	created, err := quack.NewCaseService(store).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create reversal case: %v", err)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil || len(actions) != 1 {
		t.Fatalf("load original action: actions=%+v err=%v", actions, err)
	}
	if err := store.DB().Model(&model.CaseActionExecution{}).Where("id = ?", actions[0].ID).Update("status", model.ActionExecutionSucceeded).Error; err != nil {
		t.Fatalf("mark original action succeeded: %v", err)
	}
	authorizer := quack.NewGuildService(store, fakeDiscordClient{authorization: &quack.DiscordGuildAuthorization{
		Guild:  quack.DiscordBotGuild{ID: "guild-1", OwnerID: "owner-1"},
		Actor:  quack.DiscordMemberAuthorization{DiscordUserID: "mod-1", Present: true, PermissionBits: uint64(discordgo.PermissionModerateMembers), TopRolePosition: 10},
		Bot:    quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 100, Bot: true},
		Target: &quack.DiscordMemberAuthorization{DiscordUserID: "target-1", Present: true, TopRolePosition: 1},
	}})
	reversal, err := quack.NewActionService(store, nil).
		WithRecoveryControls(authorizer, nil).
		ReverseForAppeal(ctx, moderator, strconv.FormatUint(created.CaseNumber, 10), actions[0].ID, model.ActionRemoveTimeout, nil)
	if err != nil || reversal == nil {
		t.Fatalf("reverse case by number: reversal=%+v err=%v", reversal, err)
	}
	if reversal.CaseID != created.ID || reversal.ActionType != model.ActionRemoveTimeout {
		t.Fatalf("reversal targeted wrong case: %+v", reversal)
	}
}

func actionTemplateInput(slug string, actions []quack.TemplateActionInput) quack.TemplateInput {
	input := validTemplateInput(slug)
	input.Levels[0].Actions = actions
	return input
}
