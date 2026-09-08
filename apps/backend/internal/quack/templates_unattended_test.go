package quack_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// unattendedPolicyStore isolates policy checks while retaining the production
// repository contract; unexpected persistence calls fail through the nil embed.
type unattendedPolicyStore struct {
	quack.TemplateRepository
	template            *model.ExpandedCaseTemplate
	err                 error
	guildID, templateID string
}

// GetCaseTemplateExpanded records the scope passed by the system use case.
func (s *unattendedPolicyStore) GetCaseTemplateExpanded(_ context.Context, guildID, templateID string) (*model.ExpandedCaseTemplate, error) {
	s.guildID, s.templateID = guildID, templateID
	return s.template, s.err
}

// TestValidateUnattendedTemplatePolicy covers compatibility decisions and keeps
// transient storage failures separate from policy unavailability.
func TestValidateUnattendedTemplatePolicy(t *testing.T) {
	backendErr := errors.New("database unavailable")
	now := time.Now()
	tests := []struct {
		name     string
		change   func(*model.ExpandedCaseTemplate)
		missing  bool
		storeErr error
		want     error
	}{
		{name: "actionless default"},
		{name: "supported actions", change: func(p *model.ExpandedCaseTemplate) {
			for _, action := range []model.ActionType{model.ActionSendDM, model.ActionTimeoutUser, model.ActionKickUser, model.ActionBanUser} {
				p.Levels = append(p.Levels, model.ExpandedCaseTemplateLevel{Actions: []model.CaseTemplateLevelAction{{ActionType: action}}})
			}
		}},
		{name: "optional context", change: func(p *model.ExpandedCaseTemplate) {
			p.ContextFields = []model.CaseTemplateContextField{{Key: "optional"}}
		}},
		{name: "missing", missing: true, want: quack.ErrUnattendedTemplateUnavailable},
		{name: "archived", change: func(p *model.ExpandedCaseTemplate) { p.Template.ArchivedAt = &now }, want: quack.ErrUnattendedTemplateUnavailable},
		{name: "required context", change: func(p *model.ExpandedCaseTemplate) {
			p.ContextFields = []model.CaseTemplateContextField{{Key: "reason", Required: true}}
		}, want: quack.ErrUnattendedTemplateUnavailable},
		{name: "multiple actions", change: func(p *model.ExpandedCaseTemplate) {
			p.Levels[0].Actions = []model.CaseTemplateLevelAction{{ActionType: model.ActionBanUser}, {ActionType: model.ActionSendDM}}
		}, want: quack.ErrUnattendedTemplateUnavailable},
		{name: "unsupported action", change: func(p *model.ExpandedCaseTemplate) {
			p.Levels[0].Actions = []model.CaseTemplateLevelAction{{ActionType: "unsupported"}}
		}, want: quack.ErrUnattendedTemplateUnavailable},
		{name: "no levels", change: func(p *model.ExpandedCaseTemplate) { p.Levels = nil }, want: quack.ErrUnattendedTemplateUnavailable},
		{name: "no default", change: func(p *model.ExpandedCaseTemplate) { p.Levels[0].Level.IsDefault = false }, want: quack.ErrUnattendedTemplateUnavailable},
		{name: "multiple defaults", change: func(p *model.ExpandedCaseTemplate) { p.Levels = append(p.Levels, p.Levels[0]) }, want: quack.ErrUnattendedTemplateUnavailable},
		{name: "legacy compatibility", storeErr: model.ErrTemplateCompatibilityReviewRequired, want: quack.ErrUnattendedTemplateUnavailable},
		{name: "storage failure", storeErr: backendErr, want: backendErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &model.ExpandedCaseTemplate{Levels: []model.ExpandedCaseTemplateLevel{{Level: model.CaseTemplateLevel{IsDefault: true}}}}
			if tt.change != nil {
				tt.change(p)
			}
			if tt.missing {
				p = nil
			}
			repository := &unattendedPolicyStore{template: p, err: tt.storeErr}
			err := quack.NewTemplateService(repository).ValidateUnattendedTemplate(context.Background(), " guild-a ", " template-a ")
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if repository.guildID != "guild-a" || repository.templateID != "template-a" {
				t.Fatalf("lookup scope = %q/%q", repository.guildID, repository.templateID)
			}
		})
	}
}

// TestValidateUnattendedTemplateGuildIsolation exercises real scoped persistence
// so an existing template in another guild is treated as unavailable.
func TestValidateUnattendedTemplateGuildIsolation(t *testing.T) {
	repository := newMigratedStore(t)
	guild := templateGuildContext(t, repository, "unattended-guild", "admin", 0)
	template, err := repository.CreateCaseTemplate(context.Background(), model.CreateCaseTemplateParams{
		Template: model.CaseTemplate{GuildID: guild.Guild.ID, Slug: "trap", Name: "Trap", ReasonTemplate: "Trap"},
		Levels:   []model.ExpandedCaseTemplateLevel{{Level: model.CaseTemplateLevel{Name: "Default", IsDefault: true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := quack.NewTemplateService(repository)
	if err := service.ValidateUnattendedTemplate(context.Background(), guild.Guild.ID, template.Template.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateUnattendedTemplate(context.Background(), "other-guild", template.Template.ID); !errors.Is(err, quack.ErrUnattendedTemplateUnavailable) {
		t.Fatalf("cross-guild lookup = %v", err)
	}
}
