package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/quackdiscord/bot/internal/quack/actionmods"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// prepareNotification opens a DM channel before an irreversible membership
// change (kick or ban) so the member can still be reached afterwards. Failure
// to prepare is recorded on the notification and never blocks enforcement.
func (s *ActionService) prepareNotification(ctx context.Context, item model.Case) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	notification, err := s.store.GetCaseNotification(ctx, item.ID)
	if err != nil {
		slog.ErrorContext(ctx, "Could not load notification before enforcement", "case_id", item.ID, "error", err)
	}
	if err != nil || notification == nil || notification.Status != model.NotificationPending {
		return
	}
	prepared, ok := s.discord.(DiscordPreparedDMClient)
	if !ok {
		// best-effort: the missing adapter is recorded for diagnostics; enforcement proceeds regardless
		_ = s.store.PrepareCaseNotification(ctx, item.ID, "", "prepared DM adapter is unavailable")
		return
	}
	channelID, prepareErr := prepared.PrepareDM(ctx, item.TargetDiscordUserID)
	message := ""
	if prepareErr != nil {
		message = redactDiscordError(prepareErr)
	}
	if err := s.store.PrepareCaseNotification(ctx, item.ID, channelID, message); err != nil {
		slog.ErrorContext(ctx, "Could not record prepared notification", "case_id", item.ID, "error", err)
	}
}

// processNotification delivers member-visible outcome facts after enforcement is
// terminal, preserving the adapter-rendered receipt behind the existing send fence.
func (s *ActionService) processNotification(ctx context.Context, workerID, caseID string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	claimed, err := s.store.ClaimCaseNotification(ctx, model.ClaimCaseNotificationParams{CaseID: caseID, WorkerID: workerID})
	if err != nil || claimed == nil {
		return err
	}
	item, err := s.store.GetCaseByID(ctx, caseID)
	if err != nil {
		return err
	}
	if item == nil {
		return ErrCaseNotFound
	}
	guild, err := s.store.GetGuildByID(ctx, item.GuildID)
	if err != nil {
		return err
	}
	settings, err := s.store.GetGuildSettings(ctx, item.GuildID)
	if err != nil {
		return err
	}
	actions, err := s.store.ListCaseActionExecutions(ctx, item.ID)
	if err != nil {
		return err
	}
	// Read recorded Discord responses so displayed expiry times reflect the actual successful attempt.
	executionIDs := make([]string, 0, len(actions))
	for _, action := range actions {
		executionIDs = append(executionIDs, action.ID)
	}
	var attempts []model.CaseActionAttempt
	if len(executionIDs) > 0 {
		// best-effort: without attempts the DM simply omits the timeout expiry
		attempts, _ = s.store.ListCaseActionAttempts(ctx, executionIDs)
	}
	request := caseNotificationRequest(*item, guild, settings, actions, attempts)
	request.PreparedChannelDiscordID = claimed.PreparedChannelDiscordID
	request.DashboardBaseURL = s.dashboardBaseURL
	if err := s.store.BeginCaseNotificationDelivery(ctx, claimed.ID, claimed.LeaseToken); err != nil {
		return err
	}
	var receipt CaseNotificationReceipt
	var sendErr error
	if client, ok := s.discord.(DiscordCaseNotificationClient); ok {
		receipt, sendErr = client.SendCaseNotification(ctx, request)
	} else {
		sendErr = errors.New("discord case notification adapter is unavailable")
	}
	params := model.CompleteCaseNotificationParams{
		NotificationID:           claimed.ID,
		LeaseToken:               claimed.LeaseToken,
		WorkerID:                 workerID,
		RenderedMessage:          receipt.RenderedMessage,
		PreparedChannelDiscordID: claimed.PreparedChannelDiscordID,
	}
	if sendErr != nil {
		result := actionmods.ResultFromError(sendErr)
		params.Status = model.NotificationFailed
		params.ErrorCode = result.ErrorCode
		params.ErrorMessage = result.Error
		params.EventType = model.CaseEventNotificationFailed
	} else {
		params.Status = model.NotificationSent
		params.EventType = model.CaseEventNotificationSent
		params.DeliveryMessageDiscordID = receipt.MessageID
	}
	if err := s.store.CompleteCaseNotification(ctx, params); err != nil {
		return fmt.Errorf("record case notification result: %w", err)
	}
	level := slog.LevelInfo
	if sendErr != nil {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "Case notification recorded", "case_id", caseID, "status", params.Status, "error_code", params.ErrorCode)
	return nil
}

// redactDiscordError converts adapter failures to safe durable notification diagnostics.
func redactDiscordError(err error) string {
	if err == nil {
		return ""
	}
	var discordErr actionmods.DiscordError
	if errors.As(err, &discordErr) {
		return discordErr.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Discord request timed out"
	}
	return "Discord request failed"
}

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
