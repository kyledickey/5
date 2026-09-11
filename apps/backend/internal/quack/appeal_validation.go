package quack

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// validateQuestions normalizes a form definition: 1-10 questions sorted by
// Position with unique non-empty ids, contiguous positions from 0, prompts of
// at most 300 characters, and a supported type.
func validateQuestions(questions []model.AppealQuestion) ([]model.AppealQuestion, error) {
	if len(questions) == 0 || len(questions) > 10 {
		return nil, appealValidation("appeal form must contain between 1 and 10 questions")
	}
	normalized := append([]model.AppealQuestion(nil), questions...)
	sort.SliceStable(normalized, func(i, j int) bool { return normalized[i].Position < normalized[j].Position })
	seen := map[string]bool{}
	for index := range normalized {
		question := &normalized[index]
		question.ID = strings.TrimSpace(question.ID)
		question.Prompt = strings.TrimSpace(question.Prompt)
		if question.ID == "" || len(question.ID) > 64 || question.Prompt == "" || len([]rune(question.Prompt)) > 300 ||
			seen[question.ID] || question.Position != index {
			return nil, appealValidation("appeal questions require unique ids and contiguous ordering")
		}
		seen[question.ID] = true
		switch question.Type {
		case model.AppealQuestionShortText, model.AppealQuestionLongText, model.AppealQuestionBoolean:
		default:
			return nil, appealValidation("appeal question type is unsupported")
		}
	}
	return normalized, nil
}

// validateAnswers matches answers to questions by id, rejecting duplicates,
// unknown ids, missing required answers, and values of the wrong type. Text
// answers are trimmed and limited to 4000 characters. The result is ordered
// like questions and omits unanswered optional questions.
func validateAnswers(questions []model.AppealQuestion, answers []model.AppealAnswer) ([]model.AppealAnswer, error) {
	byID := map[string]model.AppealAnswer{}
	for _, answer := range answers {
		answer.QuestionID = strings.TrimSpace(answer.QuestionID)
		if answer.QuestionID == "" || byID[answer.QuestionID].QuestionID != "" {
			return nil, appealValidation("answers must have unique question ids")
		}
		byID[answer.QuestionID] = answer
	}
	normalized := make([]model.AppealAnswer, 0, len(questions))
	for _, question := range questions {
		answer, present := byID[question.ID]
		if !present {
			if question.Required {
				return nil, appealValidation("required appeal answer is missing")
			}
			continue
		}
		switch question.Type {
		case model.AppealQuestionBoolean:
			if _, ok := answer.Value.(bool); !ok {
				return nil, appealValidation("boolean appeal answer is invalid")
			}
		default:
			value, ok := answer.Value.(string)
			if !ok || len([]rune(strings.TrimSpace(value))) > 4000 || (question.Required && strings.TrimSpace(value) == "") {
				return nil, appealValidation("text appeal answer is invalid")
			}
			answer.Value = strings.TrimSpace(value)
		}
		normalized = append(normalized, answer)
		delete(byID, question.ID)
	}
	if len(byID) != 0 {
		return nil, appealValidation("answer references an unknown question")
	}
	return normalized, nil
}

// decodeQuestions parses a stored question snapshot and re-validates it so a
// corrupt snapshot fails loudly instead of rendering a partial form.
func decodeQuestions(body string) ([]model.AppealQuestion, error) {
	var questions []model.AppealQuestion
	if err := json.Unmarshal([]byte(body), &questions); err != nil {
		return nil, fmt.Errorf("decode appeal questions: %w", err)
	}
	return validateQuestions(questions)
}

// caseSnapshotAppealable reads the appealable flag frozen in the case's
// template snapshot; the live template's current setting is irrelevant.
func caseSnapshotAppealable(body string) bool {
	var snapshot struct {
		Template struct {
			Appealable bool `json:"appealable"`
		} `json:"template"`
	}
	return json.Unmarshal([]byte(body), &snapshot) == nil && snapshot.Template.Appealable
}

func validAppealState(status model.AppealStatus) bool {
	switch status {
	case model.AppealStatusPending, model.AppealStatusNeedsInformation, model.AppealStatusAccepted,
		model.AppealStatusRejected, model.AppealStatusClosed:
		return true
	default:
		return false
	}
}

// requireAppealReview is the shared staff gate: a resolved guild, a staff row
// for attribution, and the appeal.review capability.
func requireAppealReview(guildContext *GuildStaffContext) error {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(model.PermissionActionAppealReview) {
		return ErrAppealPermissionDenied
	}
	return nil
}
