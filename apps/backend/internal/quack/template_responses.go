package quack

import (
	"encoding/json"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// templateResponse projects an expanded template into the transport shape.
// ContextFields and Levels are always non-nil so JSON renders [] rather than null.
func templateResponse(expanded model.ExpandedCaseTemplate) TemplateResponse {
	template := expanded.Template
	response := TemplateResponse{
		ID:                     template.ID,
		GuildID:                template.GuildID,
		Slug:                   template.Slug,
		Name:                   template.Name,
		Description:            template.Description,
		ReasonTemplate:         template.ReasonTemplate,
		CaseDecayDays:          template.CaseDecayDays,
		Appealable:             template.Appealable,
		Version:                template.Version,
		CreatedByDiscordUserID: template.CreatedByDiscordUserID,
		UpdatedByDiscordUserID: template.UpdatedByDiscordUserID,
		ArchivedAt:             template.ArchivedAt,
		ContextFields:          make([]TemplateContextFieldResponse, 0, len(expanded.ContextFields)),
		Levels:                 make([]TemplateLevelResponse, 0, len(expanded.Levels)),
	}
	for _, field := range expanded.ContextFields {
		response.ContextFields = append(response.ContextFields, TemplateContextFieldResponse{
			ID:        field.ID,
			Key:       field.Key,
			Label:     field.Label,
			FieldType: field.FieldType,
			Position:  field.Position,
			Required:  field.Required,
		})
	}

	for _, level := range expanded.Levels {
		levelResponse := TemplateLevelResponse{
			TemplateLevelDetails: templateLevelDetails(level.Level),
			Actions:              make([]TemplateActionResponse, 0, len(level.Actions)),
		}
		for _, action := range level.Actions {
			levelResponse.Actions = append(levelResponse.Actions, templateActionResponse(action))
		}
		response.Levels = append(response.Levels, levelResponse)
	}

	return response
}

// templateLevelDetails copies the level fields shared by template and case snapshot responses.
func templateLevelDetails(level model.CaseTemplateLevel) TemplateLevelDetails {
	return TemplateLevelDetails{
		ID:               level.ID,
		Name:             level.Name,
		Position:         level.Position,
		IsDefault:        level.IsDefault,
		TriggerCaseCount: level.TriggerCaseCount,
		NotifyUser:       level.NotifyUser,
	}
}

// templateActionResponse projects canonical or compatible stored settings into the typed product contract.
func templateActionResponse(action model.CaseTemplateLevelAction) TemplateActionResponse {
	config := decodeTemplateActionConfig(action.ConfigJSON)
	return TemplateActionResponse{
		ID:                     action.ID,
		ActionType:             action.ActionType,
		TimeoutDurationSeconds: config.DurationSeconds,
		DeleteMessageSeconds:   config.DeleteMessageSeconds,
		MaxRetries:             action.MaxRetries,
	}
}

// decodeTemplateActionConfig reads canonical settings and the previous duration-minutes representation without exposing it.
func decodeTemplateActionConfig(body string) templateActionConfig {
	var stored struct {
		DurationSeconds      int `json:"duration_seconds"`
		DurationMinutes      int `json:"duration_minutes"`
		DeleteMessageSeconds int `json:"delete_message_seconds"`
	}
	if err := json.Unmarshal([]byte(body), &stored); err != nil {
		return templateActionConfig{}
	}
	durationSeconds := stored.DurationSeconds
	if durationSeconds == 0 && stored.DurationMinutes > 0 {
		durationSeconds = stored.DurationMinutes * 60
	}
	return templateActionConfig{
		DurationSeconds:      durationSeconds,
		DeleteMessageSeconds: stored.DeleteMessageSeconds,
	}
}

// parseJSON decodes stored JSON for a response, substituting an empty object
// for empty or malformed input so responses never carry null metadata.
func parseJSON(body string) any {
	if body == "" {
		return map[string]any{}
	}

	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		return map[string]any{}
	}
	return value
}
