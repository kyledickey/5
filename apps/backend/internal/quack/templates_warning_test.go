package quack_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestUnattendedWarningActionsPreserveScopeAndDistinctOutcomes checks that the
// public warning describes every possible level, including a case-only level.
func TestUnattendedWarningActionsPreserveScopeAndDistinctOutcomes(t *testing.T) {
	store := &unattendedPolicyStore{template: &model.ExpandedCaseTemplate{Levels: []model.ExpandedCaseTemplateLevel{
		{Actions: []model.CaseTemplateLevelAction{{ActionType: model.ActionBanUser}}},
		{Actions: []model.CaseTemplateLevelAction{{ActionType: model.ActionBanUser}}},
		{},
		{Actions: []model.CaseTemplateLevelAction{{ActionType: model.ActionTimeoutUser}}},
	}}}
	service := quack.NewTemplateService(store)
	got, err := service.UnattendedTemplateActions(context.Background(), " guild ", " template ")
	if err != nil || !slices.Equal(got, []model.ActionType{model.ActionBanUser, "", model.ActionTimeoutUser}) {
		t.Fatalf("outcomes: %v, %v", got, err)
	}
	if store.guildID != "guild" || store.templateID != "template" {
		t.Fatal("lost guild scope")
	}
	now := time.Now()
	store.template.Template.ArchivedAt = &now
	if _, err := service.UnattendedTemplateActions(context.Background(), "guild", "template"); err == nil {
		t.Fatal("rendered archived policy")
	}
}
