package model

import (
	"errors"
	"fmt"
	"time"
)

// ErrTemplateConflict rejects an edit based on an outdated policy snapshot.
var ErrTemplateConflict = errors.New("template changed; reload it before saving")

// ErrTemplateCompatibilityReviewRequired marks a preserved legacy template that cannot be represented safely by the live v5 policy contract.
var ErrTemplateCompatibilityReviewRequired = errors.New("template compatibility review required")

// TemplateCompatibilityReviewError identifies a quarantined template and explains why its preserved policy cannot be returned as live v5 configuration.
type TemplateCompatibilityReviewError struct {
	TemplateID string
	Reason     string
}

// Error returns an administrator-facing compatibility review message without projecting invalid legacy policy.
func (e *TemplateCompatibilityReviewError) Error() string {
	if e == nil {
		return ErrTemplateCompatibilityReviewRequired.Error()
	}
	if e.Reason == "" {
		return fmt.Sprintf("%s for template %s", ErrTemplateCompatibilityReviewRequired, e.TemplateID)
	}
	return fmt.Sprintf("%s for template %s: %s", ErrTemplateCompatibilityReviewRequired, e.TemplateID, e.Reason)
}

// Unwrap preserves sentinel matching across storage, service, and transport boundaries.
func (e *TemplateCompatibilityReviewError) Unwrap() error {
	return ErrTemplateCompatibilityReviewRequired
}

// ExpandedCaseTemplate groups the expanded case template state used to keep this package's responsibilities explicit.
type ExpandedCaseTemplate struct {
	Template      CaseTemplate
	ContextFields []CaseTemplateContextField
	Levels        []ExpandedCaseTemplateLevel
}

// ExpandedCaseTemplateLevel groups the expanded case template level state used to keep this package's responsibilities explicit.
type ExpandedCaseTemplateLevel struct {
	Level   CaseTemplateLevel
	Actions []CaseTemplateLevelAction
}

// CreateCaseTemplateParams groups the validated inputs needed for create case template params.
type CreateCaseTemplateParams struct {
	Template      CaseTemplate
	ContextFields []CaseTemplateContextField
	Levels        []ExpandedCaseTemplateLevel
	Audit         *AuditLogEntry
}

// UpdateCaseTemplateParams groups the validated inputs needed for update case template params.
type UpdateCaseTemplateParams struct {
	GuildID, TemplateID string
	ExpectedVersion     uint
	Template            CaseTemplate
	ContextFields       []CaseTemplateContextField
	Levels              []ExpandedCaseTemplateLevel
	Audit               *AuditLogEntry
}

// ListAuditLogEntriesParams groups the validated inputs needed for list audit log entries params.
type ListAuditLogEntriesParams struct {
	GuildID, ActorDiscordUserID, Source, Action, ResourceType, ResourceID string
	CaseID, MemberDiscordUserID, CreatedAfter, CreatedBefore, BeforeID    string
	Result                                                                AuditResult
	Limit, Offset                                                         int
}

// ListAuditLogEntriesResult captures the outcome of list audit log entries result for the caller.
type ListAuditLogEntriesResult struct {
	Entries []AuditLogEntry
	Total   int64
}

// CreateCaseParams groups the validated inputs needed for create case params.
type CreateCaseParams struct {
	Case             Case
	Event            CaseEvent
	ActionExecutions []CaseActionExecution
	Evidence         []CaseEvidenceSnapshot
	Attachments      []CaseEvidenceAttachment
	Notification     *CaseNotification
	Audit            *AuditLogEntry
	AdditionalAudits []AuditLogEntry
}

// CreatedCase groups the created case state used to keep this package's responsibilities explicit.
type CreatedCase struct {
	Case             Case
	Event            CaseEvent
	ActionExecutions []CaseActionExecution
	Evidence         []CaseEvidenceSnapshot
	Attachments      []CaseEvidenceAttachment
	Notification     *CaseNotification
}

// CountTemplateCasesForTargetParams groups the validated inputs needed for count template cases for target params.
type CountTemplateCasesForTargetParams struct {
	// CreatedAtOrAfter is an inclusive rolling-window boundary; nil counts all history.
	CreatedAtOrAfter                         *time.Time
	GuildID, TemplateID, TargetDiscordUserID string
}

// ListCasesParams groups the validated inputs needed for list cases params.
type ListCasesParams struct {
	GuildID, TargetDiscordUserID, ModeratorDiscordUserID, TemplateID    string
	CaseNumber, ActionResult, AppealStatus, CreatedAfter, CreatedBefore string
	Validity                                                            CaseValidity
	Limit, Offset                                                       int
}

// ListCasesResult captures the outcome of list cases result for the caller.
type ListCasesResult struct {
	Cases []Case
	Total int64
}

// TargetCaseSummary groups the target case summary state used to keep this package's responsibilities explicit.
type TargetCaseSummary struct {
	Total      int64
	ByValidity map[CaseValidity]int64
	ByTemplate map[string]int64
}

// ClaimedCaseAction identifies the supported claimed case action values stored and exchanged by Quack.
type ClaimedCaseAction struct {
	Case      Case
	Execution CaseActionExecution
}

// ClaimCaseActionParams groups the validated inputs needed for claim case action params.
type ClaimCaseActionParams struct{ CaseID, WorkerID string }

// CompleteCaseActionParams groups the validated inputs needed for complete case action params.
type CompleteCaseActionParams struct {
	ExecutionID                                                      string
	LeaseToken                                                       string
	AttemptNumber                                                    uint8
	WorkerID                                                         string
	AttemptStatus                                                    ActionAttemptStatus
	ExecutionStatus                                                  ActionExecutionStatus
	ErrorCode, ErrorMessage, RequestPayloadJSON, ResponsePayloadJSON string
	NextRetryAt                                                      *time.Time
	EventType                                                        CaseEventType
	EventBody, EventMetadataJSON, CorrelationID, RequestID           string
}

// VoidCaseParams carries the immutable correction decision and audit evidence for a case.
type VoidCaseParams struct {
	GuildID, CaseID, ActorDiscordUserID, Reason string
	ReplacementCaseID                           *string
	Audit                                       *AuditLogEntry
}

// RetryCaseActionParams carries an authorized manual retry request.
type RetryCaseActionParams struct {
	GuildID, ExecutionID, ActorDiscordUserID string
	Audit                                    *AuditLogEntry
}

// DismissCaseActionParams carries an authorized failed-action dismissal.
type DismissCaseActionParams struct {
	GuildID, ExecutionID, ActorDiscordUserID string
	Audit                                    *AuditLogEntry
}

// QueueCaseReversalParams carries an explicit, staff-confirmed timeout-removal or unban operation.
type QueueCaseReversalParams struct {
	GuildID, CaseID, ActorDiscordUserID string
	OriginalExecutionID                 string
	AppealID                            *string
	ActionType                          ActionType
	Audit                               *AuditLogEntry
}

// FailedCaseActionFilter bounds staff review queries to one guild.
type FailedCaseActionFilter struct {
	GuildID       string
	Limit, Offset int
}

// FailedCaseActionResult contains stable failed-action review pagination.
type FailedCaseActionResult struct {
	Executions []CaseActionExecution
	Total      int64
}

// ClaimCaseNotificationParams carries the case and worker identity used for a fenced notification claim.
type ClaimCaseNotificationParams struct{ CaseID, WorkerID string }

// CompleteCaseNotificationParams carries a fenced notification delivery outcome.
type CompleteCaseNotificationParams struct {
	NotificationID, LeaseToken, WorkerID                                string
	Status                                                              NotificationStatus
	PreparedChannelDiscordID, RenderedMessage, DeliveryMessageDiscordID string
	ErrorCode, ErrorMessage                                             string
	EventType                                                           CaseEventType
}

// SkipCaseActionsParams groups the validated inputs needed for skip case actions params.
type SkipCaseActionsParams struct {
	CaseID                           string
	AfterPosition                    int
	Reason, CorrelationID, RequestID string
}

// UpsertGuildParams groups the validated inputs needed for upsert guild params.
type UpsertGuildParams struct{ DiscordGuildID, Name, IconURL, OwnerDiscordUserID string }

// BootstrapGuildParams carries authoritative Discord guild metadata used by the idempotent install transaction.
type BootstrapGuildParams struct {
	DiscordGuildID, Name, IconURL, OwnerDiscordUserID string
	KnownChannelDiscordIDs                            []string
}

// BootstrapGuildResult reports the durable guild, settings, and starter-policy state produced by an install or rejoin event.
type BootstrapGuildResult struct {
	Guild                  Guild
	Settings               GuildSettings
	StarterTemplate        ExpandedCaseTemplate
	GuildCreated           bool
	StarterTemplateCreated bool
}

// GuildModuleToggle addresses only an explicitly requested canonical flag. The
// enabling validator supplies the exact configuration it checked; storage rejects
// concurrent configuration changes instead of enabling an unvalidated destination.
type GuildModuleToggle struct {
	ModuleID           string
	Enabled            bool
	ExpectedConfigJSON string
}

// UpdateGuildSettingsParams contains validated core settings, explicit module toggles, and immutable audit evidence.
type UpdateGuildSettingsParams struct {
	ModuleToggles []GuildModuleToggle
	Settings      GuildSettings
	Audit         *AuditLogEntry
}

// UpsertStaffMemberParams groups the validated inputs needed for upsert staff member params.
type UpsertStaffMemberParams struct {
	GuildID, DiscordUserID string
	LastSeenPermissionBits uint64
	LastKnownDisplayName   string
	LastActiveAt           time.Time
}

// ActionStatusCount groups the action status count state used to keep this package's responsibilities explicit.
type ActionStatusCount struct {
	Status ActionExecutionStatus
	Count  int64
}

// OldestActionExecution groups the oldest action execution state used to keep this package's responsibilities explicit.
type OldestActionExecution struct {
	ID, CaseID  string
	CaseNumber  uint64
	ActionType  ActionType
	Status      ActionExecutionStatus
	CreatedAt   time.Time
	NextRetryAt *time.Time
}

// RecentActionFailure groups the recent action failure state used to keep this package's responsibilities explicit.
type RecentActionFailure struct {
	ID, CaseID               string
	CaseNumber               uint64
	ActionType               ActionType
	Status                   ActionExecutionStatus
	LastErrorCode, LastError string
	UpdatedAt                time.Time
}

// ActionQueueSnapshot groups the action queue snapshot state used to keep this package's responsibilities explicit.
type ActionQueueSnapshot struct {
	StatusCounts         []ActionStatusCount
	OldestPendingOrRetry *OldestActionExecution
	RecentFailures       []RecentActionFailure
}

// GuildModuleConfigurationError reports an actionable missing or concurrently
// changed module configuration without conflating it with a database outage.
type GuildModuleConfigurationError struct{ Message string }

// Error exposes the configuration correction needed before retrying enablement.
func (e *GuildModuleConfigurationError) Error() string { return e.Message }
