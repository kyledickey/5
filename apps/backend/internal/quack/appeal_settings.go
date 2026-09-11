package quack

import (
	"context"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// DefaultAppealQuestions returns the single statement collected by Discord and
// web submissions. Each appeal snapshots this form for historical readability.
func DefaultAppealQuestions() []model.AppealQuestion {
	return []model.AppealQuestion{{
		ID:       "reason",
		Prompt:   "What would you like the moderators to reconsider?",
		Type:     model.AppealQuestionLongText,
		Required: true,
		Position: 0,
	}}
}

// GetSettings returns the shared appeal form. Historical configurable forms are
// retained on old appeals but no longer determine new submissions.
func (s *AppealService) GetSettings(ctx context.Context, guildID string) (*AppealSettingsResponse, error) {
	if strings.TrimSpace(guildID) == "" {
		return nil, appealValidation("guild is required")
	}
	return &AppealSettingsResponse{GuildID: strings.TrimSpace(guildID), Questions: DefaultAppealQuestions(), Default: true}, nil
}
