package model

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
)

const (
	// AuditSourceImport identifies an operator-controlled historical import.
	AuditSourceImport AuditSource = "import"
	// AuditSourceHoneypot identifies an automated case created by the isolated honeypot module.
	AuditSourceHoneypot AuditSource = "honeypot"
)

// AuditAction is a stable, filterable name for one meaningful Quack event.
type AuditAction string

const (
	AuditActionAuthorizationDenied        AuditAction = "authorization.denied"
	AuditActionAuditRead                  AuditAction = "audit.read"
	AuditActionStatisticsRead             AuditAction = "statistics.read"
	AuditActionCaseCreate                 AuditAction = "case.create"
	AuditActionCaseRead                   AuditAction = "case.read"
	AuditActionCaseSearch                 AuditAction = "case.search"
	AuditActionCaseHistoryRead            AuditAction = "case.history.read"
	AuditActionCaseVoid                   AuditAction = "case.void"
	AuditActionCaseUpdate                 AuditAction = "case.update"
	AuditActionEvidenceCapture            AuditAction = "evidence.capture"
	AuditActionTemplateCreate             AuditAction = "case_template.create"
	AuditActionTemplateUpdate             AuditAction = "case_template.update"
	AuditActionTemplateArchive            AuditAction = "case_template.archive"
	AuditActionTemplateRestore            AuditAction = "case_template.restore"
	AuditActionTemplateImport             AuditAction = "case_template.import"
	AuditActionTemplateExport             AuditAction = "case_template.export"
	AuditActionTemplateRead               AuditAction = "case_template.read"
	AuditActionSettingsRead               AuditAction = "guild_settings.read"
	AuditActionSettingsUpdate             AuditAction = "guild_settings.update"
	AuditActionActionAttempt              AuditAction = "case_action.attempt"
	AuditActionActionSucceeded            AuditAction = "case_action.succeeded"
	AuditActionActionRetrying             AuditAction = "case_action.retrying"
	AuditActionActionFailed               AuditAction = "case_action.failed"
	AuditActionActionSkipped              AuditAction = "case_action.skipped"
	AuditActionActionRetry                AuditAction = "case_action.retry"
	AuditActionActionDismiss              AuditAction = "case_action.dismiss"
	AuditActionActionReverse              AuditAction = "case_action.reverse"
	AuditActionActionFailureRead          AuditAction = "case_action.failures.read"
	AuditActionActionRecovered            AuditAction = "case_action.recovered"
	AuditActionNotificationSent           AuditAction = "case_notification.sent"
	AuditActionNotificationFailed         AuditAction = "case_notification.failed"
	AuditActionAppealRead                 AuditAction = "appeal.read"
	AuditActionAppealSettingsUpdate       AuditAction = "appeal.settings.update"
	AuditActionAppealSubmit               AuditAction = "appeal.submit"
	AuditActionAppealInformationSubmit    AuditAction = "appeal.information.submit"
	AuditActionAppealQueueRead            AuditAction = "appeal.queue.read"
	AuditActionAppealInformationRequested AuditAction = "appeal.information_requested"
	AuditActionAppealReopened             AuditAction = "appeal.reopened"
	AuditActionAppealAccepted             AuditAction = "appeal.accepted"
	AuditActionAppealRejected             AuditAction = "appeal.rejected"
	AuditActionAppealClose                AuditAction = "appeal.close"
	AuditActionAppealClosed               AuditAction = "appeal.closed"
	AuditActionCaseVoidAppeal             AuditAction = "case.void.appeal"
	AuditActionMirrorDelivered            AuditAction = "audit_mirror.delivered"
	AuditActionMirrorFailed               AuditAction = "audit_mirror.failed"
	AuditActionMirrorRepaired             AuditAction = "audit_mirror.repaired"
	AuditActionMirrorSkipped              AuditAction = "audit_mirror.skipped"
	AuditActionImportBatch                AuditAction = "v4_import.batch"
	AuditActionHoneypotTrigger            AuditAction = "honeypot.trigger"
)

// AuditActionContract defines the stable resource and mirror policy for an audit action.
// Metadata remains an object of identifiers, counts, states, and bounded diagnostics;
// secrets, credentials, transport payloads, and member content are always redacted.
type AuditActionContract struct {
	Action       AuditAction
	ResourceType string
	Important    bool
}

var auditActionContracts = map[AuditAction]AuditActionContract{
	AuditActionCaseUpdate:                                     {AuditActionCaseUpdate, "case", true},
	AuditActionAuthorizationDenied:                            {AuditActionAuthorizationDenied, "permission", false},
	AuditActionAuditRead:                                      {AuditActionAuditRead, "audit_log", false},
	AuditActionStatisticsRead:                                 {AuditActionStatisticsRead, "statistics", false},
	AuditActionCaseCreate:                                     {AuditActionCaseCreate, "case", true},
	AuditActionCaseRead:                                       {AuditActionCaseRead, "case", false},
	AuditActionCaseSearch:                                     {AuditActionCaseSearch, "case", false},
	AuditActionCaseHistoryRead:                                {AuditActionCaseHistoryRead, "member", false},
	AuditActionCaseVoid:                                       {AuditActionCaseVoid, "case", true},
	AuditActionEvidenceCapture:                                {AuditActionEvidenceCapture, "case_evidence", false},
	AuditActionTemplateCreate:                                 {AuditActionTemplateCreate, "case_template", true},
	AuditActionTemplateUpdate:                                 {AuditActionTemplateUpdate, "case_template", true},
	AuditActionTemplateArchive:                                {AuditActionTemplateArchive, "case_template", true},
	AuditActionTemplateRestore:                                {AuditActionTemplateRestore, "case_template", true},
	AuditActionTemplateImport:                                 {AuditActionTemplateImport, "case_template", true},
	AuditActionTemplateExport:                                 {AuditActionTemplateExport, "case_template", false},
	AuditActionTemplateRead:                                   {AuditActionTemplateRead, "case_template", false},
	AuditActionSettingsRead:                                   {AuditActionSettingsRead, "guild_settings", false},
	AuditActionSettingsUpdate:                                 {AuditActionSettingsUpdate, "guild_settings", true},
	AuditActionActionAttempt:                                  {AuditActionActionAttempt, "case_action_execution", false},
	AuditActionActionSucceeded:                                {AuditActionActionSucceeded, "case_action_execution", true},
	AuditActionActionRetrying:                                 {AuditActionActionRetrying, "case_action_execution", false},
	AuditActionActionFailed:                                   {AuditActionActionFailed, "case_action_execution", true},
	AuditActionActionSkipped:                                  {AuditActionActionSkipped, "case_action_execution", true},
	AuditActionActionRetry:                                    {AuditActionActionRetry, "case_action_execution", true},
	AuditActionActionDismiss:                                  {AuditActionActionDismiss, "case_action_execution", true},
	AuditActionActionReverse:                                  {AuditActionActionReverse, "case_action_execution", true},
	AuditActionActionFailureRead:                              {AuditActionActionFailureRead, "case_action_execution", false},
	AuditActionActionRecovered:                                {AuditActionActionRecovered, "case_action_execution", false},
	AuditActionNotificationSent:                               {AuditActionNotificationSent, "case_notification", false},
	AuditActionNotificationFailed:                             {AuditActionNotificationFailed, "case_notification", false},
	AuditActionAppealRead:                                     {AuditActionAppealRead, "appeal", false},
	AuditActionAppealSettingsUpdate:                           {AuditActionAppealSettingsUpdate, "guild_settings", true},
	AuditActionAppealSubmit:                                   {AuditActionAppealSubmit, "appeal", true},
	AuditActionAppealInformationSubmit:                        {AuditActionAppealInformationSubmit, "appeal", false},
	AuditActionAppealQueueRead:                                {AuditActionAppealQueueRead, "appeal", false},
	AuditActionAppealInformationRequested:                     {AuditActionAppealInformationRequested, "appeal", false},
	AuditActionAppealReopened:                                 {AuditActionAppealReopened, "appeal", false},
	AuditActionAppealAccepted:                                 {AuditActionAppealAccepted, "appeal", true},
	AuditActionAppealRejected:                                 {AuditActionAppealRejected, "appeal", true},
	AuditActionAppealClose:                                    {AuditActionAppealClose, "appeal", true},
	AuditActionAppealClosed:                                   {AuditActionAppealClosed, "appeal", true},
	AuditActionCaseVoidAppeal:                                 {AuditActionCaseVoidAppeal, "case", true},
	AuditActionMirrorDelivered:                                {AuditActionMirrorDelivered, "audit_entry", false},
	AuditActionMirrorFailed:                                   {AuditActionMirrorFailed, "audit_entry", false},
	AuditActionMirrorRepaired:                                 {AuditActionMirrorRepaired, "guild_settings", false},
	AuditActionMirrorSkipped:                                  {AuditActionMirrorSkipped, "audit_entry", false},
	AuditActionImportBatch:                                    {AuditActionImportBatch, "import_batch", true},
	AuditActionHoneypotTrigger:                                {AuditActionHoneypotTrigger, "case", false},
	AuditAction("guild.lifecycle.bootstrap"):                  {AuditAction("guild.lifecycle.bootstrap"), "guild", false},
	AuditAction("guild.lifecycle.leave"):                      {AuditAction("guild.lifecycle.leave"), "guild", false},
	AuditAction("guild_settings.channel_reference.cleared"):   {AuditAction("guild_settings.channel_reference.cleared"), "guild_settings", false},
	AuditAction("guild_settings.channel_references.repaired"): {AuditAction("guild_settings.channel_references.repaired"), "guild_settings", false},
	AuditAction("case_template.bootstrap"):                    {AuditAction("case_template.bootstrap"), "case_template", false},
	AuditAction("evidence_channel.ensure"):                    {AuditAction("evidence_channel.ensure"), "guild_settings", false},
	AuditAction("member_case.list"):                           {AuditAction("member_case.list"), "guild", false},
	AuditAction("member_case.read"):                           {AuditAction("member_case.read"), "case", false},
	AuditAction("ticket.settings.read"):                       {AuditAction("ticket.settings.read"), "ticket", false},
	AuditAction("ticket.settings.update"):                     {AuditAction("ticket.settings.update"), "ticket", true},
	AuditAction("ticket.open"):                                {AuditAction("ticket.open"), "ticket", true},
	AuditAction("ticket.resolve"):                             {AuditAction("ticket.resolve"), "ticket", true},
	AuditAction("ticket.cancel"):                              {AuditAction("ticket.cancel"), "ticket", true},
	AuditAction("ticket.reopen"):                              {AuditAction("ticket.reopen"), "ticket", false},
	AuditAction("ticket.reply"):                               {AuditAction("ticket.reply"), "ticket", false},
	AuditAction("ticket.entry_channel_repair"):                {AuditAction("ticket.entry_channel_repair"), "ticket", false},
	AuditAction("ticket.v4_import"):                           {AuditAction("ticket.v4_import"), "ticket_import", true},
	AuditAction("general_logging.settings.update"):            {AuditAction("general_logging.settings.update"), "general_logging_settings", true},
	AuditAction("general_logging.channel_repair"):             {AuditAction("general_logging.channel_repair"), "general_logging_settings", false},
	AuditAction("general_logging.v4_settings_import"):         {AuditAction("general_logging.v4_settings_import"), "general_logging_settings_import", true},
	AuditAction("honeypot.settings.read"):                     {AuditAction("honeypot.settings.read"), "honeypot_settings", false},
	AuditAction("honeypot.settings.update"):                   {AuditAction("honeypot.settings.update"), "honeypot_settings", true},
	AuditAction("honeypot.trigger.detected"):                  {AuditAction("honeypot.trigger.detected"), "honeypot_trigger", false},
	AuditAction("honeypot.trigger.failed"):                    {AuditAction("honeypot.trigger.failed"), "honeypot_trigger", true},
	AuditAction("honeypot.case.created"):                      {AuditAction("honeypot.case.created"), "case", false},
	AuditAction("honeypot.configuration.disabled"):            {AuditAction("honeypot.configuration.disabled"), "honeypot_settings", true},
	AuditAction("honeypot.v4_settings_import"):                {AuditAction("honeypot.v4_settings_import"), "honeypot_settings_import", true},
}

// AuditContract returns the contract for a stable action name. Package-specific
// actions use their recorded resource type and are never mirrored by default.
func AuditContract(action string, resourceType string) AuditActionContract {
	key := AuditAction(strings.TrimSpace(action))
	if contract, ok := auditActionContracts[key]; ok {
		return contract
	}
	return AuditActionContract{Action: key, ResourceType: strings.TrimSpace(resourceType)}
}

// IsAuditEvent reports whether an action belongs in staff history. Reads, worker
// bookkeeping, and unknown service events never become product audit entries.
func IsAuditEvent(action string) bool {
	return AuditContract(action, "").Important
}

// ImportantAuditActions returns the deterministic event set used by history and its Discord mirror.
func ImportantAuditActions() []string {
	actions := make([]string, 0, len(auditActionContracts))
	for action, contract := range auditActionContracts {
		if contract.Important {
			actions = append(actions, string(action))
		}
	}
	slices.Sort(actions)
	return actions
}

// AuditMetadataRedactedValue is persisted in place of sensitive metadata values.
const AuditMetadataRedactedValue = "[REDACTED]"

// RedactAuditMetadata returns a canonical JSON object with sensitive keys and
// transport/member-content values removed recursively.
func RedactAuditMetadata(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return `{"redaction":"invalid_metadata_removed"}`
	}
	object, ok := value.(map[string]any)
	if !ok {
		return `{"redaction":"non_object_metadata_removed"}`
	}
	redactAuditValue(object)
	body, err := json.Marshal(object)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func redactAuditValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveAuditMetadataKey(key) {
				typed[key] = AuditMetadataRedactedValue
				continue
			}
			redactAuditValue(child)
		}
	case []any:
		for _, child := range typed {
			redactAuditValue(child)
		}
	}
}

func sensitiveAuditMetadataKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	for _, fragment := range []string{"token", "secret", "password", "cookie", "authorization", "webhook", "session", "access_key", "private_key", "payload", "message_content", "member_content", "transcript"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

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
