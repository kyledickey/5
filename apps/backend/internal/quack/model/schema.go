package model

import (
	"time"
)

type PermissionAction string

const (
	PermissionActionCaseCreate         PermissionAction = "case.create"
	PermissionActionCaseRead           PermissionAction = "case.read"
	PermissionActionCaseTemplateRead   PermissionAction = "case_template.read"
	PermissionActionCaseTemplateWrite  PermissionAction = "case_template.write"
	PermissionActionCaseTemplateDelete PermissionAction = "case_template.delete"
	PermissionActionAppealReview       PermissionAction = "appeal.review"
	PermissionActionTicketResolve      PermissionAction = "ticket.resolve"
	PermissionActionAuditRead          PermissionAction = "audit.read"
	PermissionActionGuildSettingsRead  PermissionAction = "guild_settings.read"
	PermissionActionGuildSettingsWrite PermissionAction = "guild_settings.write"
	PermissionActionCaseVoid           PermissionAction = "case.void"
	PermissionActionFailureDismiss     PermissionAction = "action_failure.dismiss"
)

// CaseValidity identifies whether a case participates in escalation history.
type CaseValidity string

const (
	CaseValidityValid  CaseValidity = "valid"
	CaseValidityVoided CaseValidity = "voided"
)

type CaseSource string

const (
	CaseSourceDashboard CaseSource = "dashboard"
	CaseSourceDiscord   CaseSource = "discord"
	CaseSourceHoneypot  CaseSource = "honeypot"
	CaseSourceV4Import  CaseSource = "v4_import"
)

type ActionType string

const (
	ActionSendDM        ActionType = "send_dm"
	ActionTimeoutUser   ActionType = "timeout_user"
	ActionKickUser      ActionType = "kick_user"
	ActionBanUser       ActionType = "ban_user"
	ActionRemoveTimeout ActionType = "remove_timeout"
	ActionUnbanUser     ActionType = "unban_user"
)

// ContextFieldType identifies the supported member-visible template context value shapes.
type ContextFieldType string

const (
	ContextFieldShortText   ContextFieldType = "short_text"
	ContextFieldLongText    ContextFieldType = "long_text"
	ContextFieldBoolean     ContextFieldType = "boolean"
	ContextFieldNumber      ContextFieldType = "number"
	ContextFieldMessageLink ContextFieldType = "discord_message_link"
)

// NotificationStatus identifies the durable state of the single automatic member notification for a case.
type NotificationStatus string

const (
	NotificationPending  NotificationStatus = "pending"
	NotificationPrepared NotificationStatus = "prepared"
	NotificationClaimed  NotificationStatus = "claimed"
	NotificationSending  NotificationStatus = "sending"
	NotificationSent     NotificationStatus = "sent"
	NotificationFailed   NotificationStatus = "failed"
)

type ActionExecutionStatus string

const (
	ActionExecutionPending   ActionExecutionStatus = "pending"
	ActionExecutionRunning   ActionExecutionStatus = "running"
	ActionExecutionSucceeded ActionExecutionStatus = "succeeded"
	ActionExecutionFailed    ActionExecutionStatus = "failed"
	ActionExecutionRetrying  ActionExecutionStatus = "retrying"
	ActionExecutionSkipped   ActionExecutionStatus = "skipped"
	ActionExecutionCancelled ActionExecutionStatus = "cancelled"
)

type ActionAttemptStatus string

const (
	ActionAttemptRunning   ActionAttemptStatus = "running"
	ActionAttemptSucceeded ActionAttemptStatus = "succeeded"
	ActionAttemptFailed    ActionAttemptStatus = "failed"
)

type EventVisibility string

const (
	EventVisibilityInternal EventVisibility = "internal"
	EventVisibilityStaff    EventVisibility = "staff"
	EventVisibilityPublic   EventVisibility = "public"
)

type CaseEventType string

const (
	CaseEventCreated            CaseEventType = "case_created"
	CaseEventActionQueued       CaseEventType = "action_queued"
	CaseEventActionSucceeded    CaseEventType = "action_succeeded"
	CaseEventActionFailed       CaseEventType = "action_failed"
	CaseEventVoided             CaseEventType = "case_voided"
	CaseEventReplaced           CaseEventType = "case_replaced"
	CaseEventActionRetried      CaseEventType = "action_retry_requested"
	CaseEventActionDismissed    CaseEventType = "action_failure_dismissed"
	CaseEventReversalQueued     CaseEventType = "action_reversal_queued"
	CaseEventNotificationSent   CaseEventType = "notification_sent"
	CaseEventNotificationFailed CaseEventType = "notification_failed"
	CaseEventAppealCreated      CaseEventType = "appeal_created"
)

type AppealStatus string

const (
	AppealStatusPending          AppealStatus = "pending"
	AppealStatusNeedsInformation AppealStatus = "needs_information"
	AppealStatusAccepted         AppealStatus = "accepted"
	AppealStatusRejected         AppealStatus = "rejected"
	AppealStatusClosed           AppealStatus = "closed"
)

type AuditSource string

const (
	AuditSourceAPI     AuditSource = "api"
	AuditSourceWeb     AuditSource = "web"
	AuditSourceDiscord AuditSource = "discord"
	AuditSourceSystem  AuditSource = "system"
)

type AuditResult string

const (
	AuditResultSuccess AuditResult = "success"
	AuditResultFailure AuditResult = "failure"
	AuditResultDenied  AuditResult = "denied"
)

type ULIDModel struct {
	ID        string    `gorm:"type:char(26);primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`
	UpdatedAt time.Time `gorm:"not null"`
}

type Guild struct {
	ULIDModel
	DiscordGuildID     string `gorm:"size:32;not null;uniqueIndex"`
	Name               string `gorm:"size:191;not null"`
	IconURL            string `gorm:"size:1024"`
	OwnerDiscordUserID string `gorm:"size:32;not null;index"`
	IsActive           bool   `gorm:"not null;index"`
}

// GuildSettings contains guild-owned Quack configuration without embedding optional-module runtime state in the moderation core.
type GuildSettings struct {
	ULIDModel
	GuildID                           string     `gorm:"type:char(26);not null;uniqueIndex"`
	AppealQueueChannelDiscordID       string     `gorm:"size:32;not null;default:''"`
	AppealRejoinURL                   string     `gorm:"size:256;not null;default:''"`
	AppealReviewReasonRequired        bool       `gorm:"not null;default:false"`
	AuditMirrorChannelDiscordID       string     `gorm:"size:32;not null;default:''"`
	ManagedEvidenceChannelDiscordID   string     `gorm:"size:32;not null;default:''"`
	NotificationIntroduction          string     `gorm:"type:text;not null"`
	NotificationFooter                string     `gorm:"type:text;not null"`
	TicketsEnabled                    bool       `gorm:"not null;default:false"`
	GeneralLoggingEnabled             bool       `gorm:"not null;default:false"`
	HoneypotEnabled                   bool       `gorm:"not null;default:false"`
	StarterPolicyTemplateID           string     `gorm:"type:char(26);not null;default:''"`
	StarterPolicyNoticePending        bool       `gorm:"not null"`
	StarterPolicyNoticeAcknowledgedAt *time.Time `gorm:"index"`
}

type StaffMember struct {
	ULIDModel
	GuildID                string     `gorm:"type:char(26);not null;uniqueIndex:idx_staff_member_guild_user,priority:1;index"`
	DiscordUserID          string     `gorm:"size:32;not null;uniqueIndex:idx_staff_member_guild_user,priority:2;index"`
	LastSeenPermissionBits uint64     `gorm:"type:bigint unsigned;not null;default:0"`
	LastKnownDisplayName   string     `gorm:"size:191"`
	LastActiveAt           *time.Time `gorm:"index"`
}

type CaseTemplate struct {
	ULIDModel
	GuildID                string     `gorm:"type:char(26);not null;uniqueIndex:idx_case_template_guild_slug,priority:1"`
	Slug                   string     `gorm:"size:64;not null;uniqueIndex:idx_case_template_guild_slug,priority:2"`
	Name                   string     `gorm:"size:191;not null"`
	Description            string     `gorm:"type:text;not null"`
	ReasonTemplate         string     `gorm:"type:text;not null"`
	CaseDecayDays          int        `gorm:"not null;default:0"`
	Appealable             bool       `gorm:"not null;default:false"`
	Version                uint       `gorm:"not null"`
	CreatedByDiscordUserID string     `gorm:"size:32;not null"`
	UpdatedByDiscordUserID string     `gorm:"size:32;not null"`
	ArchivedAt             *time.Time `gorm:"index"`
}

// CaseTemplateContextField defines one ordered member-visible value collected whenever a template is applied.
type CaseTemplateContextField struct {
	ULIDModel
	TemplateID string           `gorm:"type:char(26);not null;uniqueIndex:idx_template_context_key,priority:1;uniqueIndex:idx_template_context_position,priority:1;index"`
	Key        string           `gorm:"size:64;not null;uniqueIndex:idx_template_context_key,priority:2"`
	Label      string           `gorm:"size:191;not null"`
	FieldType  ContextFieldType `gorm:"size:32;not null"`
	Position   int              `gorm:"not null;uniqueIndex:idx_template_context_position,priority:2"`
	Required   bool             `gorm:"not null;default:false"`
}

type CaseTemplateLevel struct {
	ULIDModel
	TemplateID       string `gorm:"type:char(26);not null;uniqueIndex:idx_template_level_position,priority:1;index"`
	Position         int    `gorm:"not null;uniqueIndex:idx_template_level_position,priority:2"`
	Name             string `gorm:"size:191;not null"`
	IsDefault        bool   `gorm:"not null;default:false;index"`
	TriggerCaseCount int    `gorm:"not null;default:0"`
	NotifyUser       bool   `gorm:"not null;default:false"`
}

// CaseTemplateLevelAction is limited to one enforcement action per level by the
// uq_v5_level_enforcement_action unique index in schema.go.
type CaseTemplateLevelAction struct {
	ULIDModel
	LevelID    string     `gorm:"type:char(26);not null;index"`
	ActionType ActionType `gorm:"size:64;not null;index"`
	ConfigJSON string     `gorm:"type:json;not null"`
	MaxRetries uint8      `gorm:"not null;default:0"`
}

type Case struct {
	ULIDModel
	GuildID                 string       `gorm:"type:char(26);not null;index:idx_case_guild_case_number,priority:1,unique;index:idx_case_guild_target,priority:1;index:idx_case_guild_mod,priority:1;index:idx_case_guild_status,priority:1;uniqueIndex:idx_case_guild_idempotency,priority:1"`
	CaseNumber              uint64       `gorm:"type:bigint unsigned;not null;index:idx_case_guild_case_number,priority:2,unique"`
	TemplateID              *string      `gorm:"type:char(26);index"`
	TemplateVersion         uint         `gorm:"not null"`
	TemplateSnapshotJSON    string       `gorm:"type:json;not null"`
	TargetDiscordUserID     string       `gorm:"size:32;not null;index:idx_case_guild_target,priority:2"`
	ModeratorDiscordUserID  string       `gorm:"size:32;not null;index:idx_case_guild_mod,priority:2"`
	Reason                  string       `gorm:"type:text;not null"`
	Validity                CaseValidity `gorm:"column:status;size:32;not null;index:idx_case_guild_status,priority:2"`
	Source                  CaseSource   `gorm:"size:32;not null;index"`
	CorrelationID           string       `gorm:"size:128;index"`
	ContextChannelDiscordID string       `gorm:"size:32"`
	ContextMessageDiscordID string       `gorm:"size:32"`
	ContextURL              string       `gorm:"size:1024"`
	MetadataJSON            string       `gorm:"type:json;not null"`
	ContextValuesJSON       string       `gorm:"type:json"`
	VoidedReason            string       `gorm:"type:text"`
	VoidedByDiscordUserID   string       `gorm:"size:32;not null;default:''"`
	VoidedAt                *time.Time   `gorm:"index"`
	ReplacementCaseID       *string      `gorm:"type:char(26);index"`
	ReplacesCaseID          *string      `gorm:"type:char(26);index"`
	IdempotencyKey          *string      `gorm:"size:191;uniqueIndex:idx_case_guild_idempotency,priority:2"`
}

type CaseActionExecution struct {
	ULIDModel
	CaseID                   string                `gorm:"type:char(26);not null;index:idx_action_execution_case_position,priority:1;index"`
	TemplateActionID         *string               `gorm:"type:char(26);index"`
	Position                 int                   `gorm:"not null;index:idx_action_execution_case_position,priority:2"`
	ActionType               ActionType            `gorm:"size:64;not null;index"`
	Status                   ActionExecutionStatus `gorm:"size:32;not null;index:idx_action_execution_status_retry,priority:1"`
	IdempotencyKey           string                `gorm:"size:191;not null;uniqueIndex"`
	ConfigSnapshotJSON       string                `gorm:"type:json;not null"`
	NotifyUser               bool                  `gorm:"not null;default:false"`
	NotificationType         string                `gorm:"size:64"`
	AttemptCount             uint8                 `gorm:"not null;default:0"`
	MaxRetries               uint8                 `gorm:"not null;default:0"`
	RetryBackoffMS           int                   `gorm:"not null;default:0"`
	SafeForRetry             bool                  `gorm:"not null"`
	Irreversible             bool                  `gorm:"not null;default:false"`
	LastErrorCode            string                `gorm:"size:64"`
	LastError                string                `gorm:"type:text"`
	StartedAt                *time.Time
	FinishedAt               *time.Time
	NextRetryAt              *time.Time `gorm:"index:idx_action_execution_status_retry,priority:2"`
	CorrelationID            string     `gorm:"size:128;index"`
	LeaseToken               string     `gorm:"size:64;index"`
	LeaseExpiresAt           *time.Time `gorm:"index"`
	DismissedAt              *time.Time `gorm:"index"`
	DismissedByDiscordUserID string     `gorm:"size:32;not null;default:''"`
	ReversalOfExecutionID    *string    `gorm:"type:char(26);index"`
	ReversalAppealID         *string    `gorm:"type:char(26);index"`
}

// CaseEvidenceSnapshot is an immutable, transport-neutral snapshot of a Discord message linked to a case.
type CaseEvidenceSnapshot struct {
	ULIDModel
	CaseID              string    `gorm:"type:char(26);not null;index"`
	GuildID             string    `gorm:"type:char(26);not null;index"`
	ChannelDiscordID    string    `gorm:"size:32;not null"`
	MessageDiscordID    string    `gorm:"size:32;not null"`
	AuthorDiscordUserID string    `gorm:"size:32;not null"`
	MessageURL          string    `gorm:"size:1024;not null"`
	Content             string    `gorm:"type:text;not null"`
	MessageCreatedAt    time.Time `gorm:"not null"`
	MessageEditedAt     *time.Time
	EmbedsJSON          string `gorm:"type:json;not null"`
	CaptureOutcome      string `gorm:"size:32;not null"`
	CaptureWarning      string `gorm:"type:text;not null"`
}

// CaseEvidenceAttachment preserves attachment metadata and, when possible, a stable managed-channel copy.
type CaseEvidenceAttachment struct {
	ULIDModel
	EvidenceID                   string `gorm:"type:char(26);not null;index"`
	Filename                     string `gorm:"size:255;not null"`
	ContentType                  string `gorm:"size:191;not null"`
	SizeBytes                    int64  `gorm:"not null"`
	OriginalURL                  string `gorm:"size:2048;not null"`
	PreservedURL                 string `gorm:"size:2048;not null"`
	PreservedMessageDiscordID    string `gorm:"size:32;not null"`
	PreservedAttachmentDiscordID string `gorm:"size:32;not null"`
	CopyOutcome                  string `gorm:"size:32;not null"`
	Warning                      string `gorm:"type:text;not null"`
}

// CaseNotification is the single durable automatic member notification owned by a case rather than an action.
type CaseNotification struct {
	ULIDModel
	CaseID                   string             `gorm:"type:char(26);not null;uniqueIndex"`
	Status                   NotificationStatus `gorm:"size:32;not null;index"`
	PreparedChannelDiscordID string             `gorm:"size:32;not null"`
	RenderedMessage          string             `gorm:"type:text;not null"`
	DeliveryMessageDiscordID string             `gorm:"size:32;not null"`
	AttemptCount             uint8              `gorm:"not null;default:0"`
	LastErrorCode            string             `gorm:"size:64;not null"`
	LastError                string             `gorm:"type:text;not null"`
	LeaseToken               string             `gorm:"size:64;index"`
	LeaseExpiresAt           *time.Time         `gorm:"index"`
	SentAt                   *time.Time
}

type CaseActionAttempt struct {
	ULIDModel
	ExecutionID         string              `gorm:"type:char(26);not null;uniqueIndex:idx_action_attempt_execution_number,priority:1;index"`
	AttemptNumber       uint8               `gorm:"not null;uniqueIndex:idx_action_attempt_execution_number,priority:2"`
	Status              ActionAttemptStatus `gorm:"size:32;not null;index"`
	WorkerID            string              `gorm:"size:64"`
	StartedAt           time.Time           `gorm:"not null"`
	FinishedAt          *time.Time
	DurationMS          int64  `gorm:"not null;default:0"`
	ErrorCode           string `gorm:"size:64"`
	ErrorMessage        string `gorm:"type:text"`
	RequestPayloadJSON  string `gorm:"type:json;not null"`
	ResponsePayloadJSON string `gorm:"type:json;not null"`
}

type CaseEvent struct {
	ULIDModel
	CaseID             string          `gorm:"type:char(26);not null;index:idx_case_event_case_created,priority:1"`
	GuildID            string          `gorm:"type:char(26);not null;index"`
	EventType          CaseEventType   `gorm:"size:64;not null;index"`
	ActorDiscordUserID string          `gorm:"size:32;index"`
	ActorType          string          `gorm:"size:32;not null"`
	Visibility         EventVisibility `gorm:"size:32;not null"`
	Body               string          `gorm:"type:text;not null"`
	MetadataJSON       string          `gorm:"type:json;not null"`
}

// Appeal is one case-linked appeal, its immutable submission, and its terminal
// review state. The unique case reference enforces one appeal per case.
type Appeal struct {
	ULIDModel
	GuildID                 string       `gorm:"type:char(26);not null;index:idx_appeal_guild_status,priority:1;index:idx_appeal_guild_user,priority:1"`
	CaseID                  *string      `gorm:"type:char(26);uniqueIndex"`
	TargetDiscordUserID     string       `gorm:"size:32;not null;index:idx_appeal_guild_user,priority:2"`
	Status                  AppealStatus `gorm:"size:32;not null;index:idx_appeal_guild_status,priority:2"`
	Content                 string       `gorm:"type:text;not null"`
	QuestionSnapshotJSON    string       `gorm:"type:json;not null"`
	AnswersJSON             string       `gorm:"type:json;not null"`
	Version                 uint64       `gorm:"type:bigint unsigned;not null"`
	DecisionReason          string       `gorm:"type:text"`
	ReviewedByDiscordUserID string       `gorm:"size:32"`
	ReviewedAt              *time.Time   `gorm:"index"`
	ReviewMessageDiscordID  string       `gorm:"size:32"`
	MetadataJSON            string       `gorm:"type:json;not null"`
}

// AppealEvent keeps actor type separate from actor identity so member responses
// can omit moderator attribution.
type AppealEvent struct {
	ULIDModel
	AppealID           string `gorm:"type:char(26);not null;index"`
	GuildID            string `gorm:"type:char(26);not null;index"`
	EventType          string `gorm:"size:64;not null;index"`
	ActorDiscordUserID string `gorm:"size:32;index"`
	ActorType          string `gorm:"size:32;not null"`
	Body               string `gorm:"type:text;not null"`
	MetadataJSON       string `gorm:"type:json;not null"`
}

type AuditLogEntry struct {
	ULIDModel
	GuildID             string      `gorm:"type:char(26);not null;index:idx_audit_guild_action,priority:1;index"`
	ActorDiscordUserID  string      `gorm:"size:32;index"`
	ActorPermissionBits uint64      `gorm:"type:bigint unsigned;not null;default:0"`
	Source              AuditSource `gorm:"size:32;not null;index"`
	Action              string      `gorm:"size:96;not null;index:idx_audit_guild_action,priority:2"`
	ResourceType        string      `gorm:"size:64;not null;index:idx_audit_resource,priority:1"`
	ResourceID          string      `gorm:"size:64;not null;index:idx_audit_resource,priority:2"`
	Result              AuditResult `gorm:"size:32;not null;index"`
	FailureReason       string      `gorm:"type:text"`
	CorrelationID       string      `gorm:"size:128;index"`
	RequestID           string      `gorm:"size:128;index"`
	MetadataJSON        string      `gorm:"type:json;not null"`
}

// CasePublication tracks a public case message independently of an expiring
// interaction token. PresentationJSON contains only the original public display
// fields; it is never a serialized staff case detail or evidence record.
type CasePublication struct {
	MessageID        string    `gorm:"size:32;primaryKey"`
	CaseID           string    `gorm:"type:char(26);not null;index"`
	ChannelID        string    `gorm:"size:32;not null"`
	PresentationJSON string    `gorm:"type:longtext;not null"`
	LastDigest       string    `gorm:"size:64;not null"`
	RetryAt          time.Time `gorm:"not null;index;index:idx_case_publication_due,priority:2"`
	// RefreshRequested limits scanning to new, changed or still-pending receipts.
	RefreshRequested bool `gorm:"not null;default:true;index:idx_case_publication_due,priority:1"`
	// Revision fences completion against mutations committed during a refresh.
	Revision uint64 `gorm:"not null;default:0"`
}

type OAuthState struct {
	RedirectTo   string    `json:"redirect_to"`
	ResponseMode string    `json:"response_mode"`
	CreatedAt    time.Time `json:"created_at"`
}

type AuthSession struct {
	ID               string    `json:"-"`
	DiscordUserID    string    `json:"discord_user_id"`
	Username         string    `json:"username"`
	GlobalName       string    `json:"global_name"`
	Avatar           string    `json:"avatar"`
	AccessToken      string    `json:"-"`
	RefreshToken     string    `json:"-"`
	CSRFToken        string    `json:"-"`
	TokenType        string    `json:"token_type"`
	Scope            string    `json:"scope"`
	TokenExpiresAt   time.Time `json:"token_expires_at"`
	SessionExpiresAt time.Time `json:"session_expires_at"`
	CreatedAt        time.Time `json:"created_at"`
	LastSeenAt       time.Time `json:"last_seen_at"`
}

// Label gives a human-readable outcome name for Discord and member messages.
// Persisted values and JSON contracts retain their stable action identifiers.
func (action ActionType) Label() string {
	switch action {
	case ActionTimeoutUser:
		return "Timeout"
	case ActionKickUser:
		return "Kick"
	case ActionBanUser:
		return "Ban"
	case ActionRemoveTimeout:
		return "Remove timeout"
	case ActionUnbanUser:
		return "Unban"
	case ActionSendDM:
		return "Notification"
	default:
		return "Action"
	}
}

// Label describes execution progress without exposing internal state names.
func (status ActionExecutionStatus) Label() string {
	switch status {
	case ActionExecutionPending:
		return "Queued"
	case ActionExecutionRunning:
		return "In progress"
	case ActionExecutionSucceeded:
		return "Completed"
	case ActionExecutionFailed:
		return "Needs review"
	case ActionExecutionRetrying:
		return "Retry scheduled"
	case ActionExecutionSkipped:
		return "Skipped"
	case ActionExecutionCancelled:
		return "Cancelled"
	default:
		return "Unknown"
	}
}
