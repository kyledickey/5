package quack

import (
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// TemplateInput describes an admin-owned moderation policy before validation and normalization.
type TemplateInput struct {
	// CaseDecayDays limits counting to recent cases; zero keeps all-time history.
	CaseDecayDays int `json:"case_decay_days"`
	// ExpectedVersion protects edits based on a previously read policy; zero uses the service read version.
	ExpectedVersion uint                        `json:"expected_version,omitempty"`
	Slug            string                      `json:"slug"`
	Name            string                      `json:"name"`
	Description     string                      `json:"description"`
	ReasonTemplate  string                      `json:"reason_template"`
	Appealable      bool                        `json:"appealable"`
	ContextFields   []TemplateContextFieldInput `json:"context_fields"`
	Levels          []TemplateLevelInput        `json:"levels"`
}

// TemplateContextFieldInput defines an ordered optional staff context field.
type TemplateContextFieldInput struct {
	Key       string                 `json:"key"`
	Label     string                 `json:"label"`
	FieldType model.ContextFieldType `json:"type"`
	Position  int                    `json:"position"`
	Required  bool                   `json:"required"`
}

// TemplateLevelInput defines one default or case-count escalation with at most one enforcement action.
type TemplateLevelInput struct {
	Name             string                `json:"name"`
	Position         int                   `json:"position"`
	IsDefault        bool                  `json:"is_default"`
	TriggerCaseCount int                   `json:"trigger_case_count"`
	NotifyUser       bool                  `json:"notify_user"`
	Actions          []TemplateActionInput `json:"actions"`
}

// TemplateActionInput contains the admin-controlled settings for a timeout, kick, or ban.
type TemplateActionInput struct {
	ActionType             model.ActionType `json:"action_type"`
	TimeoutDurationSeconds int              `json:"timeout_duration_seconds,omitempty"`
	DeleteMessageSeconds   int              `json:"delete_message_seconds,omitempty"`
	MaxRetries             int              `json:"max_retries"`
}

// TemplateResponse presents the current version of a guild moderation policy, including archive state.
type TemplateResponse struct {
	// CaseDecayDays limits counting to recent cases; zero keeps all-time history.
	CaseDecayDays          int                            `json:"case_decay_days"`
	ID                     string                         `json:"id"`
	GuildID                string                         `json:"guild_id"`
	Slug                   string                         `json:"slug"`
	Name                   string                         `json:"name"`
	Description            string                         `json:"description"`
	ReasonTemplate         string                         `json:"reason_template"`
	Appealable             bool                           `json:"appealable"`
	Version                uint                           `json:"version"`
	CreatedByDiscordUserID string                         `json:"created_by_discord_user_id"`
	UpdatedByDiscordUserID string                         `json:"updated_by_discord_user_id"`
	ArchivedAt             *time.Time                     `json:"archived_at"`
	ContextFields          []TemplateContextFieldResponse `json:"context_fields"`
	Levels                 []TemplateLevelResponse        `json:"levels"`
}

// TemplateContextFieldResponse is the stable transport representation of a template context definition.
type TemplateContextFieldResponse struct {
	ID        string                 `json:"id"`
	Key       string                 `json:"key"`
	Label     string                 `json:"label"`
	FieldType model.ContextFieldType `json:"type"`
	Position  int                    `json:"position"`
	Required  bool                   `json:"required"`
}

// TemplatePolicy is the guild-neutral policy-only import and export shape.
type TemplatePolicy struct {
	// CaseDecayDays limits counting to recent cases; zero keeps all-time history.
	CaseDecayDays  int                         `json:"case_decay_days"`
	SchemaVersion  int                         `json:"schema_version"`
	Slug           string                      `json:"slug"`
	Name           string                      `json:"name"`
	Description    string                      `json:"description"`
	OfficialReason string                      `json:"official_reason"`
	Appealable     bool                        `json:"appealable"`
	ContextFields  []TemplateContextFieldInput `json:"context_fields"`
	Levels         []TemplateLevelInput        `json:"levels"`
}

// TemplateImportInput requires explicit confirmation before imported policy becomes active.
type TemplateImportInput struct {
	Confirm bool           `json:"confirm"`
	Policy  TemplatePolicy `json:"policy"`
}

// TemplateLevelDetails describes the case-count threshold and notification policy for a level.
type TemplateLevelDetails struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Position         int    `json:"position"`
	IsDefault        bool   `json:"is_default"`
	TriggerCaseCount int    `json:"trigger_case_count"`
	NotifyUser       bool   `json:"notify_user"`
}

// TemplateLevelResponse adds configured enforcement to a template level.
type TemplateLevelResponse struct {
	TemplateLevelDetails
	Actions []TemplateActionResponse `json:"actions"`
}

// TemplateActionResponse exposes admin-owned enforcement settings without worker implementation controls.
type TemplateActionResponse struct {
	ID                     string           `json:"id"`
	ActionType             model.ActionType `json:"action_type"`
	TimeoutDurationSeconds int              `json:"timeout_duration_seconds,omitempty"`
	DeleteMessageSeconds   int              `json:"delete_message_seconds,omitempty"`
	MaxRetries             uint8            `json:"max_retries"`
}

// EditInput copies this exact policy snapshot and its version for a guarded edit.
// Child slices are rebuilt so changing the input cannot mutate the response.
func (template TemplateResponse) EditInput() TemplateInput {
	input := TemplateInput{
		CaseDecayDays:   template.CaseDecayDays,
		ExpectedVersion: template.Version,
		Slug:            template.Slug,
		Name:            template.Name,
		Description:     template.Description,
		ReasonTemplate:  template.ReasonTemplate,
		Appealable:      template.Appealable,
	}
	for _, f := range template.ContextFields {
		input.ContextFields = append(input.ContextFields, TemplateContextFieldInput{
			Key:       f.Key,
			Label:     f.Label,
			FieldType: f.FieldType,
			Position:  f.Position,
			Required:  f.Required,
		})
	}
	for _, level := range template.Levels {
		in := TemplateLevelInput{
			Name:             level.Name,
			Position:         level.Position,
			IsDefault:        level.IsDefault,
			TriggerCaseCount: level.TriggerCaseCount,
			NotifyUser:       level.NotifyUser,
		}
		for _, action := range level.Actions {
			in.Actions = append(in.Actions, TemplateActionInput{
				ActionType:             action.ActionType,
				TimeoutDurationSeconds: action.TimeoutDurationSeconds,
				DeleteMessageSeconds:   action.DeleteMessageSeconds,
				MaxRetries:             int(action.MaxRetries),
			})
		}
		input.Levels = append(input.Levels, in)
	}
	return input
}
