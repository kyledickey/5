package model

import (
	"strings"
	"testing"
)

// wantAuditContracts pins the full expansion of the grouped contract table.
// Every action name here is persisted in audit_log_entries and filtered on by
// the Discord mirror, so a diff against this list is a schema-visible change.
var wantAuditContracts = map[string]AuditActionContract{
	"appeal.accepted":                            {"appeal", true},
	"appeal.close":                               {"appeal", true},
	"appeal.closed":                              {"appeal", true},
	"appeal.information.submit":                  {"appeal", false},
	"appeal.information_requested":               {"appeal", false},
	"appeal.queue.read":                          {"appeal", false},
	"appeal.read":                                {"appeal", false},
	"appeal.rejected":                            {"appeal", true},
	"appeal.reopened":                            {"appeal", false},
	"appeal.settings.update":                     {"guild_settings", true},
	"appeal.submit":                              {"appeal", true},
	"audit.read":                                 {"audit_log", false},
	"audit_mirror.delivered":                     {"audit_entry", false},
	"audit_mirror.failed":                        {"audit_entry", false},
	"audit_mirror.repaired":                      {"guild_settings", false},
	"audit_mirror.skipped":                       {"audit_entry", false},
	"authorization.denied":                       {"permission", false},
	"case.create":                                {"case", true},
	"case.history.read":                          {"member", false},
	"case.read":                                  {"case", false},
	"case.search":                                {"case", false},
	"case.update":                                {"case", true},
	"case.void":                                  {"case", true},
	"case.void.appeal":                           {"case", true},
	"case_action.attempt":                        {"case_action_execution", false},
	"case_action.dismiss":                        {"case_action_execution", true},
	"case_action.failed":                         {"case_action_execution", true},
	"case_action.failures.read":                  {"case_action_execution", false},
	"case_action.recovered":                      {"case_action_execution", false},
	"case_action.retry":                          {"case_action_execution", true},
	"case_action.retrying":                       {"case_action_execution", false},
	"case_action.reverse":                        {"case_action_execution", true},
	"case_action.skipped":                        {"case_action_execution", true},
	"case_action.succeeded":                      {"case_action_execution", true},
	"case_notification.failed":                   {"case_notification", false},
	"case_notification.sent":                     {"case_notification", false},
	"case_template.archive":                      {"case_template", true},
	"case_template.bootstrap":                    {"case_template", false},
	"case_template.create":                       {"case_template", true},
	"case_template.export":                       {"case_template", false},
	"case_template.import":                       {"case_template", true},
	"case_template.read":                         {"case_template", false},
	"case_template.restore":                      {"case_template", true},
	"case_template.update":                       {"case_template", true},
	"evidence.capture":                           {"case_evidence", false},
	"evidence_channel.ensure":                    {"guild_settings", false},
	"general_logging.channel_repair":             {"general_logging_settings", false},
	"general_logging.settings.update":            {"general_logging_settings", true},
	"general_logging.v4_settings_import":         {"general_logging_settings_import", true},
	"guild.lifecycle.bootstrap":                  {"guild", false},
	"guild.lifecycle.leave":                      {"guild", false},
	"guild_settings.channel_reference.cleared":   {"guild_settings", false},
	"guild_settings.channel_references.repaired": {"guild_settings", false},
	"guild_settings.read":                        {"guild_settings", false},
	"guild_settings.update":                      {"guild_settings", true},
	"honeypot.case.created":                      {"case", false},
	"honeypot.configuration.disabled":            {"honeypot_settings", true},
	"honeypot.settings.read":                     {"honeypot_settings", false},
	"honeypot.settings.update":                   {"honeypot_settings", true},
	"honeypot.trigger":                           {"case", false},
	"honeypot.trigger.detected":                  {"honeypot_trigger", false},
	"honeypot.trigger.failed":                    {"honeypot_trigger", true},
	"honeypot.v4_settings_import":                {"honeypot_settings_import", true},
	"member_case.list":                           {"guild", false},
	"member_case.read":                           {"case", false},
	"statistics.read":                            {"statistics", false},
	"ticket.cancel":                              {"ticket", true},
	"ticket.entry_channel_repair":                {"ticket", false},
	"ticket.open":                                {"ticket", true},
	"ticket.reopen":                              {"ticket", false},
	"ticket.reply":                               {"ticket", false},
	"ticket.resolve":                             {"ticket", true},
	"ticket.settings.read":                       {"ticket", false},
	"ticket.settings.update":                     {"ticket", true},
	"ticket.v4_import":                           {"ticket_import", true},
	"v4_import.batch":                            {"import_batch", true},
}

func TestAuditContractTableMatchesPersistedExpansion(t *testing.T) {
	if len(auditActionContracts) != len(wantAuditContracts) {
		t.Fatalf("contract count changed: got %d want %d", len(auditActionContracts), len(wantAuditContracts))
	}
	for action, want := range wantAuditContracts {
		if got := AuditContract(action, "fallback"); got != want {
			t.Errorf("%s: got %+v want %+v", action, got, want)
		}
	}
}

func TestOperationalRedactionRecursesAcrossCredentialsContentAndPayloads(t *testing.T) {
	raw := `{"safe":"count","nested":{"oauth_token":"oauth-secret","cookie":"cookie-secret","session_id":"session-secret","webhook_url":"https://discord.invalid/webhook-secret","member_content":"private words","action_payload":{"reason":"private payload"}},"items":[{"authorization":"Bearer secret"}]}`
	redacted := RedactAuditMetadata(raw)
	for _, secret := range []string{"oauth-secret", "cookie-secret", "session-secret", "webhook-secret", "private words", "private payload", "Bearer secret"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redaction exposed %q in %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, `"safe":"count"`) {
		t.Fatalf("redaction removed safe aggregate field: %s", redacted)
	}
}
