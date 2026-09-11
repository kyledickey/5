package quack

import (
	"encoding/json"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// CaseNotificationRequest contains only member-visible facts and routing identity.
// It excludes raw cases, template JSON, staff context, evidence and attempt payloads.
type CaseNotificationRequest struct {
	TargetDiscordUserID, PreparedChannelDiscordID, DashboardBaseURL, GuildID, CaseID string
	GuildName, RuleName, Reason, Introduction, Footer                                string
	CaseNumber                                                                       uint64
	CreatedAt                                                                        time.Time
	IncludeAppealInstructions                                                        bool
	// AppealControl preserves eligibility even for historical partial template snapshots.
	AppealControl bool
	Outcomes      []CaseNotificationOutcome
}

// CaseNotificationOutcome projects recorded enforcement without private diagnostics.
// TimeoutUntil is present only when a successful attempt recorded a valid expiry.
type CaseNotificationOutcome struct {
	ActionType   model.ActionType
	Status       model.ActionExecutionStatus
	TimeoutUntil *time.Time
}

// CaseNotificationReceipt returns the rendered attempt body even on delivery error,
// allowing core completion to retain the existing historical notification receipt.
type CaseNotificationReceipt struct {
	RenderedMessage, ChannelID, MessageID string
}

// caseNotificationRequest selects immutable rule facts and actual execution results;
// Discord wording and escaping belong to the notification adapter.
func caseNotificationRequest(
	item model.Case,
	guild *model.Guild,
	settings *model.GuildSettings,
	actions []model.CaseActionExecution,
	attempts []model.CaseActionAttempt,
) CaseNotificationRequest {
	request := CaseNotificationRequest{
		AppealControl:       caseSnapshotAppealable(item.TemplateSnapshotJSON),
		TargetDiscordUserID: item.TargetDiscordUserID,
		GuildID:             item.GuildID,
		CaseID:              item.ID,
		Reason:              item.Reason,
		CaseNumber:          item.CaseNumber,
		CreatedAt:           item.CreatedAt,
	}
	if guild != nil {
		request.GuildName = guild.Name
	}
	if settings != nil {
		request.Introduction = settings.NotificationIntroduction
		request.Footer = settings.NotificationFooter
	}
	if snapshot := templateSnapshotResponse(item.TemplateSnapshotJSON); snapshot != nil {
		request.RuleName = snapshot.Template.Name
		request.IncludeAppealInstructions = snapshot.Template.Appealable
	}
	for _, action := range actions {
		outcome := CaseNotificationOutcome{ActionType: action.ActionType, Status: action.Status}
		if action.Status == model.ActionExecutionSucceeded && action.ActionType == model.ActionTimeoutUser {
			for _, attempt := range attempts {
				if attempt.ExecutionID != action.ID || attempt.Status != model.ActionAttemptSucceeded {
					continue
				}
				var response struct {
					Until string `json:"timeout_until"`
				}
				if json.Unmarshal([]byte(attempt.ResponsePayloadJSON), &response) != nil {
					continue
				}
				if until, err := time.Parse(time.RFC3339, response.Until); err == nil {
					outcome.TimeoutUntil = &until
					break
				}
			}
		}
		request.Outcomes = append(request.Outcomes, outcome)
	}
	return request
}
