package quack

import (
	"context"
	"fmt"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// EnsureHoneypotTemplate creates the initial ban policy through normal template
// validation and auditing. Repeated setup preserves the administrator's edits.
// Archived policies require an explicit restore rather than being silently revived.
func (s *TemplateService) EnsureHoneypotTemplate(ctx context.Context, guild *GuildStaffContext) (*TemplateResponse, error) {
	if err := s.requireWrite(ctx, guild, "case_template.create", ""); err != nil {
		return nil, err
	}
	existing, err := s.store.GetCaseTemplateBySlug(ctx, guild.Guild.ID, "honeypot")
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.ArchivedAt != nil {
			return nil, fmt.Errorf("%w: restore the archived honeypot template before setup", ErrTemplateValidation)
		}
		return s.Get(ctx, guild, existing.ID)
	}
	return s.Create(ctx, guild, TemplateInput{
		Slug:           "honeypot",
		Name:           "Honeypot",
		Description:    "Applied when a member posts in the honeypot channel.",
		ReasonTemplate: "Posted in the honeypot channel despite the warning.",
		Appealable:     true,
		Levels: []TemplateLevelInput{{
			Name:       "Default",
			Position:   1,
			IsDefault:  true,
			NotifyUser: true,
			Actions:    []TemplateActionInput{{ActionType: model.ActionBanUser}},
		}},
	})
}
