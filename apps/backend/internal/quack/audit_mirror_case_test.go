package quack

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// auditCaseRepository isolates record enrichment from polling and Discord delivery.
type auditCaseRepository struct {
	AuditMirrorRepository
	item      *model.Case
	execution *model.CaseActionExecution
}

func (r auditCaseRepository) GetCaseByID(context.Context, string) (*model.Case, error) {
	return r.item, nil
}
func (r auditCaseRepository) GetCaseActionExecution(context.Context, string, string) (*model.CaseActionExecution, error) {
	return r.execution, nil
}

// TestAuditCaseEnrichmentUsesSnapshotAndCurrentRetryEligibility checks tenant
// isolation and avoids offering a stale punishment retry after case voiding.
func TestAuditCaseEnrichmentUsesSnapshotAndCurrentRetryEligibility(t *testing.T) {
	item := &model.Case{GuildID: "guild", CaseNumber: 42, TargetDiscordUserID: "member", TemplateSnapshotJSON: `{"template":{"id":"template","name":"Original rule"}}`, Validity: model.CaseValidityValid}
	item.ID = "case"
	execution := &model.CaseActionExecution{CaseID: "case", ActionType: model.ActionBanUser, Status: model.ActionExecutionFailed}
	execution.ID = "execution"
	worker := NewAuditMirrorWorker(auditCaseRepository{item: item, execution: execution}, nil, 0)
	entry := model.AuditLogEntry{GuildID: "guild", ResourceType: "case_action_execution", ResourceID: "execution", Action: string(model.AuditActionActionFailed)}
	enrich := func() AuditMirrorMessage {
		t.Helper()
		message := AuditMirrorMessage{}
		if err := worker.enrichCase(context.Background(), entry, &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	message := enrich()
	if message.CaseNumber != 42 || message.TargetDiscordUserID != "member" || message.TemplateName != "Original rule" || message.ActionType != model.ActionBanUser || message.RetryExecutionID != "execution" {
		t.Fatalf("missing details: %+v", message)
	}
	item.Validity = model.CaseValidityVoided
	if message := enrich(); message.RetryExecutionID != "" {
		t.Fatal("voided punishment offered retry")
	}
	original := "original"
	execution.ReversalOfExecutionID = &original
	if message := enrich(); message.RetryExecutionID != "execution" {
		t.Fatal("failed reversal lost retry")
	}
	execution.Status = model.ActionExecutionSucceeded
	if message := enrich(); message.RetryExecutionID != "" {
		t.Fatal("completed action offered retry")
	}
	item.GuildID = "other-guild"
	if message := enrich(); message.CaseID != "" || message.TargetDiscordUserID != "" {
		t.Fatal("cross-guild case was exposed")
	}
}

// TestCaseCreatedAuditPreservesSelectedOutcome proves that a creation event
// describes its immutable decision, while completion is a separate event.
func TestCaseCreatedAuditPreservesSelectedOutcome(t *testing.T) {
	for _, fixture := range []struct{ name, snapshot, level, outcome string }{
		{"warning", `{"template":{"id":"template","name":"Original rule"},"selected_level":{"id":"level","name":"First warning"},"actions":[],"context_values":[{"value":"private context"}]}`, "First warning", "Warning"},
		{"timeout", `{"template":{"id":"template","name":"Original rule"},"selected_level":{"id":"level","name":"Third case"},"actions":[{"action_type":"timeout_user","timeout_duration_seconds":86400}]}`, "Third case", "Timeout (24h)"},
		{"ban", `{"template":{"id":"template","name":"Original rule"},"selected_level":{"id":"level","name":"Final"},"actions":[{"action_type":"ban_user"}]}`, "Final", "Ban"},
		{"historical", `{"template":{"id":"template","name":"Original rule"}}`, "", ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			item := &model.Case{GuildID: "guild", TemplateSnapshotJSON: fixture.snapshot}
			item.ID = "case"
			execution := &model.CaseActionExecution{CaseID: "case", ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionPending}
			worker := NewAuditMirrorWorker(auditCaseRepository{item: item, execution: execution}, nil, 0)
			entry := model.AuditLogEntry{GuildID: "guild", ResourceType: "case", ResourceID: "case", Action: string(model.AuditActionCaseCreate)}
			for _, status := range []model.ActionExecutionStatus{model.ActionExecutionPending, model.ActionExecutionSucceeded} {
				execution.Status = status
				message := AuditMirrorMessage{}
				if err := worker.enrichCase(context.Background(), entry, &message); err != nil {
					t.Fatal(err)
				}
				if message.SelectedLevelName != fixture.level || message.SelectedOutcome != fixture.outcome || message.TemplateName != "Original rule" {
					t.Fatalf("creation decision changed with execution %s: %+v", status, message)
				}
				if message.ActionType != "" {
					t.Fatal("creation event claimed an execution result")
				}
			}
		})
	}
}
