package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/quackdiscord/bot/internal/discordtext"
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

// AppealRepository is the package-owned persistence boundary exposed by the store adapter.
type AppealRepository interface {
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

// NewAppealService constructs the package service without central runtime ownership.
func NewAppealService(store AppealRepository) *AppealService {
	return &AppealService{store: store}
}

// Submit creates the only appeal for an eligible case owned by the authenticated identity.
func (s *AppealService) Submit(ctx context.Context, caseID, memberDiscordUserID string, input AppealSubmissionInput) (*AppealResponse, error) {
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
	appeal := model.Appeal{GuildID: item.GuildID, CaseID: &caseIDCopy, TargetDiscordUserID: memberDiscordUserID, Status: model.AppealStatusPending, QuestionSnapshotJSON: string(questionJSON), AnswersJSON: string(answersJSON), Version: 1, MetadataJSON: "{}"}
	created, err := s.store.CreateAppeal(ctx, model.CreateAppealParams{
		Appeal:       appeal,
		Event:        model.AppealEvent{EventType: string(model.AppealEventSubmitted), ActorDiscordUserID: memberDiscordUserID, ActorType: "member", Body: "Appeal submitted", MetadataJSON: "{}"},
		CaseEvent:    model.CaseEvent{EventType: model.CaseEventAppealCreated, ActorDiscordUserID: memberDiscordUserID, ActorType: "member", Visibility: model.EventVisibilityPublic, Body: "Appeal submitted", MetadataJSON: "{}"},
		Audit:        appealAudit(ctx, item.GuildID, memberDiscordUserID, 0, "appeal.submit", "appeal", "", model.AuditResultSuccess),
		Notification: model.AppealNotification{TargetDiscordUserID: memberDiscordUserID, Audience: model.AppealNotificationStaff, Status: model.AppealNotificationPending, Body: discordtext.Conversation("appeal", fmt.Sprintf("<@%s> asked staff to review case #%d.", memberDiscordUserID, item.CaseNumber), "", "Review the statement in the appeal queue.", "")},
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

// eligibleCase keeps form openings and submissions on the same ownership boundary.
func (s *AppealService) eligibleCase(ctx context.Context, caseID, memberDiscordUserID string) (*model.Case, error) {
	if s == nil || s.store == nil {
		return nil, ErrAppealNotFound
	}
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
			_ = s.auditMember(ctx, item.GuildID, memberDiscordUserID, "appeal.submit", item.ID, model.AuditResultDenied)
		}
		return nil, ErrAppealNotFound
	}
	if item.Validity != model.CaseValidityValid || !caseSnapshotAppealable(item.TemplateSnapshotJSON) {
		_ = s.auditMember(ctx, item.GuildID, memberDiscordUserID, "appeal.submit", item.ID, model.AuditResultDenied)
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
			_ = s.auditMember(ctx, item.GuildID, memberDiscordUserID, "appeal.read", item.ID, model.AuditResultDenied)
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
	if err := recordAudit(ctx, s.store, pointerAudit(appealAudit(ctx, item.GuildID, guildContext.Staff.DiscordUserID, guildContext.PermissionBits, "appeal.read", "appeal", item.ID, model.AuditResultSuccess))); err != nil {
		return nil, err
	}
	return s.response(ctx, item, false)
}

// ListStaff returns the authorized guild queue with stable pagination and optional state filter.
func (s *AppealService) ListStaff(ctx context.Context, guildContext *GuildStaffContext, status model.AppealStatus, limit, offset int) (*AppealListResponse, error) {
	if err := requireAppealReview(guildContext); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || offset < 0 || (status != "" && !validAppealState(status)) {
		return nil, appealValidation("invalid appeal queue filter")
	}
	result, err := s.store.ListAppeals(ctx, model.AppealListParams{GuildID: guildContext.Guild.ID, Status: status, Limit: limit, Offset: offset})
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
	if err := recordAudit(ctx, s.store, pointerAudit(appealAudit(ctx, guildContext.Guild.ID, guildContext.Staff.DiscordUserID, guildContext.PermissionBits, "appeal.queue.read", "appeal", "list", model.AuditResultSuccess))); err != nil {
		return nil, err
	}
	return &AppealListResponse{Appeals: responses, Total: result.Total, Limit: limit, Offset: offset}, nil
}

// Accept atomically records the decision, voids the case, and queues punishment removal.
func (s *AppealService) Accept(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, []model.AppealStatus{model.AppealStatusPending}, model.AppealStatusAccepted, model.AppealEventAccepted, true)
}

// Reject records a terminal decision without changing case validity.
func (s *AppealService) Reject(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, []model.AppealStatus{model.AppealStatusPending}, model.AppealStatusRejected, model.AppealEventRejected, false)
}

// Close is a compatibility alias for rejecting an undecided appeal.
func (s *AppealService) Close(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.Reject(ctx, guildContext, appealID, reason)
}

func (s *AppealService) transition(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string, from []model.AppealStatus, to model.AppealStatus, eventType model.AppealEventType, voidCase bool) (*AppealResponse, error) {
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
	body := memberNotificationBody(to, reason)
	if to == model.AppealStatusAccepted {
		settings, err := s.store.GetGuildSettings(ctx, item.GuildID)
		if err != nil {
			return nil, err
		}
		if settings != nil && settings.AppealRejoinURL != "" {
			body += "\n\nIf you left or were banned, you can rejoin once any ban has been removed: " + settings.AppealRejoinURL
		}
	}
	params := model.TransitionAppealParams{
		GuildID: item.GuildID, AppealID: item.ID, ActorDiscordUserID: guildContext.Staff.DiscordUserID,
		AllowedFrom: from, To: to, Reason: reason, VoidCase: voidCase,
		Event:        model.AppealEvent{EventType: string(eventType), ActorDiscordUserID: guildContext.Staff.DiscordUserID, ActorType: "staff", Body: reason, MetadataJSON: "{}"},
		AppealAudit:  appealAudit(ctx, item.GuildID, guildContext.Staff.DiscordUserID, guildContext.PermissionBits, "appeal."+string(eventType), "appeal", item.ID, model.AuditResultSuccess),
		Notification: model.AppealNotification{TargetDiscordUserID: item.TargetDiscordUserID, Audience: model.AppealNotificationMember, Status: model.AppealNotificationPending, Body: body},
	}
	if voidCase {
		caseAudit := appealAudit(ctx, item.GuildID, guildContext.Staff.DiscordUserID, guildContext.PermissionBits, "case.void.appeal", "case", "", model.AuditResultSuccess)
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

func appealAudit(ctx context.Context, guildID, actorID string, permissionBits uint64, action, resourceType, resourceID string, result model.AuditResult) model.AuditLogEntry {
	requestID, correlationID := TraceIDsFromContext(ctx)
	return model.AuditLogEntry{GuildID: guildID, ActorDiscordUserID: actorID, ActorPermissionBits: permissionBits, Source: AuditSourceFromContext(ctx), Action: action, ResourceType: resourceType, ResourceID: resourceID, Result: result, RequestID: requestID, CorrelationID: correlationID, MetadataJSON: "{}"}
}

func pointerAudit(entry model.AuditLogEntry) *model.AuditLogEntry { return &entry }

func (s *AppealService) auditMember(ctx context.Context, guildID, actorID, action, resourceID string, result model.AuditResult) error {
	entry := appealAudit(ctx, guildID, actorID, 0, action, "appeal", resourceID, result)
	return recordAudit(ctx, s.store, &entry)
}
