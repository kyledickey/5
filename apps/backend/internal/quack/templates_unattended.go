package quack

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// ErrUnattendedTemplateUnavailable identifies missing, archived, or incompatible
// policy that cannot be used by a system-triggered moderation workflow.
var ErrUnattendedTemplateUnavailable = errors.New("unattended template is unavailable")

// ValidateUnattendedTemplate checks current guild-scoped policy for automation
// that supplies no custom context and executes at most one supported action per
// level. It performs no staff authorization: callers must establish their system
// workflow authority before invoking this read-only compatibility check.
func (s *TemplateService) ValidateUnattendedTemplate(ctx context.Context, guildID, templateID string) error {
	if s == nil || s.store == nil {
		return errors.New("template service is not configured")
	}
	template, err := s.store.GetCaseTemplateExpanded(ctx, strings.TrimSpace(guildID), strings.TrimSpace(templateID))
	if err != nil {
		if errors.Is(err, model.ErrTemplateCompatibilityReviewRequired) {
			return fmt.Errorf("%w: %v", ErrUnattendedTemplateUnavailable, err)
		}
		return err
	}
	if template == nil || template.Template.ArchivedAt != nil {
		return ErrUnattendedTemplateUnavailable
	}
	for _, field := range template.ContextFields {
		if field.Required {
			return fmt.Errorf("%w: required context field %s cannot be supplied unattended", ErrUnattendedTemplateUnavailable, field.Key)
		}
	}
	defaults := 0
	for _, level := range template.Levels {
		if level.Level.IsDefault {
			defaults++
		}
		if len(level.Actions) > 1 {
			return fmt.Errorf("%w: template level has multiple actions", ErrUnattendedTemplateUnavailable)
		}
		for _, action := range level.Actions {
			switch action.ActionType {
			case model.ActionSendDM, model.ActionTimeoutUser, model.ActionKickUser, model.ActionBanUser:
			default:
				return fmt.Errorf("%w: unsupported unattended action %s", ErrUnattendedTemplateUnavailable, action.ActionType)
			}
		}
	}
	if defaults != 1 || len(template.Levels) == 0 {
		return fmt.Errorf("%w: template must have exactly one default level", ErrUnattendedTemplateUnavailable)
	}
	return nil
}
