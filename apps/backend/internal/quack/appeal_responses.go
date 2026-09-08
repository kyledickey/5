package quack

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack/model"
)

func (s *AppealService) response(ctx context.Context, item *model.Appeal, member bool) (*AppealResponse, error) {
	questions, err := decodeQuestions(item.QuestionSnapshotJSON)
	if err != nil {
		return nil, err
	}
	var answers []model.AppealAnswer
	if err := json.Unmarshal([]byte(item.AnswersJSON), &answers); err != nil {
		return nil, fmt.Errorf("decode appeal answers: %w", err)
	}
	events, err := s.store.ListAppealEvents(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	responseEvents := make([]AppealEventResponse, 0, len(events))
	for _, event := range events {
		actorID := event.ActorDiscordUserID
		if member && event.ActorType == "staff" {
			actorID = ""
		}
		responseEvents = append(responseEvents, AppealEventResponse{ID: event.ID, Type: model.AppealEventType(event.EventType), ActorType: event.ActorType, ActorDiscordUserID: actorID, Body: event.Body, CreatedAt: event.CreatedAt})
	}
	reviewedBy := item.ReviewedByDiscordUserID
	if member {
		reviewedBy = ""
	}
	caseID := ""
	if item.CaseID != nil {
		caseID = *item.CaseID
	}
	response := &AppealResponse{ID: item.ID, GuildID: item.GuildID, CaseID: caseID, TargetDiscordUserID: item.TargetDiscordUserID, Status: item.Status, Questions: questions, Answers: answers, DecisionReason: item.DecisionReason, ReviewedByDiscordUserID: reviewedBy, Events: responseEvents, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if !member && item.Status == model.AppealStatusAccepted && caseID != "" {
		actions, actionErr := s.store.ListCaseActionExecutions(ctx, caseID)
		if actionErr != nil {
			return nil, actionErr
		}
		queued := make(map[string]bool)
		for _, action := range actions {
			if action.ReversalOfExecutionID != nil {
				queued[*action.ReversalOfExecutionID] = true
			}
		}
		for _, action := range actions {
			if action.Status != model.ActionExecutionSucceeded || action.ReversalOfExecutionID != nil || queued[action.ID] {
				continue
			}
			switch action.ActionType {
			case model.ActionTimeoutUser:
				response.ReversalOffers = append(response.ReversalOffers, AppealReversalOffer{OriginalExecutionID: action.ID, ActionType: model.ActionRemoveTimeout})
			case model.ActionBanUser:
				response.ReversalOffers = append(response.ReversalOffers, AppealReversalOffer{OriginalExecutionID: action.ID, ActionType: model.ActionUnbanUser})
			}
		}
	}
	return response, nil
}

// memberNotificationBody keeps review decisions distinct from enforcement and
// quotes staff-authored context without disclosing the reviewing staff member.
func memberNotificationBody(status model.AppealStatus, reason string) string {
	icon, lead, next := "appeal", "Your appeal was closed.", ""
	switch status {
	case model.AppealStatusNeedsInformation:
		icon, lead, next = "reply", "Staff need a little more information to review your appeal.", "You can reply from your Quack dashboard."
	case model.AppealStatusAccepted:
		icon, lead, next = "accept", "Your appeal was accepted.", "Your case was voided. Quack will try to remove any ban or timeout from it."
	case model.AppealStatusRejected:
		icon, lead = "decline", "Your appeal was declined."
	}
	return discordtext.Conversation(icon, lead, discordtext.Plain(reason), next, "")
}
