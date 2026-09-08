package quack

import (
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// AppealSettingsResponse returns the effective future form, including Quack's default when no override exists.
type AppealSettingsResponse struct {
	GuildID   string                 `json:"guild_id"`
	Questions []model.AppealQuestion `json:"questions"`
	Default   bool                   `json:"default"`
}

// AppealSubmissionInput carries answers to the effective snapshotted form.
type AppealSubmissionInput struct {
	Answers []model.AppealAnswer `json:"answers"`
}

// AppealDecisionInput carries the required public-safe reason for one staff transition.
type AppealDecisionInput struct {
	Reason string `json:"reason"`
}

// AppealEventResponse is one timeline entry; member projections omit staff identity.
type AppealEventResponse struct {
	ID                 string                `json:"id"`
	Type               model.AppealEventType `json:"type"`
	ActorType          string                `json:"actor_type"`
	ActorDiscordUserID string                `json:"actor_discord_user_id,omitempty"`
	Body               string                `json:"body"`
	CreatedAt          time.Time             `json:"created_at"`
}

// AppealReversalOffer describes a separately confirmed reversal without executing it.
type AppealReversalOffer struct {
	OriginalExecutionID string           `json:"original_execution_id"`
	ActionType          model.ActionType `json:"action_type"`
}

// AppealResponse is the complete case-linked appeal projection.
type AppealResponse struct {
	CaseNumber              uint64                 `json:"case_number"`
	TemplateName            string                 `json:"template_name"`
	ID                      string                 `json:"id"`
	GuildID                 string                 `json:"guild_id"`
	CaseID                  string                 `json:"case_id"`
	TargetDiscordUserID     string                 `json:"target_discord_user_id"`
	Status                  model.AppealStatus     `json:"status"`
	Questions               []model.AppealQuestion `json:"questions"`
	Answers                 []model.AppealAnswer   `json:"answers"`
	DecisionReason          string                 `json:"decision_reason,omitempty"`
	ReviewedByDiscordUserID string                 `json:"reviewed_by_discord_user_id,omitempty"`
	Events                  []AppealEventResponse  `json:"events"`
	ReversalOffers          []AppealReversalOffer  `json:"reversal_offers,omitempty"`
	CreatedAt               time.Time              `json:"created_at"`
	UpdatedAt               time.Time              `json:"updated_at"`
}

// AppealListResponse returns stable staff queue pagination.
type AppealListResponse struct {
	Appeals []AppealResponse `json:"appeals"`
	Total   int64            `json:"total"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}
