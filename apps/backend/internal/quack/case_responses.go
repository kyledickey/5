package quack

import (
	"context"
	"encoding/json"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// caseResponse projects a committed case and its initial actions into the shared adapter response.
func caseResponse(created model.CreatedCase) CaseResponse {
	response := caseResponseFromModel(created.Case, created.ActionExecutions)
	response.EvidenceIncomplete = evidenceIncomplete(created.Evidence)
	return response
}

// caseResponseFromModel projects a stored case and its action executions into
// the staff API shape, decoding the JSON columns and reading the rule name and
// selected level out of the immutable template snapshot. EvidenceIncomplete is
// left false; callers that loaded evidence set it.
func caseResponseFromModel(caseModel model.Case, actionExecutions []model.CaseActionExecution) CaseResponse {
	response := CaseResponse{
		CreatedAt:               caseModel.CreatedAt,
		UpdatedAt:               caseModel.UpdatedAt,
		ID:                      caseModel.ID,
		GuildID:                 caseModel.GuildID,
		CaseNumber:              caseModel.CaseNumber,
		TemplateID:              caseModel.TemplateID,
		TemplateVersion:         caseModel.TemplateVersion,
		TargetDiscordUserID:     caseModel.TargetDiscordUserID,
		ModeratorDiscordUserID:  caseModel.ModeratorDiscordUserID,
		Reason:                  caseModel.Reason,
		Validity:                caseModel.Validity,
		Source:                  caseModel.Source,
		ContextChannelDiscordID: caseModel.ContextChannelDiscordID,
		ContextMessageDiscordID: caseModel.ContextMessageDiscordID,
		ContextURL:              caseModel.ContextURL,
		Metadata:                parseJSON(caseModel.MetadataJSON),
		ContextValues:           parseCaseContextValues(caseModel.ContextValuesJSON),
		VoidedReason:            caseModel.VoidedReason,
		VoidedAt:                caseModel.VoidedAt,
		ReplacementCaseID:       caseModel.ReplacementCaseID,
		ReplacesCaseID:          caseModel.ReplacesCaseID,
		SelectedLevel:           selectedLevelResponse(caseModel.TemplateSnapshotJSON),
		Actions:                 make([]CaseActionResponse, 0, len(actionExecutions)),
	}

	if snapshot := templateSnapshotResponse(caseModel.TemplateSnapshotJSON); snapshot != nil {
		response.RuleName = snapshot.Template.Name
	}
	for _, action := range actionExecutions {
		response.Actions = append(response.Actions, caseActionResponse(action))
	}

	return response
}

// caseResponsesForModels loads a page's actions in one query, retaining the
// case order and avoiding a database round trip for every row.
func (s *CaseService) caseResponsesForModels(ctx context.Context, cases []model.Case) ([]CaseResponse, error) {
	responses := make([]CaseResponse, 0, len(cases))
	if len(cases) == 0 {
		return responses, nil
	}
	ids := make([]string, len(cases))
	for i, item := range cases {
		ids[i] = item.ID
	}
	actions, err := s.store.ListCaseActionsForCases(ctx, ids)
	if err != nil {
		return nil, err
	}
	byCase := make(map[string][]model.CaseActionExecution, len(cases))
	for _, action := range actions {
		byCase[action.CaseID] = append(byCase[action.CaseID], action)
	}
	for _, caseModel := range cases {
		responses = append(responses, caseResponseFromModel(caseModel, byCase[caseModel.ID]))
	}
	return responses, nil
}

// caseActionResponse projects durable enforcement state without worker lease details.
func caseActionResponse(action model.CaseActionExecution) CaseActionResponse {
	return CaseActionResponse{
		ID:               action.ID,
		Position:         action.Position,
		ActionType:       action.ActionType,
		Status:           action.Status,
		TemplateActionID: action.TemplateActionID,
		IdempotencyKey:   action.IdempotencyKey,
		NotifyUser:       action.NotifyUser,
		NotificationType: action.NotificationType,
		MaxRetries:       action.MaxRetries,
		RetryBackoffMS:   action.RetryBackoffMS,
		SafeForRetry:     action.SafeForRetry,
		Irreversible:     action.Irreversible,
	}
}

// caseActionDetailResponses pairs each action execution with its recorded
// attempts (grouped by execution ID) and decoded config for the staff detail view.
func caseActionDetailResponses(actions []model.CaseActionExecution, attempts []model.CaseActionAttempt) []CaseActionDetailResponse {
	attemptsByExecution := map[string][]CaseActionAttemptResponse{}
	for _, attempt := range attempts {
		attemptsByExecution[attempt.ExecutionID] = append(attemptsByExecution[attempt.ExecutionID], CaseActionAttemptResponse{
			ID:              attempt.ID,
			ExecutionID:     attempt.ExecutionID,
			AttemptNumber:   attempt.AttemptNumber,
			Status:          attempt.Status,
			WorkerID:        attempt.WorkerID,
			StartedAt:       attempt.StartedAt,
			FinishedAt:      attempt.FinishedAt,
			DurationMS:      attempt.DurationMS,
			ErrorCode:       attempt.ErrorCode,
			ErrorMessage:    attempt.ErrorMessage,
			RequestPayload:  parseJSON(attempt.RequestPayloadJSON),
			ResponsePayload: parseJSON(attempt.ResponsePayloadJSON),
		})
	}

	responses := make([]CaseActionDetailResponse, 0, len(actions))
	for _, action := range actions {
		responses = append(responses, CaseActionDetailResponse{
			CaseActionResponse: caseActionResponseWithExpiry(action, attempts),
			ConfigSnapshot:     parseJSON(action.ConfigSnapshotJSON),
			AttemptCount:       action.AttemptCount,
			LastErrorCode:      action.LastErrorCode,
			LastError:          action.LastError,
			StartedAt:          action.StartedAt,
			FinishedAt:         action.FinishedAt,
			NextRetryAt:        action.NextRetryAt,
			Attempts:           attemptsByExecution[action.ID],
		})
	}
	return responses
}

// caseEventResponses projects immutable timeline events for authorized staff.
func caseEventResponses(events []model.CaseEvent) []CaseEventResponse {
	responses := make([]CaseEventResponse, 0, len(events))
	for _, event := range events {
		responses = append(responses, CaseEventResponse{
			ID:                 event.ID,
			CreatedAt:          event.CreatedAt,
			UpdatedAt:          event.UpdatedAt,
			EventType:          event.EventType,
			ActorDiscordUserID: event.ActorDiscordUserID,
			ActorType:          event.ActorType,
			Visibility:         event.Visibility,
			Body:               event.Body,
			Metadata:           parseJSON(event.MetadataJSON),
		})
	}
	return responses
}

// selectedLevelResponse reads the escalation decision from the immutable case
// snapshot, returning nil for malformed or level-less snapshots.
func selectedLevelResponse(snapshotJSON string) *CaseSelectedLevel {
	var snapshot struct {
		SelectedLevel CaseSelectedLevel `json:"selected_level"`
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil || snapshot.SelectedLevel.ID == "" {
		return nil
	}
	return &snapshot.SelectedLevel
}

// templateSnapshotResponse decodes the policy snapshot retained by historical
// case views. Older snapshots stored action settings under a nested "config"
// object; those are decoded into the current flat fields so history stays readable.
func templateSnapshotResponse(snapshotJSON string) *CaseTemplateSnapshotResponse {
	var stored struct {
		Template      templateSnapshotTemplate       `json:"template"`
		SelectedLevel CaseSelectedLevel              `json:"selected_level"`
		Actions       []json.RawMessage              `json:"actions"`
		ContextFields []TemplateContextFieldResponse `json:"context_fields"`
		ContextValues []CaseContextValueResponse     `json:"context_values"`
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &stored); err != nil || stored.Template.ID == "" {
		return nil
	}
	snapshot := CaseTemplateSnapshotResponse{
		Template:      stored.Template,
		SelectedLevel: stored.SelectedLevel,
		Actions:       make([]templateSnapshotAction, 0, len(stored.Actions)),
		ContextFields: stored.ContextFields,
		ContextValues: stored.ContextValues,
	}
	for _, raw := range stored.Actions {
		var action templateSnapshotAction
		if err := json.Unmarshal(raw, &action); err != nil {
			continue
		}
		if action.TimeoutDurationSeconds == 0 && action.DeleteMessageSeconds == 0 {
			var legacy struct {
				Config json.RawMessage `json:"config"`
			}
			if err := json.Unmarshal(raw, &legacy); err == nil && len(legacy.Config) > 0 {
				settings := decodeTemplateActionConfig(string(legacy.Config))
				action.TimeoutDurationSeconds = settings.DurationSeconds
				action.DeleteMessageSeconds = settings.DeleteMessageSeconds
			}
		}
		snapshot.Actions = append(snapshot.Actions, action)
	}
	return &snapshot
}

// caseEvidenceResponses groups attachments under their snapshots. For member
// views the original CDN URL is hidden whenever a managed copy exists.
func caseEvidenceResponses(
	snapshots []model.CaseEvidenceSnapshot,
	attachments []model.CaseEvidenceAttachment,
	member bool,
) []CaseEvidenceResponse {
	byEvidence := map[string][]CaseEvidenceAttachmentResponse{}
	for _, item := range attachments {
		original := item.OriginalURL
		if member && item.PreservedURL != "" {
			original = ""
		}
		byEvidence[item.EvidenceID] = append(byEvidence[item.EvidenceID], CaseEvidenceAttachmentResponse{
			Filename:     item.Filename,
			ContentType:  item.ContentType,
			SizeBytes:    item.SizeBytes,
			OriginalURL:  original,
			PreservedURL: item.PreservedURL,
			CopyOutcome:  item.CopyOutcome,
			Warning:      item.Warning,
		})
	}
	out := make([]CaseEvidenceResponse, 0, len(snapshots))
	for _, item := range snapshots {
		out = append(out, CaseEvidenceResponse{
			ID:                  item.ID,
			AuthorDiscordUserID: item.AuthorDiscordUserID,
			MessageURL:          item.MessageURL,
			Content:             item.Content,
			MessageCreatedAt:    item.MessageCreatedAt,
			MessageEditedAt:     item.MessageEditedAt,
			Embeds:              parseJSON(item.EmbedsJSON),
			CaptureOutcome:      item.CaptureOutcome,
			CaptureWarning:      item.CaptureWarning,
			Attachments:         byEvidence[item.ID],
		})
	}
	return out
}

// caseNotificationResponse hides internal delivery diagnostics from affected members.
func caseNotificationResponse(item *model.CaseNotification, member bool) *CaseNotificationResponse {
	if item == nil {
		return nil
	}
	response := &CaseNotificationResponse{
		Status:        item.Status,
		AttemptCount:  item.AttemptCount,
		LastErrorCode: item.LastErrorCode,
		LastError:     item.LastError,
		SentAt:        item.SentAt,
	}
	if member {
		response.LastErrorCode = ""
		response.LastError = ""
	}
	return response
}

// evidenceIncomplete reports missing content or files without exposing staff evidence.
func evidenceIncomplete(evidence []model.CaseEvidenceSnapshot) bool {
	for _, item := range evidence {
		if item.CaptureWarning != "" || item.CaptureOutcome == "unavailable" {
			return true
		}
	}
	return false
}

// caseActionResponseWithExpiry enriches a staff action using confirmed timeout facts.
func caseActionResponseWithExpiry(action model.CaseActionExecution, attempts []model.CaseActionAttempt) CaseActionResponse {
	result := caseActionResponse(action)
	if action.ActionType == model.ActionTimeoutUser && action.Status == model.ActionExecutionSucceeded {
		result.TimeoutUntil = RecordedTimeoutUntil(action.ID, attempts)
	}
	return result
}
