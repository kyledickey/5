package model

// CreateAppealParams carries one validated, case-owned submission and its atomic history evidence.
type CreateAppealParams struct {
	Appeal       Appeal
	Event        AppealEvent
	CaseEvent    CaseEvent
	Audit        AuditLogEntry
	Notification AppealNotification
}

// TransitionAppealParams carries one staff state change and its atomic case/audit/notification effects.
type TransitionAppealParams struct {
	GuildID, AppealID, ActorDiscordUserID string
	AllowedFrom                           []AppealStatus
	To                                    AppealStatus
	Reason                                string
	Event                                 AppealEvent
	AppealAudit                           AuditLogEntry
	CaseAudit                             *AuditLogEntry
	Notification                          AppealNotification
	VoidCase                              bool
}

// AppealListParams bounds a stable guild-owned staff queue.
type AppealListParams struct {
	GuildID       string
	Status        AppealStatus
	Limit, Offset int
}

// AppealListResult returns stable staff queue pagination.
type AppealListResult struct {
	Appeals []Appeal
	Total   int64
}

// UpdateGuildAppealSettingsParams carries a validated future form and its audit evidence.
type UpdateGuildAppealSettingsParams struct {
	Settings GuildAppealSettings
	Audit    AuditLogEntry
}

// CompleteAppealNotificationParams records one delivery outcome without mutating its timeline event.
type CompleteAppealNotificationParams struct {
	NotificationID, LeaseToken, DeliveryMessageID, DeliveryChannelID, ErrorCode string
	Status                                                                      AppealNotificationStatus
}
