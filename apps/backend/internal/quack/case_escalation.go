package quack

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// selectTemplateLevel chooses the highest escalation whose configured case-count threshold is met, falling back to the default level.
func (s *CaseService) selectTemplateLevel(ctx context.Context, guildID, targetDiscordUserID string, template *model.ExpandedCaseTemplate) (*selectedTemplateLevel, error) {
	if template == nil {
		return nil, validationCaseError("template is required")
	}

	var fallback *selectedTemplateLevel
	var best *selectedTemplateLevel
	matchedCaseCount, err := s.matchingTemplateCaseCount(ctx, guildID, targetDiscordUserID, template.Template)
	if err != nil {
		return nil, err
	}
	for _, expandedLevel := range template.Levels {
		level := expandedLevel.Level
		if len(expandedLevel.Actions) > 1 {
			return nil, validationCaseError("template level has more than one enforcement action")
		}
		if level.IsDefault {
			fallback = &selectedTemplateLevel{
				Level:            level,
				Actions:          expandedLevel.Actions,
				MatchedCaseCount: matchedCaseCount,
			}
			continue
		}

		if level.TriggerCaseCount <= 0 {
			return nil, validationCaseError("escalation level trigger_case_count must be positive")
		}

		if matchedCaseCount < int64(level.TriggerCaseCount) {
			continue
		}
		candidate := &selectedTemplateLevel{
			Level:            level,
			Actions:          expandedLevel.Actions,
			MatchedCaseCount: matchedCaseCount,
		}
		if best == nil || level.TriggerCaseCount > best.Level.TriggerCaseCount {
			best = candidate
		}
	}

	if fallback == nil {
		return nil, validationCaseError("template has no default level")
	}
	if best != nil {
		return best, nil
	}

	return fallback, nil
}

// matchingTemplateCaseCount returns the relevant historical count plus the case currently being created, matching the user-facing meaning of a trigger count.
func (s *CaseService) matchingTemplateCaseCount(ctx context.Context, guildID, targetDiscordUserID string, template model.CaseTemplate) (int64, error) {
	var since *time.Time
	if template.CaseDecayDays > 0 {
		cutoff := time.Now().UTC().Add(-time.Duration(template.CaseDecayDays) * 24 * time.Hour)
		since = &cutoff
	}
	priorCount, err := s.store.CountTemplateCasesForTarget(ctx, model.CountTemplateCasesForTargetParams{
		GuildID:             guildID,
		TemplateID:          template.ID,
		CreatedAtOrAfter:    since,
		TargetDiscordUserID: targetDiscordUserID,
	})
	if err != nil {
		return 0, err
	}
	return priorCount + 1, nil
}

// buildTemplateSnapshot builds template snapshot from validated domain state.
func buildTemplateSnapshot(template model.CaseTemplate, fields []model.CaseTemplateContextField, valuesJSON string, selectedLevel selectedTemplateLevel) (string, error) {
	snapshot := templateSnapshot{
		Template: templateSnapshotTemplate{
			ID:             template.ID,
			Slug:           template.Slug,
			Name:           template.Name,
			Version:        template.Version,
			ReasonTemplate: template.ReasonTemplate,
			CaseDecayDays:  template.CaseDecayDays,
			Appealable:     template.Appealable,
		},
		SelectedLevel: CaseSelectedLevel{
			TemplateLevelDetails: templateLevelDetails(selectedLevel.Level),
			MatchedCaseCount:     selectedLevel.MatchedCaseCount,
		},
		Actions:       make([]templateSnapshotAction, 0, len(selectedLevel.Actions)),
		ContextFields: make([]TemplateContextFieldResponse, 0, len(fields)),
	}
	_ = json.Unmarshal([]byte(valuesJSON), &snapshot.ContextValues)
	for _, field := range fields {
		snapshot.ContextFields = append(snapshot.ContextFields, TemplateContextFieldResponse{ID: field.ID, Key: field.Key, Label: field.Label, FieldType: field.FieldType, Position: field.Position, Required: field.Required})
	}

	for _, action := range selectedLevel.Actions {
		settings := templateActionResponse(action)
		snapshot.Actions = append(snapshot.Actions, templateSnapshotAction{
			ID:                     action.ID,
			ActionType:             action.ActionType,
			TimeoutDurationSeconds: settings.TimeoutDurationSeconds,
			DeleteMessageSeconds:   settings.DeleteMessageSeconds,
			MaxRetries:             action.MaxRetries,
		})
	}

	body, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal case template snapshot: %w", err)
	}
	return string(body), nil
}
