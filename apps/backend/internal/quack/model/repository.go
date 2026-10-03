package model

import (
	"errors"
	"time"
)

// ErrTemplateConflict rejects an edit based on an outdated policy snapshot.
var ErrTemplateConflict = errors.New("template changed; reload it before saving")

type ExpandedCaseTemplate struct {
	Template      CaseTemplate
	ContextFields []CaseTemplateContextField
	Levels        []ExpandedCaseTemplateLevel
}

type ExpandedCaseTemplateLevel struct {
	Level   CaseTemplateLevel
	Actions []CaseTemplateLevelAction
}

type CreateCaseTemplateParams struct {
	Template      CaseTemplate
	ContextFields []CaseTemplateContextField
	Levels        []ExpandedCaseTemplateLevel
	Audit         *AuditLogEntry
}

type UpdateCaseTemplateParams struct {
	GuildID, TemplateID string
	ExpectedVersion     uint
	Template            CaseTemplate
	ContextFields       []CaseTemplateContextField
	Levels              []ExpandedCaseTemplateLevel
	Audit               *AuditLogEntry
}

type ListAuditLogEntriesParams struct {
	GuildID, ActorDiscordUserID, Source, Action, ResourceType, ResourceID string
	CaseID, MemberDiscordUserID, CreatedAfter, CreatedBefore, BeforeID    string
	Result                                                                AuditResult
	Limit, Offset                                                         int
}

type ListAuditLogEntriesResult struct {
	Entries []AuditLogEntry
	Total   int64
}

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

type CreatedCase struct {
	Case             Case
	Event            CaseEvent
	ActionExecutions []CaseActionExecution
	Evidence         []CaseEvidenceSnapshot
	Attachments      []CaseEvidenceAttachment
	Notification     *CaseNotification
}

type CountTemplateCasesForTargetParams struct {
	// CreatedAtOrAfter is an inclusive rolling-window boundary; nil counts all history.
	CreatedAtOrAfter                         *time.Time
	GuildID, TemplateID, TargetDiscordUserID string
}

type ListCasesParams struct {
	GuildID, TargetDiscordUserID, ModeratorDiscordUserID, TemplateID    string
	CaseNumber, ActionResult, AppealStatus, CreatedAfter, CreatedBefore string
	Validity                                                            CaseValidity
	Limit, Offset                                                       int
}

type ListCasesResult struct {
	Cases []Case
	Total int64
}

type TargetCaseSummary struct {
	Total      int64
	ByValidity map[CaseValidity]int64
	ByTemplate map[string]int64
}

type ClaimedCaseAction struct {
	Case      Case
	Execution CaseActionExecution
}

type ClaimCaseActionParams struct{ CaseID, WorkerID string }

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

type SkipCaseActionsParams struct {
	CaseID                           string
	AfterPosition                    int
	Reason, CorrelationID, RequestID string
}

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

type UpsertStaffMemberParams struct {
	GuildID, DiscordUserID string
	LastSeenPermissionBits uint64
	LastKnownDisplayName   string
	LastActiveAt           time.Time
}

type ActionStatusCount struct {
	Status ActionExecutionStatus
	Count  int64
}

type OldestActionExecution struct {
	ID, CaseID  string
	CaseNumber  uint64
	ActionType  ActionType
	Status      ActionExecutionStatus
	CreatedAt   time.Time
	NextRetryAt *time.Time
}

type RecentActionFailure struct {
	ID, CaseID               string
	CaseNumber               uint64
	ActionType               ActionType
	Status                   ActionExecutionStatus
	LastErrorCode, LastError string
	UpdatedAt                time.Time
}

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

// StaffStatisticsParams bounds a derived statistics query to one guild and time range.
type StaffStatisticsParams struct {
	GuildID string
	From    time.Time
	To      time.Time
}

// StatisticBucket is one stable label/count pair in a derived breakdown.
type StatisticBucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// StaffStatistics contains only derived guild-scoped operational counts. It
// intentionally contains no actor ranking or persisted aggregate state.
type StaffStatistics struct {
	From            time.Time         `json:"from"`
	To              time.Time         `json:"to"`
	CaseTotal       int64             `json:"case_total"`
	ActionTotal     int64             `json:"action_total"`
	AppealTotal     int64             `json:"appeal_total"`
	AuditTotal      int64             `json:"audit_total"`
	CasesByDay      []StatisticBucket `json:"cases_by_day"`
	CasesByTemplate []StatisticBucket `json:"cases_by_template"`
	CasesByValidity []StatisticBucket `json:"cases_by_validity"`
	CasesBySource   []StatisticBucket `json:"cases_by_source"`
	ActionsByDay    []StatisticBucket `json:"actions_by_day"`
	ActionsByType   []StatisticBucket `json:"actions_by_type"`
	ActionsByResult []StatisticBucket `json:"actions_by_result"`
	AppealsByDay    []StatisticBucket `json:"appeals_by_day"`
	AppealsByStatus []StatisticBucket `json:"appeals_by_status"`
	AuditsByDay     []StatisticBucket `json:"audits_by_day"`
	AuditsByAction  []StatisticBucket `json:"audits_by_action"`
	AuditsByResult  []StatisticBucket `json:"audits_by_result"`
	AuditsBySource  []StatisticBucket `json:"audits_by_source"`
}
