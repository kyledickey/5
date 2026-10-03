package model

import (
	"encoding/json"
	"slices"
	"strings"
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
	ResourceType string
	Important    bool
}

// auditActionContracts groups every stable action name under the contract it
// shares with the other actions on the same line. Action names are persisted in
// audit_log_entries and filtered on by the Discord mirror, so none may change.
// Actions owned by optional modules are written literally because their packages
// import this one.
var auditActionContracts = expandAuditContracts(map[AuditActionContract][]AuditAction{
	{"case", true}:  {AuditActionCaseCreate, AuditActionCaseUpdate, AuditActionCaseVoid, AuditActionCaseVoidAppeal},
	{"case", false}: {AuditActionCaseRead, AuditActionCaseSearch, AuditActionHoneypotTrigger, "honeypot.case.created", "member_case.read"},

	{"case_template", true}:  {AuditActionTemplateCreate, AuditActionTemplateUpdate, AuditActionTemplateArchive, AuditActionTemplateRestore, AuditActionTemplateImport},
	{"case_template", false}: {AuditActionTemplateExport, AuditActionTemplateRead, "case_template.bootstrap"},

	{"case_action_execution", true}:  {AuditActionActionSucceeded, AuditActionActionFailed, AuditActionActionSkipped, AuditActionActionRetry, AuditActionActionDismiss, AuditActionActionReverse},
	{"case_action_execution", false}: {AuditActionActionAttempt, AuditActionActionRetrying, AuditActionActionFailureRead, AuditActionActionRecovered},

	{"appeal", true}:  {AuditActionAppealSubmit, AuditActionAppealAccepted, AuditActionAppealRejected, AuditActionAppealClose, AuditActionAppealClosed},
	{"appeal", false}: {AuditActionAppealRead, AuditActionAppealInformationSubmit, AuditActionAppealQueueRead, AuditActionAppealInformationRequested, AuditActionAppealReopened},

	{"guild_settings", true}:  {AuditActionSettingsUpdate, AuditActionAppealSettingsUpdate},
	{"guild_settings", false}: {AuditActionSettingsRead, AuditActionMirrorRepaired, "guild_settings.channel_reference.cleared", "guild_settings.channel_references.repaired", "evidence_channel.ensure"},

	{"ticket", true}:             {"ticket.settings.update", "ticket.open", "ticket.resolve", "ticket.cancel"},
	{"ticket", false}:            {"ticket.settings.read", "ticket.reopen", "ticket.reply", "ticket.entry_channel_repair"},
	{"ticket_import", true}:      {"ticket.v4_import"},
	{"import_batch", true}:       {AuditActionImportBatch},
	{"case_notification", false}: {AuditActionNotificationSent, AuditActionNotificationFailed},
	{"audit_entry", false}:       {AuditActionMirrorDelivered, AuditActionMirrorFailed, AuditActionMirrorSkipped},
	{"audit_log", false}:         {AuditActionAuditRead},
	{"statistics", false}:        {AuditActionStatisticsRead},
	{"permission", false}:        {AuditActionAuthorizationDenied},
	{"member", false}:            {AuditActionCaseHistoryRead},
	{"case_evidence", false}:     {AuditActionEvidenceCapture},
	{"guild", false}:             {"guild.lifecycle.bootstrap", "guild.lifecycle.leave", "member_case.list"},

	{"general_logging_settings", true}:        {"general_logging.settings.update"},
	{"general_logging_settings", false}:       {"general_logging.channel_repair"},
	{"general_logging_settings_import", true}: {"general_logging.v4_settings_import"},
	{"honeypot_settings", true}:               {"honeypot.settings.update", "honeypot.configuration.disabled"},
	{"honeypot_settings", false}:              {"honeypot.settings.read"},
	{"honeypot_settings_import", true}:        {"honeypot.v4_settings_import"},
	{"honeypot_trigger", true}:                {"honeypot.trigger.failed"},
	{"honeypot_trigger", false}:               {"honeypot.trigger.detected"},
})

func expandAuditContracts(groups map[AuditActionContract][]AuditAction) map[AuditAction]AuditActionContract {
	contracts := make(map[AuditAction]AuditActionContract)
	for contract, actions := range groups {
		for _, action := range actions {
			contracts[action] = contract
		}
	}
	return contracts
}

// AuditContract returns the contract for a stable action name. Package-specific
// actions use their recorded resource type and are never mirrored by default.
func AuditContract(action string, resourceType string) AuditActionContract {
	if contract, ok := auditActionContracts[AuditAction(strings.TrimSpace(action))]; ok {
		return contract
	}
	return AuditActionContract{ResourceType: strings.TrimSpace(resourceType)}
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
