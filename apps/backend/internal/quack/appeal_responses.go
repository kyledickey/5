package quack

import (
	"context"
	"encoding/json"
	"fmt"

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
	if caseID != "" {
		caseRecord, err := s.store.GetCaseByID(ctx, caseID)
		if err != nil {
			return nil, err
		}
		if caseRecord != nil {
			response.CaseNumber = caseRecord.CaseNumber
			response.TemplateName = memberTemplateName(*caseRecord)
		}
	}
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
