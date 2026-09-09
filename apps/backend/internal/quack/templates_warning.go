package quack

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// UnattendedTemplateActions returns the distinct outcomes a channel warning must
// describe. It is a guild-scoped system read, intended for the already-authorized
// honeypot worker; it neither selects a member's level nor writes an audit event.
// An empty action represents a case recorded without a Discord punishment.
func (s *TemplateService) UnattendedTemplateActions(ctx context.Context, guildID, templateID string) ([]model.ActionType, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("template service is not configured")
	}
	template, err := s.store.GetCaseTemplateExpanded(ctx, strings.TrimSpace(guildID), strings.TrimSpace(templateID))
	if err != nil {
		return nil, err
	}
	if template == nil || template.Template.ArchivedAt != nil {
		return nil, ErrUnattendedTemplateUnavailable
	}
	var actions []model.ActionType
	for _, level := range template.Levels {
		action := model.ActionType("")
		if len(level.Actions) > 0 {
			action = level.Actions[0].ActionType
		}
		if !slices.Contains(actions, action) {
			actions = append(actions, action)
		}
	}
	if len(actions) == 0 {
		return nil, ErrUnattendedTemplateUnavailable
	}
	return actions, nil
}
