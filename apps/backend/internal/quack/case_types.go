package quack

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// CaseInput contains moderator-provided context; escalation, reason, and enforcement come from the template.
type CaseInput struct {
	Attachments             []DiscordAttachmentSnapshot `json:"attachments,omitempty"`
	TemplateID              string                      `json:"template_id"`
	TargetDiscordUserID     string                      `json:"target_discord_user_id"`
	Source                  model.CaseSource            `json:"source"`
	ContextChannelDiscordID string                      `json:"context_channel_discord_id"`
	ContextMessageDiscordID string                      `json:"context_message_discord_id"`
	ContextURL              string                      `json:"context_url"`
	Metadata                json.RawMessage             `json:"metadata"`
	ContextValues           []CaseContextValueInput     `json:"context_values"`
	EvidenceLinks           []string                    `json:"evidence_links"`
	ReplacesCaseID          string                      `json:"replaces_case_id,omitempty"`
	IdempotencyKey          string                      `json:"-"`
}

type CaseContextValueInput struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value" swaggertype:"object"`
}

// CaseContextValueResponse is the staff-only definition/value pair stored with the case.
type CaseContextValueResponse struct {
	Key       string                 `json:"key"`
	Label     string                 `json:"label"`
	FieldType model.ContextFieldType `json:"type"`
	Required  bool                   `json:"required"`
	Value     any                    `json:"value"`
}

// CaseListInput carries filters for a single authorized guild or member history query.
type CaseListInput struct {
	Limit                  string
	Offset                 string
	TargetDiscordUserID    string
	ModeratorDiscordUserID string
	TemplateID             string
	Validity               string
	CaseNumber             string
	ActionResult           string
	AppealStatus           string
	CreatedAfter           string
	CreatedBefore          string
}

type CaseListResponse struct {
	Cases  []CaseResponse `json:"cases"`
	Total  int64          `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// CaseDetailResponse adds authorized staff history, enforcement attempts, evidence, and delivery state to a case.
type CaseDetailResponse struct {
	CaseResponse
	TemplateSnapshot *CaseTemplateSnapshotResponse `json:"template_snapshot,omitempty"`
	Actions          []CaseActionDetailResponse    `json:"actions"`
	Events           []CaseEventResponse           `json:"events"`
	Evidence         []CaseEvidenceResponse        `json:"evidence"`
	Notification     *CaseNotificationResponse     `json:"notification,omitempty"`
}

// CaseProfileResponse combines a member history page with all-time guild summary counts.
type CaseProfileResponse struct {
	Cases   []CaseResponse     `json:"cases"`
	Total   int64              `json:"total"`
	Limit   int                `json:"limit"`
	Offset  int                `json:"offset"`
	Summary CaseProfileSummary `json:"summary"`
}

// CaseProfileSummary is the all-time count of a member's cases in the guild,
// broken down by validity and by template, independent of the page filters.
type CaseProfileSummary struct {
	Total      int64            `json:"total"`
	ByValidity map[string]int64 `json:"by_validity"`
	ByTemplate map[string]int64 `json:"by_template"`
}

// CaseResponse presents the immutable moderation decision separately from its current validity and action progress.
type CaseResponse struct {
	RuleName                string                     `json:"rule_name,omitempty"`
	EvidenceIncomplete      bool                       `json:"evidence_incomplete"`
	CreatedAt               time.Time                  `json:"created_at"`
	UpdatedAt               time.Time                  `json:"updated_at"`
	ID                      string                     `json:"id"`
	GuildID                 string                     `json:"guild_id"`
	CaseNumber              uint64                     `json:"case_number"`
	TemplateID              *string                    `json:"template_id"`
	TemplateVersion         uint                       `json:"template_version"`
	TargetDiscordUserID     string                     `json:"target_discord_user_id"`
	ModeratorDiscordUserID  string                     `json:"moderator_discord_user_id"`
	Reason                  string                     `json:"reason"`
	Validity                model.CaseValidity         `json:"validity"`
	Source                  model.CaseSource           `json:"source"`
	ContextChannelDiscordID string                     `json:"context_channel_discord_id,omitempty"`
	ContextMessageDiscordID string                     `json:"context_message_discord_id,omitempty"`
	ContextURL              string                     `json:"context_url,omitempty"`
	Metadata                any                        `json:"metadata"`
	ContextValues           []CaseContextValueResponse `json:"context_values"`
	VoidedReason            string                     `json:"voided_reason,omitempty"`
	VoidedAt                *time.Time                 `json:"voided_at,omitempty"`
	ReplacementCaseID       *string                    `json:"replacement_case_id,omitempty"`
	ReplacesCaseID          *string                    `json:"replaces_case_id,omitempty"`
	SelectedLevel           *CaseSelectedLevel         `json:"selected_level,omitempty"`
	Actions                 []CaseActionResponse       `json:"actions"`
}

// CaseEvidenceAttachmentResponse exposes stable evidence references without leaking the staff-only channel identity.
type CaseEvidenceAttachmentResponse struct {
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	OriginalURL  string `json:"original_url"`
	PreservedURL string `json:"preserved_url,omitempty"`
	CopyOutcome  string `json:"copy_outcome"`
	Warning      string `json:"warning,omitempty"`
	SizeBytes    int64  `json:"size_bytes"`
}

// CaseEvidenceResponse exposes an immutable message snapshot and bounded attachment metadata.
type CaseEvidenceResponse struct {
	ID                  string                           `json:"id"`
	AuthorDiscordUserID string                           `json:"author_discord_user_id"`
	MessageURL          string                           `json:"message_url"`
	Content             string                           `json:"content"`
	CaptureOutcome      string                           `json:"capture_outcome"`
	CaptureWarning      string                           `json:"capture_warning,omitempty"`
	MessageCreatedAt    time.Time                        `json:"message_created_at"`
	MessageEditedAt     *time.Time                       `json:"message_edited_at,omitempty"`
	Embeds              any                              `json:"embeds"`
	Attachments         []CaseEvidenceAttachmentResponse `json:"attachments"`
}

// CaseNotificationResponse exposes delivery state separately from enforcement.
type CaseNotificationResponse struct {
	Status        model.NotificationStatus `json:"status"`
	AttemptCount  uint8                    `json:"attempt_count"`
	LastErrorCode string                   `json:"last_error_code,omitempty"`
	LastError     string                   `json:"last_error,omitempty"`
	SentAt        *time.Time               `json:"sent_at,omitempty"`
}

// CaseSelectedLevel identifies the snapshotted escalation level and the all-time count that selected it.
type CaseSelectedLevel struct {
	TemplateLevelDetails
	MatchedCaseCount int64 `json:"matched_case_count"`
}

// CaseActionResponse summarizes configured enforcement without exposing worker lease tokens.
type CaseActionResponse struct {
	TimeoutUntil     *time.Time                  `json:"timeout_until,omitempty"`
	ID               string                      `json:"id"`
	Position         int                         `json:"position"`
	ActionType       model.ActionType            `json:"action_type"`
	Status           model.ActionExecutionStatus `json:"status"`
	TemplateActionID *string                     `json:"template_action_id"`
	IdempotencyKey   string                      `json:"idempotency_key"`
	NotifyUser       bool                        `json:"notify_user"`
	NotificationType string                      `json:"notification_type,omitempty"`
	MaxRetries       uint8                       `json:"max_retries"`
	RetryBackoffMS   int                         `json:"retry_backoff_ms"`
	SafeForRetry     bool                        `json:"safe_for_retry"`
	Irreversible     bool                        `json:"irreversible"`
}

// CaseActionDetailResponse adds the immutable action configuration and recorded attempts for staff review.
type CaseActionDetailResponse struct {
	CaseActionResponse
	ConfigSnapshot any                         `json:"config_snapshot"`
	AttemptCount   uint8                       `json:"attempt_count"`
	LastErrorCode  string                      `json:"last_error_code,omitempty"`
	LastError      string                      `json:"last_error,omitempty"`
	StartedAt      *time.Time                  `json:"started_at,omitempty"`
	FinishedAt     *time.Time                  `json:"finished_at,omitempty"`
	NextRetryAt    *time.Time                  `json:"next_retry_at,omitempty"`
	Attempts       []CaseActionAttemptResponse `json:"attempts"`
}

// CaseActionAttemptResponse describes one recorded Discord attempt for authorized staff diagnostics.
type CaseActionAttemptResponse struct {
	ID              string                    `json:"id"`
	ExecutionID     string                    `json:"execution_id"`
	AttemptNumber   uint8                     `json:"attempt_number"`
	Status          model.ActionAttemptStatus `json:"status"`
	WorkerID        string                    `json:"worker_id,omitempty"`
	StartedAt       time.Time                 `json:"started_at"`
	FinishedAt      *time.Time                `json:"finished_at,omitempty"`
	DurationMS      int64                     `json:"duration_ms"`
	ErrorCode       string                    `json:"error_code,omitempty"`
	ErrorMessage    string                    `json:"error_message,omitempty"`
	RequestPayload  any                       `json:"request_payload"`
	ResponsePayload any                       `json:"response_payload"`
}

type CaseEventResponse struct {
	ID                 string                `json:"id"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
	EventType          model.CaseEventType   `json:"event_type"`
	ActorDiscordUserID string                `json:"actor_discord_user_id,omitempty"`
	ActorType          string                `json:"actor_type"`
	Visibility         model.EventVisibility `json:"visibility"`
	Body               string                `json:"body"`
	Metadata           any                   `json:"metadata"`
}

// CaseTemplateSnapshotResponse preserves the policy version, context schema, and selected outcome used by a case.
type CaseTemplateSnapshotResponse struct {
	Template      templateSnapshotTemplate       `json:"template"`
	SelectedLevel CaseSelectedLevel              `json:"selected_level"`
	Actions       []templateSnapshotAction       `json:"actions"`
	ContextFields []TemplateContextFieldResponse `json:"context_fields"`
	ContextValues []CaseContextValueResponse     `json:"context_values"`
}

// templateSnapshotTemplate preserves the rule identity and official member-facing reason at creation.
type templateSnapshotTemplate struct {
	CaseDecayDays  int    `json:"case_decay_days"`
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	Version        uint   `json:"version"`
	ReasonTemplate string `json:"reason_template"`
	Appealable     bool   `json:"appealable"`
}

// templateSnapshotAction preserves admin-defined enforcement settings independently of later template edits.
type templateSnapshotAction struct {
	ID                     string           `json:"id"`
	ActionType             model.ActionType `json:"action_type"`
	TimeoutDurationSeconds int              `json:"timeout_duration_seconds,omitempty"`
	DeleteMessageSeconds   int              `json:"delete_message_seconds,omitempty"`
	MaxRetries             uint8            `json:"max_retries"`
}

// selectedTemplateLevel pairs an escalation decision with the count and actions needed to create its snapshot.
type selectedTemplateLevel struct {
	Level            model.CaseTemplateLevel
	Actions          []model.CaseTemplateLevelAction
	MatchedCaseCount int64
}

// caseCreatePreflight is what the unlocked preflight computed. The identity
// fields are compared against a fresh selection inside the guild lock so a
// concurrent template edit or escalation change cannot commit a stale decision;
// Captured carries the evidence snapshots taken before the transaction began.
type caseCreatePreflight struct {
	TemplateID, SelectedLevelID, ContextValuesJSON string
	TemplateVersion                                uint
	ActionType                                     model.ActionType
	Captured                                       CapturedEvidence
}

// caseCreateAttribution distinguishes a live staff request from the one
// product-approved system automation path without widening generic case APIs.
type caseCreateAttribution struct {
	actorType string
	system    bool
}

// normalizeOptionalTime validates stable RFC3339 staff-search boundaries.
func normalizeOptionalTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", validationCaseError("date filter must use RFC3339")
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func validActionExecutionStatus(value model.ActionExecutionStatus) bool {
	switch value {
	case model.ActionExecutionPending, model.ActionExecutionRunning, model.ActionExecutionSucceeded,
		model.ActionExecutionFailed, model.ActionExecutionRetrying, model.ActionExecutionSkipped,
		model.ActionExecutionCancelled:
		return true
	default:
		return false
	}
}

func validAppealStatus(value model.AppealStatus) bool {
	switch value {
	case model.AppealStatusPending, model.AppealStatusNeedsInformation, model.AppealStatusAccepted,
		model.AppealStatusRejected, model.AppealStatusClosed:
		return true
	default:
		return false
	}
}

// pagination parses the raw limit and offset query values shared by every case
// list endpoint. An empty limit defaults to 50 and is capped at 100; an empty
// offset defaults to 0. Non-numeric, zero, or negative values are validation errors.
func pagination(limitValue, offsetValue string) (int, int, error) {
	limit := 50
	if strings.TrimSpace(limitValue) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(limitValue))
		if err != nil || parsed <= 0 {
			return 0, 0, validationCaseError("limit must be a positive integer")
		}
		limit = parsed
	}
	if limit > 100 {
		limit = 100
	}

	offset := 0
	if strings.TrimSpace(offsetValue) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(offsetValue))
		if err != nil || parsed < 0 {
			return 0, 0, validationCaseError("offset must be a non-negative integer")
		}
		offset = parsed
	}

	return limit, offset, nil
}

func validCaseValidity(validity model.CaseValidity) bool {
	switch validity {
	case model.CaseValidityValid, model.CaseValidityVoided:
		return true
	default:
		return false
	}
}

func caseValiditySummary(source map[model.CaseValidity]int64) map[string]int64 {
	out := make(map[string]int64, len(source))
	for status, count := range source {
		out[string(status)] = count
	}
	return out
}

func validationCaseError(message string) error {
	return fmt.Errorf("%w: %s", ErrCaseValidation, message)
}

func validCaseSource(source model.CaseSource) bool {
	switch source {
	case model.CaseSourceDashboard, model.CaseSourceDiscord, model.CaseSourceHoneypot, model.CaseSourceV4Import:
		return true
	default:
		return false
	}
}

// irreversibleAction marks membership removals whose ambiguous failures require staff review.
func irreversibleAction(actionType model.ActionType) bool {
	switch actionType {
	case model.ActionTimeoutUser, model.ActionKickUser, model.ActionBanUser:
		return true
	default:
		return false
	}
}
