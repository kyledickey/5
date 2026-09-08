package quack

import (
	"context"
	"github.com/quackdiscord/bot/internal/quack/actionmods"
	"github.com/quackdiscord/bot/internal/quack/model"
	"testing"
)

// guardRepository isolates provenance reads; any unrelated operation panics.
type guardRepository struct {
	ActionRepository
	original *model.CaseActionExecution
	payload  string
	newer    bool
}

func (r guardRepository) LoadCaseReversalProvenance(context.Context, string, string, string) (*model.CaseActionExecution, string, bool, error) {
	return r.original, r.payload, r.newer, nil
}

// guardClient records which checked operation was invoked and its provenance.
type guardClient struct {
	DiscordActionClient
	calls    int
	expected string
}

func (c *guardClient) RemoveOwnedTimeout(_ context.Context, _, _, expected, _ string) (map[string]any, error) {
	c.calls++
	c.expected = expected
	return map[string]any{"result": "timeout_removed"}, nil
}
func (c *guardClient) RemoveOwnedBan(_ context.Context, _, _, expected, _ string) (map[string]any, error) {
	c.calls++
	c.expected = expected
	return map[string]any{"result": "unbanned"}, nil
}

// TestReversalGuardFailsClosed checks that neither missing capability/provenance
// nor newer unresolved enforcement can fall back to an unconditional adapter.
func TestReversalGuardFailsClosed(t *testing.T) {
	originalID := "original"
	action := actionmods.Context{Case: model.Case{ULIDModel: model.ULIDModel{ID: "case"}, GuildID: "guild", CaseNumber: 42, Reason: "reason"}, Execution: model.CaseActionExecution{ActionType: model.ActionRemoveTimeout, ReversalOfExecutionID: &originalID}}
	for _, scenario := range []string{"match", "newer", "no_original", "no_expiry", "no_capability", "ban"} {
		t.Run(scenario, func(t *testing.T) {
			repository := guardRepository{original: &model.CaseActionExecution{CaseID: "case", Status: model.ActionExecutionSucceeded, ActionType: model.ActionTimeoutUser}, payload: `{"timeout_until":"2026-09-09T00:00:00Z"}`}
			current := action
			switch scenario {
			case "newer":
				repository.newer = true
			case "no_original":
				repository.original = nil
			case "no_expiry":
				repository.payload = "{}"
			case "ban":
				repository.original.ActionType = model.ActionBanUser
				current.Execution.ActionType = model.ActionUnbanUser
			}
			client := &guardClient{}
			service := NewActionService(repository, client)
			if scenario == "no_capability" {
				service.store = struct{ ActionRepository }{}
			}
			result := service.executeGuardedReversal(context.Background(), current)
			allowed := scenario == "match" || scenario == "ban"
			if (result.Error == "") != allowed || (client.calls == 1) != allowed {
				t.Fatalf("result=%+v calls=%d", result, client.calls)
			}
			if scenario == "ban" && client.expected != "Quack case #42: reason" {
				t.Fatal(client.expected)
			}
		})
	}
}

// guardRoutingStore captures outcomes without executing unrelated persistence.
type guardRoutingStore struct {
	ActionRepository
	completed model.CompleteCaseActionParams
}

// GetGuildByID provides a resolvable guild so routing reaches its ownership check.
func (s *guardRoutingStore) GetGuildByID(context.Context, string) (*model.Guild, error) {
	return &model.Guild{DiscordGuildID: "guild"}, nil
}

// CompleteCaseAction captures the rejection classification persisted by the worker.
func (s *guardRoutingStore) CompleteCaseAction(_ context.Context, p model.CompleteCaseActionParams) error {
	s.completed = p
	return nil
}

// TestUnlinkedReversalNeverReachesUnconditionalHandler protects legacy or corrupt
// execution rows even though current template validation excludes inverse actions.
func TestUnlinkedReversalNeverReachesUnconditionalHandler(t *testing.T) {
	for _, kind := range []model.ActionType{model.ActionRemoveTimeout, model.ActionUnbanUser} {
		repository := &guardRoutingStore{}
		service := NewActionService(repository, &guardClient{})
		called := false
		service.handlers[kind] = actionmods.Func(func(context.Context, actionmods.Context) actionmods.Result { called = true; return actionmods.Result{} })
		err := service.processClaimedAction(context.Background(), "worker", model.ClaimedCaseAction{Case: model.Case{GuildID: "guild"}, Execution: model.CaseActionExecution{ActionType: kind}})
		if err != nil || called || repository.completed.ExecutionStatus != model.ActionExecutionFailed || repository.completed.ErrorCode != "reversal_provenance_unavailable" {
			t.Fatalf("%s bypassed guard: called=%v completion=%+v err=%v", kind, called, repository.completed, err)
		}
	}
}
