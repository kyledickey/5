package model

import (
	"errors"
	"time"
)

var (
	// ErrAppealAlreadyExists reports the one-appeal-per-case uniqueness boundary.
	ErrAppealAlreadyExists = errors.New("appeal already exists for case")
	// ErrAppealStateConflict reports a stale or ineligible timeline transition.
	ErrAppealStateConflict = errors.New("appeal state conflict")
	// ErrAppealCaseIneligible reports a voided, non-appealable, missing, or unrelated case.
	ErrAppealCaseIneligible = errors.New("case is not eligible for appeal")
)

// AppealQuestionType identifies the intentionally small set of dashboard form controls.
type AppealQuestionType string

const (
	AppealQuestionShortText AppealQuestionType = "short_text"
	AppealQuestionLongText  AppealQuestionType = "long_text"
	AppealQuestionBoolean   AppealQuestionType = "boolean"
)

// AppealEventType identifies an immutable step in one case-linked appeal timeline.
type AppealEventType string

const (
	AppealEventSubmitted        AppealEventType = "submitted"
	AppealEventInformationAsked AppealEventType = "information_requested"
	AppealEventInformationAdded AppealEventType = "information_submitted"
	AppealEventReopened         AppealEventType = "reopened"
	AppealEventAccepted         AppealEventType = "accepted"
	AppealEventRejected         AppealEventType = "rejected"
	AppealEventClosed           AppealEventType = "closed"
)

// AppealQuestion is one ordered, member-visible question snapshotted at submission.
type AppealQuestion struct {
	ID       string             `json:"id"`
	Prompt   string             `json:"prompt"`
	Type     AppealQuestionType `json:"type"`
	Required bool               `json:"required"`
	Position int                `json:"position"`
}

// AppealAnswer is one answer keyed to a snapshotted question.
type AppealAnswer struct {
	QuestionID string `json:"question_id"`
	Value      any    `json:"value"`
}

// GuildAppealSettings stores the validated form used for future submissions.
type GuildAppealSettings struct {
	ULIDModel
	GuildID                string
	QuestionsJSON          string
	UpdatedByDiscordUserID string
}

// AppealNotificationAudience identifies whether an outbox message targets the member or staff queue.
type AppealNotificationAudience string

const (
	AppealNotificationMember AppealNotificationAudience = "member"
	AppealNotificationStaff  AppealNotificationAudience = "staff"
)

// AppealNotificationStatus identifies delivery progress for an appeal outbox item.
type AppealNotificationStatus string

const (
	AppealNotificationPending AppealNotificationStatus = "pending"
	AppealNotificationClaimed AppealNotificationStatus = "claimed"
	AppealNotificationSending AppealNotificationStatus = "sending"
	AppealNotificationSent    AppealNotificationStatus = "sent"
	AppealNotificationFailed  AppealNotificationStatus = "failed"
)

// AppealNotification is an idempotent outbox item without staff identity in its member-facing body.
type AppealNotification struct {
	ULIDModel
	AppealID            string
	EventID             string
	GuildID             string
	TargetDiscordUserID string
	Audience            AppealNotificationAudience
	Status              AppealNotificationStatus
	Body                string
	DecisionIntentJSON  string
	DeliveryChannelID   string
	RefreshRequested    bool
	DeliveryMessageID   string
	LastErrorCode       string
	LeaseToken          string
	LeaseExpiresAt      *time.Time
}

// AppealDecisionIntent freezes member-facing decision facts at the transition.
// Version identifies the durable payload contract; no current settings are needed
// to render a queued notice after restart or a later appeal transition.
type AppealDecisionIntent struct {
	CaseNumber uint64       `json:"case_number,omitempty"`
	CaseID     string       `json:"case_id,omitempty"`
	GuildName  string       `json:"guild_name,omitempty"`
	Version    int          `json:"version"`
	Status     AppealStatus `json:"status"`
	Reason     string       `json:"reason"`
	RejoinURL  string       `json:"rejoin_url,omitempty"`
}
