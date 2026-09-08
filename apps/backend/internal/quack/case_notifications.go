package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/discordtext"
	actionmods "github.com/quackdiscord/bot/internal/quack/actionmods"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// prepareNotification opens a DM channel before an irreversible membership change without coupling preparation failure to enforcement.
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

// processNotification renders and attempts the one case-level notification after enforcement reaches a terminal outcome.
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
	if reader, ok := s.store.(interface {
		ListCaseActionAttempts(context.Context, []string) ([]model.CaseActionAttempt, error)
	}); ok && len(executionIDs) > 0 {
		attempts, _ = reader.ListCaseActionAttempts(ctx, executionIDs)
	}
	message := renderCaseNotification(*item, guild, settings, actions, attempts...)
	if err := s.store.BeginCaseNotificationDelivery(ctx, claimed.ID, claimed.LeaseToken); err != nil {
		return err
	}
	var response map[string]any
	var sendErr error
	appealable := caseSnapshotAppealable(item.TemplateSnapshotJSON)
	if appealable && s.dashboardBaseURL != "" {
		if client, ok := s.discord.(DiscordCaseNotificationClient); ok {
			response, sendErr = client.SendCaseNotification(ctx, item.TargetDiscordUserID, claimed.PreparedChannelDiscordID, message, s.dashboardBaseURL, item.GuildID, item.ID)
		} else {
			sendErr = errors.New("Discord appeal notification adapter is unavailable")
		}
	} else if claimed.PreparedChannelDiscordID != "" {
		if prepared, ok := s.discord.(DiscordPreparedDMClient); ok {
			response, sendErr = prepared.SendPreparedDM(ctx, claimed.PreparedChannelDiscordID, message)
		} else {
			sendErr = errors.New("prepared DM adapter is unavailable")
		}
	} else if s.discord != nil {
		response, sendErr = s.discord.SendDM(ctx, item.TargetDiscordUserID, message)
	} else {
		sendErr = errors.New("Discord action client is unavailable")
	}
	params := model.CompleteCaseNotificationParams{NotificationID: claimed.ID, LeaseToken: claimed.LeaseToken, WorkerID: workerID, RenderedMessage: message, PreparedChannelDiscordID: claimed.PreparedChannelDiscordID}
	if sendErr != nil {
		result := actionmods.ResultFromError(sendErr)
		params.Status = model.NotificationFailed
		params.ErrorCode = result.ErrorCode
		params.ErrorMessage = result.Error
		params.EventType = model.CaseEventNotificationFailed
	} else {
		params.Status = model.NotificationSent
		params.EventType = model.CaseEventNotificationSent
		if id, ok := response["message_id"].(string); ok {
			params.DeliveryMessageDiscordID = id
		}
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

// renderCaseNotification describes the rule and recorded outcome without staff
// context, evidence or moderator identity.
func renderCaseNotification(item model.Case, guild *model.Guild, settings *model.GuildSettings, actions []model.CaseActionExecution, attempts ...model.CaseActionAttempt) string {
	guildName := "this server"
	if guild != nil && strings.TrimSpace(guild.Name) != "" {
		guildName = guild.Name
	}
	server := "**" + notificationPlain(guildName) + "**"
	icon, lead := "warn", "You received a warning in "+server
	primary := -1
	removed := false
	for i, action := range actions {
		if action.Status != model.ActionExecutionSucceeded {
			continue
		}
		switch action.ActionType {
		case model.ActionTimeoutUser:
			icon, lead = "timeout", "You’ve been timed out in "+server
		case model.ActionKickUser:
			icon, lead = "kick", "You’ve been removed from "+server
		case model.ActionBanUser:
			icon, lead = "ban", "You’ve been banned from "+server
		case model.ActionRemoveTimeout:
			icon, lead, removed = "untimeout", "Your timeout in "+server+" has been removed", true
		case model.ActionUnbanUser:
			icon, lead, removed = "unban", "Your ban from "+server+" has been removed", true
		default:
			continue
		}
		primary = i
		break
	}
	snapshot := templateSnapshotResponse(item.TemplateSnapshotJSON)
	parts := []string{}
	if snapshot != nil && strings.TrimSpace(snapshot.Template.Name) != "" {
		rule := "**" + notificationPlain(snapshot.Template.Name) + "**"
		if removed {
			parts = append(parts, "This updates your case for "+rule+".")
		} else {
			lead += " for " + rule
		}
	}
	if settings != nil && strings.TrimSpace(settings.NotificationIntroduction) != "" {
		parts = append(parts, notificationPlain(strings.TrimSpace(settings.NotificationIntroduction)))
	}
	for i, action := range actions {
		if action.Status == model.ActionExecutionFailed && primary == -1 {
			icon = "error"
		}
		if i != primary && action.ActionType != model.ActionSendDM {
			parts = append(parts, discordtext.ActionSentence(action.ActionType, action.Status))
		}
		if action.Status != model.ActionExecutionSucceeded || action.ActionType != model.ActionTimeoutUser {
			continue
		}
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
				parts = append(parts, fmt.Sprintf("You can chat again <t:%d:R> — <t:%d:f>.", until.Unix(), until.Unix()))
				break
			}
		}
	}
	if snapshot != nil && snapshot.Template.Appealable {
		parts = append(parts, "You can ask staff to review this decision from your Quack dashboard.")
	}
	if settings != nil && strings.TrimSpace(settings.NotificationFooter) != "" {
		parts = append(parts, notificationPlain(strings.TrimSpace(settings.NotificationFooter)))
	}
	meta := fmt.Sprintf("Case #%d", item.CaseNumber)
	if !item.CreatedAt.IsZero() {
		meta += fmt.Sprintf(" · <t:%d:R>", item.CreatedAt.Unix())
	}
	return discordtext.Conversation(icon, lead+".", notificationPlain(item.Reason), strings.Join(parts, "\n\n"), meta)
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

// notificationPlain keeps guild and member text literal inside product-owned Markdown.
// This formatter remains Discord-type-free so the core does not import the bot adapter.
func notificationPlain(value string) string {
	return discordtext.Plain(value)
}
