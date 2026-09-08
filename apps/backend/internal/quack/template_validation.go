package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// normalizedTemplate holds a validated policy ready to persist as one template version.
type normalizedTemplate struct {
	template      model.CaseTemplate
	contextFields []model.CaseTemplateContextField
	levels        []model.ExpandedCaseTemplateLevel
}

// validate checks validate before state is read or changed.
func (s *TemplateService) validate(ctx context.Context, guildContext *GuildStaffContext, templateID string, input TemplateInput) (*normalizedTemplate, error) {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, validationError("missing guild context")
	}

	slug := strings.ToLower(strings.TrimSpace(input.Slug))
	if !templateSlugPattern.MatchString(slug) {
		return nil, validationError("slug must be 2-64 lowercase letters, numbers, underscores, or hyphens")
	}

	if existing, err := s.store.GetCaseTemplateBySlug(ctx, guildContext.Guild.ID, slug); err != nil {
		return nil, err
	} else if existing != nil && existing.ID != templateID {
		return nil, validationError("slug already exists for this guild")
	}

	if input.CaseDecayDays < 0 || input.CaseDecayDays > MaxCaseDecayDays {
		return nil, validationError("case_decay_days must be between 0 and 36500; 0 keeps all-time counting")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, validationError("name is required")
	}

	reasonTemplate := strings.TrimSpace(input.ReasonTemplate)
	if reasonTemplate == "" {
		return nil, validationError("reason_template is required")
	}

	levels, err := normalizeLevels(input.Levels)
	if err != nil {
		return nil, err
	}

	template := model.CaseTemplate{
		GuildID:                guildContext.Guild.ID,
		Slug:                   slug,
		Name:                   name,
		Description:            strings.TrimSpace(input.Description),
		ReasonTemplate:         reasonTemplate,
		CaseDecayDays:          input.CaseDecayDays,
		Appealable:             input.Appealable,
		CreatedByDiscordUserID: guildContext.Staff.DiscordUserID,
		UpdatedByDiscordUserID: guildContext.Staff.DiscordUserID,
	}

	contextFields, err := normalizeContextFields(input.ContextFields)
	if err != nil {
		return nil, err
	}
	return &normalizedTemplate{template: template, contextFields: contextFields, levels: levels}, nil
}

// normalizeContextFields validates identifiers, types, unique ordering, and member-visible field bounds.
func normalizeContextFields(inputs []TemplateContextFieldInput) ([]model.CaseTemplateContextField, error) {
	if len(inputs) > 10 {
		return nil, validationError("at most 10 context fields are allowed")
	}
	keys := map[string]struct{}{}
	positions := map[int]struct{}{}
	fields := make([]model.CaseTemplateContextField, 0, len(inputs))
	for i, input := range inputs {
		key := strings.ToLower(strings.TrimSpace(input.Key))
		if !templateSlugPattern.MatchString(key) {
			return nil, validationError("context field key must be 2-64 lowercase letters, numbers, underscores, or hyphens")
		}
		if _, ok := keys[key]; ok {
			return nil, validationError("context field keys must be unique")
		}
		keys[key] = struct{}{}
		label := strings.TrimSpace(input.Label)
		if label == "" || len([]rune(label)) > 100 {
			return nil, validationError("context field label must be 1-100 characters")
		}
		if !validContextFieldType(input.FieldType) {
			return nil, validationError("context field type is invalid")
		}
		position := input.Position
		if position == 0 {
			position = i + 1
		}
		if position < 1 {
			return nil, validationError("context field position must be positive")
		}
		if _, ok := positions[position]; ok {
			return nil, validationError("context field positions must be unique")
		}
		positions[position] = struct{}{}
		fields = append(fields, model.CaseTemplateContextField{Key: key, Label: label, FieldType: input.FieldType, Position: position, Required: input.Required})
	}
	return fields, nil
}

// validContextFieldType reports whether a field uses one of the five v5 value shapes.
func validContextFieldType(value model.ContextFieldType) bool {
	switch value {
	case model.ContextFieldShortText, model.ContextFieldLongText, model.ContextFieldBoolean, model.ContextFieldNumber, model.ContextFieldMessageLink:
		return true
	default:
		return false
	}
}

// normalizeLevels requires one default, unique positive thresholds, and at most one action per level.
func normalizeLevels(inputs []TemplateLevelInput) ([]model.ExpandedCaseTemplateLevel, error) {
	if len(inputs) == 0 {
		return nil, validationError("at least one level is required")
	}

	levels := make([]model.ExpandedCaseTemplateLevel, 0, len(inputs))
	defaultCount := 0
	thresholds := make(map[int]struct{}, len(inputs))

	for i, input := range inputs {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			return nil, validationError("level name is required")
		}
		if !input.IsDefault && input.TriggerCaseCount <= 0 {
			return nil, validationError("escalation level trigger_case_count must be positive")
		}
		if input.IsDefault {
			defaultCount++
			if input.TriggerCaseCount != 0 {
				return nil, validationError("default level cannot have escalation triggers")
			}
		} else {
			if _, duplicate := thresholds[input.TriggerCaseCount]; duplicate {
				return nil, validationError("escalation level trigger_case_count values must be distinct")
			}
			thresholds[input.TriggerCaseCount] = struct{}{}
		}
		actions, err := normalizeActions(input.Actions)
		if err != nil {
			return nil, err
		}

		position := input.Position
		if position == 0 {
			position = i + 1
		}
		if position < 0 {
			return nil, validationError("level position must be non-negative")
		}

		levels = append(levels, model.ExpandedCaseTemplateLevel{
			Level: model.CaseTemplateLevel{
				Position:         position,
				Name:             name,
				IsDefault:        input.IsDefault,
				TriggerCaseCount: input.TriggerCaseCount,
				NotifyUser:       input.NotifyUser,
			},
			Actions: actions,
		})
	}

	if defaultCount == 0 {
		return nil, validationError("exactly one default level is required")
	}
	if defaultCount > 1 {
		return nil, validationError("only one default level is allowed")
	}

	return levels, nil
}

// normalizeActions validates the single permitted enforcement action and its admin-owned settings.
func normalizeActions(inputs []TemplateActionInput) ([]model.CaseTemplateLevelAction, error) {
	if len(inputs) > 1 {
		return nil, validationError("a template level can contain at most one enforcement action")
	}
	actions := make([]model.CaseTemplateLevelAction, 0, len(inputs))
	for _, input := range inputs {
		if input.ActionType == "record_warning" {
			return nil, validationError("record_warning is not a template action; creating a case records the warning")
		}
		if input.ActionType == model.ActionSendDM {
			return nil, validationError("send_dm is not a template action; set notify_user on the level")
		}
		if !validActionType(input.ActionType) {
			return nil, validationError("action_type is invalid")
		}
		if input.MaxRetries < 0 || input.MaxRetries > MaxTemplateSafeRetries {
			return nil, validationError(fmt.Sprintf("max_retries must be between 0 and %d", MaxTemplateSafeRetries))
		}
		configJSON, err := normalizeTemplateActionConfig(input)
		if err != nil {
			return nil, err
		}

		actions = append(actions, model.CaseTemplateLevelAction{
			ActionType: input.ActionType,
			ConfigJSON: configJSON,
			MaxRetries: uint8(input.MaxRetries),
		})
	}

	return actions, nil
}

// templateActionConfig is the internal canonical snapshot for product-owned action settings.
type templateActionConfig struct {
	DurationSeconds      int `json:"duration_seconds,omitempty"`
	DeleteMessageSeconds int `json:"delete_message_seconds,omitempty"`
}

// normalizeTemplateActionConfig validates typed settings and stores only the setting owned by the selected action.
func normalizeTemplateActionConfig(input TemplateActionInput) (string, error) {
	config := templateActionConfig{}
	switch input.ActionType {
	case model.ActionTimeoutUser:
		if input.TimeoutDurationSeconds <= 0 || input.TimeoutDurationSeconds > MaxTimeoutDurationSeconds {
			return "", validationError(fmt.Sprintf("timeout_duration_seconds must be between 1 and %d", MaxTimeoutDurationSeconds))
		}
		if input.DeleteMessageSeconds != 0 {
			return "", validationError("delete_message_seconds is only valid for ban actions")
		}
		config.DurationSeconds = input.TimeoutDurationSeconds
	case model.ActionKickUser:
		if input.TimeoutDurationSeconds != 0 || input.DeleteMessageSeconds != 0 {
			return "", validationError("kick actions do not accept action settings")
		}
	case model.ActionBanUser:
		if input.TimeoutDurationSeconds != 0 {
			return "", validationError("timeout_duration_seconds is only valid for timeout actions")
		}
		if input.DeleteMessageSeconds < 0 || input.DeleteMessageSeconds > MaxBanDeleteMessageSeconds {
			return "", validationError(fmt.Sprintf("delete_message_seconds must be between 0 and %d", MaxBanDeleteMessageSeconds))
		}
		config.DeleteMessageSeconds = input.DeleteMessageSeconds
	}
	body, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal template action config: %w", err)
	}
	return string(body), nil
}

// normalizeJSONObject accepts a JSON object, treating an omitted value as an empty object.
func normalizeJSONObject(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "{}", nil
	}

	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", err
	}
	if object == nil {
		return "", errors.New("not an object")
	}

	body, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// validActionType recognizes the three enforcement choices supported by v5 templates.
func validActionType(actionType model.ActionType) bool {
	switch actionType {
	case model.ActionTimeoutUser, model.ActionKickUser, model.ActionBanUser:
		return true
	default:
		return false
	}
}

// validationError wraps a safe template validation message for transport-specific error mapping.
func validationError(message string) error {
	return fmt.Errorf("%w: %s", ErrTemplateValidation, message)
}
