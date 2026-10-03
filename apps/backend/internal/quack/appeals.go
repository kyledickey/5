package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
)

var (
	// ErrAppealNotFound prevents unrelated members from distinguishing another member's appeal.
	ErrAppealNotFound = errors.New("appeal not found")
	// ErrAppealValidation reports a malformed form, answer, transition, or reason.
	ErrAppealValidation = errors.New("appeal validation failed")
	// ErrAppealPermissionDenied reports missing live staff review authority.
	ErrAppealPermissionDenied = errors.New("appeal permission denied")
	// ErrAppealConflict reports a duplicate submission or stale timeline transition.
	ErrAppealConflict = errors.New("appeal state conflict")
)

// AppealRepository is the persistence AppealService and the notification
// dispatcher need: appeal rows and events, the durable outbox, and the case
// and guild reads used for eligibility and projection.
type AppealRepository interface {
	GetGuildByDiscordID(context.Context, string) (*model.Guild, error)
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	CreateAppeal(context.Context, model.CreateAppealParams) (*model.Appeal, error)
	GetAppealByID(context.Context, string) (*model.Appeal, error)
	GetAppealByCaseID(context.Context, string) (*model.Appeal, error)
	ListAppeals(context.Context, model.AppealListParams) (*model.AppealListResult, error)
	ListAppealEvents(context.Context, string) ([]model.AppealEvent, error)
	TransitionAppeal(context.Context, model.TransitionAppealParams) (*model.Appeal, error)
	ClaimPendingAppealNotifications(context.Context, int) ([]model.AppealNotification, error)
	BeginAppealNotificationDelivery(context.Context, string, string) error
	CompleteAppealNotification(context.Context, model.CompleteAppealNotificationParams) error
	GetCaseByID(context.Context, string) (*model.Case, error)
	ListCaseActionExecutions(context.Context, string) ([]model.CaseActionExecution, error)
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
}

// AppealService owns case eligibility, form snapshots, ownership, state transitions, and privacy projections.
type AppealService struct {
	store AppealRepository
}

func NewAppealService(store AppealRepository) *AppealService {
	return &AppealService{store: store}
}

// Submit creates the only appeal for an eligible case owned by the authenticated identity.
func (s *AppealService) Submit(
	ctx context.Context, caseID, memberDiscordUserID string, input AppealSubmissionInput,
) (*AppealResponse, error) {
	item, err := s.eligibleCase(ctx, caseID, memberDiscordUserID)
	if err != nil {
		return nil, err
	}
	memberDiscordUserID = strings.TrimSpace(memberDiscordUserID)
	settings, err := s.GetSettings(ctx, item.GuildID)
	if err != nil {
		return nil, err
	}
	answers, err := validateAnswers(settings.Questions, input.Answers)
	if err != nil {
		return nil, err
	}
	questionJSON, _ := json.Marshal(settings.Questions)
	answersJSON, _ := json.Marshal(answers)
	caseIDCopy := item.ID
	appeal := model.Appeal{
		GuildID:              item.GuildID,
		CaseID:               &caseIDCopy,
		TargetDiscordUserID:  memberDiscordUserID,
		Status:               model.AppealStatusPending,
		QuestionSnapshotJSON: string(questionJSON),
		AnswersJSON:          string(answersJSON),
		Version:              1,
		MetadataJSON:         "{}",
	}
	staffBody := discordtext.Conversation(
		"appeal",
		fmt.Sprintf("<@%s> asked staff to review case #%d.", memberDiscordUserID, item.CaseNumber),
		"",
		"Review the statement in the appeal queue.",
		"",
	)
	created, err := s.store.CreateAppeal(ctx, model.CreateAppealParams{
		Appeal: appeal,
		Event: model.AppealEvent{
			EventType:          string(model.AppealEventSubmitted),
			ActorDiscordUserID: memberDiscordUserID,
			ActorType:          "member",
			Body:               "Appeal submitted",
			MetadataJSON:       "{}",
		},
		CaseEvent: model.CaseEvent{
			EventType:          model.CaseEventAppealCreated,
			ActorDiscordUserID: memberDiscordUserID,
			ActorType:          "member",
			Visibility:         model.EventVisibilityPublic,
			Body:               "Appeal submitted",
			MetadataJSON:       "{}",
		},
		Audit: appealAudit(ctx, item.GuildID, memberDiscordUserID, 0, "appeal.submit", "appeal", "", model.AuditResultSuccess),
		Notification: model.AppealNotification{
			TargetDiscordUserID: memberDiscordUserID,
			Audience:            model.AppealNotificationStaff,
			Status:              model.AppealNotificationPending,
			Body:                staffBody,
		},
	})
	if errors.Is(err, model.ErrAppealAlreadyExists) {
		return nil, ErrAppealConflict
	}
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "Appeal submitted", "guild_id", created.GuildID, "case_id", created.CaseID, "appeal_id", created.ID)
	return s.response(ctx, created, true)
}

// CanSubmit checks a case-owned form opening without creating an appeal. Submit
// repeats these checks so stale forms cannot bypass ownership or eligibility.
func (s *AppealService) CanSubmit(ctx context.Context, caseID, memberDiscordUserID string) error {
	_, err := s.eligibleCase(ctx, caseID, memberDiscordUserID)
	return err
}

// ReviewReasonRequired reports whether the guild requires a moderator-entered
// decision reason. The review queue reads it before choosing a one-click or form
// response because Discord only opens a form as the initial interaction response.
func (s *AppealService) ReviewReasonRequired(ctx context.Context, discordGuildID string) (bool, error) {
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil {
		return false, err
	}
	if guild == nil {
		return false, nil
	}
	settings, err := s.store.GetGuildSettings(ctx, guild.ID)
	if err != nil {
		return false, err
	}
	return settings != nil && settings.AppealReviewReasonRequired, nil
}

// eligibleCase keeps form openings and submissions on the same ownership boundary.
func (s *AppealService) eligibleCase(ctx context.Context, caseID, memberDiscordUserID string) (*model.Case, error) {
	caseID = strings.TrimSpace(caseID)
	memberDiscordUserID = strings.TrimSpace(memberDiscordUserID)
	if caseID == "" || memberDiscordUserID == "" {
		return nil, appealValidation("case and member identity are required")
	}
	item, err := s.store.GetCaseByID(ctx, caseID)
	if err != nil {
		return nil, err
	}
	if item == nil || item.TargetDiscordUserID != memberDiscordUserID {
		if item != nil {
			_ = s.auditMember(ctx, item.GuildID, memberDiscordUserID, "appeal.submit", item.ID, model.AuditResultDenied) // best-effort: not-found already returned
		}
		return nil, ErrAppealNotFound
	}
	if item.Validity != model.CaseValidityValid || !caseSnapshotAppealable(item.TemplateSnapshotJSON) {
		_ = s.auditMember(ctx, item.GuildID, memberDiscordUserID, "appeal.submit", item.ID, model.AuditResultDenied) // best-effort: ineligibility already returned
		return nil, model.ErrAppealCaseIneligible
	}
	if existing, getErr := s.store.GetAppealByCaseID(ctx, item.ID); getErr != nil {
		return nil, getErr
	} else if existing != nil {
		return nil, ErrAppealConflict
	}
	return item, nil
}

// GetMember returns an appeal only to its target identity and redacts every staff actor.
func (s *AppealService) GetMember(ctx context.Context, appealID, memberDiscordUserID string) (*AppealResponse, error) {
	item, err := s.store.GetAppealByID(ctx, strings.TrimSpace(appealID))
	if err != nil {
		return nil, err
	}
	if item == nil || item.TargetDiscordUserID != strings.TrimSpace(memberDiscordUserID) {
		if item != nil {
			_ = s.auditMember(ctx, item.GuildID, memberDiscordUserID, "appeal.read", item.ID, model.AuditResultDenied) // best-effort: not-found already returned
		}
		return nil, ErrAppealNotFound
	}
	if err := s.auditMember(ctx, item.GuildID, memberDiscordUserID, "appeal.read", item.ID, model.AuditResultSuccess); err != nil {
		return nil, err
	}
	return s.response(ctx, item, true)
}

// GetStaff returns the exact staff projection after live Moderate Members authorization.
func (s *AppealService) GetStaff(ctx context.Context, guildContext *GuildStaffContext, appealID string) (*AppealResponse, error) {
	if err := requireAppealReview(guildContext); err != nil {
		return nil, err
	}
	item, err := s.store.GetAppealByID(ctx, strings.TrimSpace(appealID))
	if err != nil {
		return nil, err
	}
	if item == nil || item.GuildID != guildContext.Guild.ID {
		return nil, ErrAppealNotFound
	}
	audit := appealAudit(
		ctx, item.GuildID, guildContext.Staff.DiscordUserID, guildContext.PermissionBits,
		"appeal.read", "appeal", item.ID, model.AuditResultSuccess,
	)
	if err := recordAudit(ctx, s.store, &audit); err != nil {
		return nil, err
	}
	return s.response(ctx, item, false)
}

// ListStaff returns the authorized guild queue with stable pagination and optional state filter.
func (s *AppealService) ListStaff(
	ctx context.Context, guildContext *GuildStaffContext, status model.AppealStatus, limit, offset int,
) (*AppealListResponse, error) {
	if err := requireAppealReview(guildContext); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || offset < 0 || (status != "" && !validAppealState(status)) {
		return nil, appealValidation("invalid appeal queue filter")
	}
	result, err := s.store.ListAppeals(ctx, model.AppealListParams{
		GuildID: guildContext.Guild.ID,
		Status:  status,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return nil, err
	}
	responses := make([]AppealResponse, 0, len(result.Appeals))
	for index := range result.Appeals {
		response, responseErr := s.response(ctx, &result.Appeals[index], false)
		if responseErr != nil {
			return nil, responseErr
		}
		responses = append(responses, *response)
	}
	audit := appealAudit(
		ctx, guildContext.Guild.ID, guildContext.Staff.DiscordUserID, guildContext.PermissionBits,
		"appeal.queue.read", "appeal", "list", model.AuditResultSuccess,
	)
	if err := recordAudit(ctx, s.store, &audit); err != nil {
		return nil, err
	}
	return &AppealListResponse{Appeals: responses, Total: result.Total, Limit: limit, Offset: offset}, nil
}

// Accept atomically records the decision, voids the case, and queues punishment removal.
func (s *AppealService) Accept(
	ctx context.Context, guildContext *GuildStaffContext, appealID, reason string,
) (*AppealResponse, error) {
	return s.transition(
		ctx, guildContext, appealID, reason,
		[]model.AppealStatus{model.AppealStatusPending}, model.AppealStatusAccepted, model.AppealEventAccepted, true,
	)
}

// Reject records a terminal decision without changing case validity.
func (s *AppealService) Reject(
	ctx context.Context, guildContext *GuildStaffContext, appealID, reason string,
) (*AppealResponse, error) {
	return s.transition(
		ctx, guildContext, appealID, reason,
		[]model.AppealStatus{model.AppealStatusPending}, model.AppealStatusRejected, model.AppealEventRejected, false,
	)
}

// Close is a compatibility alias for rejecting an undecided appeal.
func (s *AppealService) Close(
	ctx context.Context, guildContext *GuildStaffContext, appealID, reason string,
) (*AppealResponse, error) {
	return s.Reject(ctx, guildContext, appealID, reason)
}

// transition freezes member context with the durable decision and commits through
// the store transaction so competing reviewers cannot both decide an appeal.
func (s *AppealService) transition(
	ctx context.Context,
	guildContext *GuildStaffContext,
	appealID, reason string,
	from []model.AppealStatus,
	to model.AppealStatus,
	eventType model.AppealEventType,
	voidCase bool,
) (*AppealResponse, error) {
	if err := requireAppealReview(guildContext); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 2000 {
		return nil, appealValidation("reason must be between 1 and 2000 characters")
	}
	item, err := s.store.GetAppealByID(ctx, strings.TrimSpace(appealID))
	if err != nil {
		return nil, err
	}
	if item == nil || item.GuildID != guildContext.Guild.ID {
		return nil, ErrAppealNotFound
	}
	intent := model.AppealDecisionIntent{Version: 1, Status: to, Reason: reason, GuildName: guildContext.Guild.Name}
	if item.CaseID != nil {
		caseItem, err := s.store.GetCaseByID(ctx, *item.CaseID)
		if err != nil {
			return nil, err
		}
		if caseItem == nil || caseItem.GuildID != item.GuildID {
			return nil, ErrAppealNotFound
		}
		intent.CaseNumber, intent.CaseID = caseItem.CaseNumber, caseItem.ID
	}
	if to == model.AppealStatusAccepted {
		settings, err := s.store.GetGuildSettings(ctx, item.GuildID)
		if err != nil {
			return nil, err
		}
		if settings != nil && settings.AppealRejoinURL != "" {
			intent.RejoinURL = settings.AppealRejoinURL
		}
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	actorID := guildContext.Staff.DiscordUserID
	params := model.TransitionAppealParams{
		GuildID:            item.GuildID,
		AppealID:           item.ID,
		ActorDiscordUserID: actorID,
		AllowedFrom:        from,
		To:                 to,
		Reason:             reason,
		VoidCase:           voidCase,
		Event: model.AppealEvent{
			EventType:          string(eventType),
			ActorDiscordUserID: actorID,
			ActorType:          "staff",
			Body:               reason,
			MetadataJSON:       "{}",
		},
		AppealAudit: appealAudit(
			ctx, item.GuildID, actorID, guildContext.PermissionBits,
			"appeal."+string(eventType), "appeal", item.ID, model.AuditResultSuccess,
		),
		Notification: model.AppealNotification{
			TargetDiscordUserID: item.TargetDiscordUserID,
			Audience:            model.AppealNotificationMember,
			Status:              model.AppealNotificationPending,
			DecisionIntentJSON:  string(payload),
		},
	}
	if voidCase {
		caseAudit := appealAudit(
			ctx, item.GuildID, actorID, guildContext.PermissionBits, "case.void.appeal", "case", "", model.AuditResultSuccess,
		)
		params.CaseAudit = &caseAudit
	}
	updated, err := s.store.TransitionAppeal(ctx, params)
	if errors.Is(err, model.ErrAppealStateConflict) || errors.Is(err, model.ErrAppealCaseIneligible) {
		return nil, ErrAppealConflict
	}
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "Appeal decision recorded", "guild_id", updated.GuildID, "appeal_id", updated.ID, "status", updated.Status)
	return s.response(ctx, updated, false)
}

func appealValidation(message string) error {
	return fmt.Errorf("%w: %s", ErrAppealValidation, message)
}

// appealAudit builds an audit row for an appeal-related action with the
// request's trace ids and adapter source. permissionBits is 0 for members.
func appealAudit(
	ctx context.Context,
	guildID, actorID string,
	permissionBits uint64,
	action, resourceType, resourceID string,
	result model.AuditResult,
) model.AuditLogEntry {
	requestID, correlationID := idutil.TraceIDsFromContext(ctx)
	return model.AuditLogEntry{
		GuildID:             guildID,
		ActorDiscordUserID:  actorID,
		ActorPermissionBits: permissionBits,
		Source:              AuditSourceFromContext(ctx),
		Action:              action,
		ResourceType:        resourceType,
		ResourceID:          resourceID,
		Result:              result,
		RequestID:           requestID,
		CorrelationID:       correlationID,
		MetadataJSON:        "{}",
	}
}

func (s *AppealService) auditMember(ctx context.Context, guildID, actorID, action, resourceID string, result model.AuditResult) error {
	entry := appealAudit(ctx, guildID, actorID, 0, action, "appeal", resourceID, result)
	return recordAudit(ctx, s.store, &entry)
}

// AppealSettingsResponse is the form new appeals in a guild will snapshot.
// Default reports that it is Quack's built-in form rather than a guild override.
type AppealSettingsResponse struct {
	GuildID   string                 `json:"guild_id"`
	Questions []model.AppealQuestion `json:"questions"`
	Default   bool                   `json:"default"`
}

type AppealSubmissionInput struct {
	Answers []model.AppealAnswer `json:"answers"`
}

type AppealDecisionInput struct {
	Reason string `json:"reason"`
}

type AppealEventResponse struct {
	ID                 string                `json:"id"`
	Type               model.AppealEventType `json:"type"`
	ActorType          string                `json:"actor_type"`
	ActorDiscordUserID string                `json:"actor_discord_user_id,omitempty"`
	Body               string                `json:"body"`
	CreatedAt          time.Time             `json:"created_at"`
}

// AppealReversalOffer describes a separately confirmed reversal without executing it.
type AppealReversalOffer struct {
	OriginalExecutionID string           `json:"original_execution_id"`
	ActionType          model.ActionType `json:"action_type"`
}

type AppealResponse struct {
	CaseNumber              uint64                 `json:"case_number"`
	TemplateName            string                 `json:"template_name"`
	ID                      string                 `json:"id"`
	GuildID                 string                 `json:"guild_id"`
	CaseID                  string                 `json:"case_id"`
	TargetDiscordUserID     string                 `json:"target_discord_user_id"`
	Status                  model.AppealStatus     `json:"status"`
	Questions               []model.AppealQuestion `json:"questions"`
	Answers                 []model.AppealAnswer   `json:"answers"`
	DecisionReason          string                 `json:"decision_reason,omitempty"`
	ReviewedByDiscordUserID string                 `json:"reviewed_by_discord_user_id,omitempty"`
	Events                  []AppealEventResponse  `json:"events"`
	ReversalOffers          []AppealReversalOffer  `json:"reversal_offers,omitempty"`
	CreatedAt               time.Time              `json:"created_at"`
	UpdatedAt               time.Time              `json:"updated_at"`
}

type AppealListResponse struct {
	Appeals []AppealResponse `json:"appeals"`
	Total   int64            `json:"total"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}

// response builds the AppealResponse projection for item. When member is true
// every staff identity is redacted. Staff projections of an accepted appeal
// also list reversal offers: succeeded timeouts and bans on the linked case
// that have no reversal queued yet.
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
		responseEvents = append(responseEvents, AppealEventResponse{
			ID:                 event.ID,
			Type:               model.AppealEventType(event.EventType),
			ActorType:          event.ActorType,
			ActorDiscordUserID: actorID,
			Body:               event.Body,
			CreatedAt:          event.CreatedAt,
		})
	}
	reviewedBy := item.ReviewedByDiscordUserID
	if member {
		reviewedBy = ""
	}
	caseID := ""
	if item.CaseID != nil {
		caseID = *item.CaseID
	}
	response := &AppealResponse{
		ID:                      item.ID,
		GuildID:                 item.GuildID,
		CaseID:                  caseID,
		TargetDiscordUserID:     item.TargetDiscordUserID,
		Status:                  item.Status,
		Questions:               questions,
		Answers:                 answers,
		DecisionReason:          item.DecisionReason,
		ReviewedByDiscordUserID: reviewedBy,
		Events:                  responseEvents,
		CreatedAt:               item.CreatedAt,
		UpdatedAt:               item.UpdatedAt,
	}
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
				response.ReversalOffers = append(response.ReversalOffers, AppealReversalOffer{
					OriginalExecutionID: action.ID,
					ActionType:          model.ActionRemoveTimeout,
				})
			case model.ActionBanUser:
				response.ReversalOffers = append(response.ReversalOffers, AppealReversalOffer{
					OriginalExecutionID: action.ID,
					ActionType:          model.ActionUnbanUser,
				})
			}
		}
	}
	return response, nil
}

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

// GetSettings returns the one statement every Discord and web submission
// collects. Historical configurable forms are retained on old appeals but no
// longer determine new submissions; each appeal snapshots this form so it stays
// readable after the built-in wording changes.
func (s *AppealService) GetSettings(ctx context.Context, guildID string) (*AppealSettingsResponse, error) {
	if strings.TrimSpace(guildID) == "" {
		return nil, appealValidation("guild is required")
	}
	return &AppealSettingsResponse{
		GuildID: strings.TrimSpace(guildID),
		Questions: []model.AppealQuestion{{
			ID:       "reason",
			Prompt:   "What would you like the moderators to reconsider?",
			Type:     model.AppealQuestionLongText,
			Required: true,
		}},
		Default: true,
	}, nil
}

var discordInviteCode = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// normalizeAppealRejoinURL accepts only Discord invite links, returning a canonical
// URL safe to include in member notifications. Empty input removes the link.
func normalizeAppealRejoinURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || len(value) > 256 || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: use an HTTPS Discord invite link", ErrGuildSettingsValidation)
	}
	code := ""
	switch strings.ToLower(parsed.Host) {
	case "discord.gg":
		code = strings.TrimPrefix(parsed.Path, "/")
	case "discord.com":
		code = strings.TrimPrefix(parsed.Path, "/invite/")
		if code == parsed.Path {
			code = ""
		}
	}
	if !discordInviteCode.MatchString(code) {
		return "", fmt.Errorf("%w: use an HTTPS Discord invite link", ErrGuildSettingsValidation)
	}
	return "https://discord.gg/" + code, nil
}
