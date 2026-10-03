package views

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestStaffLogExplainsBanWithoutInternalMetadata keeps moderator and target
// distinct and omits delivery/source identifiers from end-user copy.
func TestStaffLogExplainsBanWithoutInternalMetadata(t *testing.T) {
	message := StaffLogMessage(`{"event":"discord_ban","actor_id":"moderator","metadata":{"target_id":"member","reason":"Repeated spam","discord_audit_entry_id":"internal-entry","worker":"private"}}`)
	if !strings.Contains(message.Content, "<@member> was banned by <@moderator>") || !strings.Contains(message.Content, "Repeated spam") {
		t.Fatalf("missing ban context: %s", message.Content)
	}
	for _, hidden := range []string{"internal-entry", "worker", "target id", "private"} {
		if strings.Contains(message.Content, hidden) {
			t.Fatalf("internal metadata leaked: %s", message.Content)
		}
	}
}

func TestStaffLogUsesReadableChannelAndBulkDeleteDetails(t *testing.T) {
	channel := StaffLogMessage(`{"event":"channel_change","channel_id":"deleted","metadata":{"operation":"deleted","name":"old-channel"}}`)
	if !strings.Contains(channel.Content, "A channel was deleted: old-channel.") || strings.Contains(channel.Content, " in <#deleted>") {
		t.Fatalf("awkward deletion copy: %s", channel.Content)
	}
	bulk := StaffLogMessage(`{"event":"message_bulk_delete","channel_id":"channel","before":"saved message","metadata":{"message_count":"20","cached_count":"1"}}`)
	if !strings.Contains(bulk.Content, "20 messages were deleted") || !strings.Contains(bulk.Content, "Messages with saved content: 1") {
		t.Fatalf("missing bulk context: %s", bulk.Content)
	}
}

func TestEditLogDistinguishesEmptyTextAndRemovedFiles(t *testing.T) {
	message := StaffLogMessage(`{"event":"message_edit","before":"","before_known":true,"after":"new","before_attachments":[{"Filename":"proof.png"}],"attachments":[]}`)
	if !strings.Contains(message.Content, "Before: no text.") || !strings.Contains(message.Content, "Files before: proof.png") || !strings.Contains(message.Content, "Files after: none.") {
		t.Fatalf("missing edit details: %s", message.Content)
	}
	unknown := StaffLogMessage(`{"event":"message_edit","before_known":false,"after":"new"}`)
	if !strings.Contains(unknown.Content, "Previous text was not available.") {
		t.Fatalf("unknown text presented as empty: %s", unknown.Content)
	}
}

// TestBulkLogShowsEachAuthorWithTheirOwnFiles preserves legacy attribution while
// avoiding duplicate display of backward-compatible aggregate payload fields.
func TestBulkLogShowsEachAuthorWithTheirOwnFiles(t *testing.T) {
	message := StaffLogMessage(`{"event":"message_bulk_delete","before":"aggregate duplicate","attachments":[{"Filename":"aggregate.png"}],"messages":[{"message_id":"one","actor_id":"alice","content":"first text","attachments":[{"Filename":"first.png"}]},{"message_id":"two","actor_id":"bob","content":"second text","attachments":[{"Filename":"second.png"}]}],"metadata":{"message_count":"2","cached_count":"2"}}`)
	for _, part := range []string{"<@alice> · Message one", "first text", "Files: first.png", "<@bob> · Message two", "second text", "Files: second.png"} {
		if !strings.Contains(message.Content, part) {
			t.Fatalf("missing %q: %s", part, message.Content)
		}
	}
	if strings.Contains(message.Content, "aggregate") {
		t.Fatal("bulk content displayed twice")
	}
	if strings.Index(message.Content, "first.png") > strings.Index(message.Content, "<@bob>") {
		t.Fatal("files detached from author")
	}
}

// TestLogAttachmentLabelsRejectUnsafeDestinations keeps malformed historical or
// forged payloads from injecting Markdown links, while retaining file names.
func TestLogAttachmentLabelsRejectUnsafeDestinations(t *testing.T) {
	for _, raw := range []string{"", "javascript:alert(1)", "https://user:secret@example.com/proof", "https://example.com/a>)[evil](https://other.test)", "https://example.com/a\nb"} {
		if got := (logAttachment{Filename: "proof.png", URL: raw}).label(); got != "proof.png" {
			t.Fatalf("unsafe destination rendered: %q", got)
		}
	}
	got := (logAttachment{Filename: "[proof].png", URL: "https://cdn.discordapp.com/a(b).png"}).label()
	if !strings.Contains(got, "\\[proof\\]") || !strings.Contains(got, "(<https://cdn.discordapp.com/a(b).png>)") {
		t.Fatal(got)
	}
}

func TestAuditFailureShowsCaseAndPermissionCheckedRetry(t *testing.T) {
	notice := AuditMirrorMessage(quack.AuditMirrorMessage{ActorDiscordUserID: "quack-system", Action: "case_action.failed", Result: model.AuditResultFailure, CaseID: "case", CaseNumber: 42, TargetDiscordUserID: "123", TemplateName: "Spam", ActionType: model.ActionBanUser, RetryExecutionID: "execution", CorrelationID: "internal-correlation", MetadataJSON: `{"private":"not-for-display"}`})
	for _, want := range []string{"Quack", "Case #42", "<@123>", "Spam", "Ban"} {
		if !strings.Contains(notice.Content, want) {
			t.Fatalf("missing %s: %s", want, notice.Content)
		}
	}
	for _, hidden := range []string{"<@quack-system>", "internal-correlation", "not-for-display"} {
		if strings.Contains(notice.Content, hidden) {
			t.Fatalf("leaked %s", hidden)
		}
	}
	if len(notice.Components) != 1 {
		t.Fatal("missing retry button")
	}
	button := notice.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	id, err := ui.DecodeCustomID(button.CustomID)
	if err != nil || id.Namespace != "case" || id.Action != "retry" || id.Payload != "execution" {
		t.Fatalf("wrong recovery route: %+v %v", id, err)
	}
}

// TestCaseCreatedMirrorNamesDecisionWithoutClaimingCompletion keeps the compact
// creation entry useful even before Discord has attempted the selected action.
func TestCaseCreatedMirrorNamesDecisionWithoutClaimingCompletion(t *testing.T) {
	notice := AuditMirrorMessage(quack.AuditMirrorMessage{ActorDiscordUserID: "moderator", Action: "case.create", Result: model.AuditResultSuccess, CaseID: "case", CaseNumber: 42, TargetDiscordUserID: "member", TemplateName: "Spam", SelectedLevelName: "Third case", SelectedOutcome: "Timeout (24h)", MetadataJSON: `{"private":"hidden"}`})
	for _, want := range []string{"<@moderator>", "<@member>", "Case #42", "Spam", "Level: Third case", "Outcome: Timeout (24h)"} {
		if !strings.Contains(notice.Content, want) {
			t.Fatalf("missing %q: %s", want, notice.Content)
		}
	}
	for _, forbidden := range []string{"completed", "hidden", "queued", "succeeded"} {
		if strings.Contains(notice.Content, forbidden) {
			t.Fatalf("creation event claims completion or exposes metadata: %s", notice.Content)
		}
	}
	completed := AuditMirrorMessage(quack.AuditMirrorMessage{Action: "case_action.succeeded", ActionType: model.ActionTimeoutUser, Result: model.AuditResultSuccess, CaseID: "case", CaseNumber: 42, SelectedLevelName: "Third case", SelectedOutcome: "Timeout (24h)"})
	if !strings.Contains(completed.Content, "Timeout action completed") || strings.Contains(completed.Content, "Selected outcome") {
		t.Fatalf("execution result was conflated with policy selection: %s", completed.Content)
	}
}

// TestAuditSettingsMirrorOmitsStorageIdentifiers keeps administrative history
// readable without exposing database resource names or correlation keys.
func TestAuditSettingsMirrorOmitsStorageIdentifiers(t *testing.T) {
	message := AuditMirrorMessage(quack.AuditMirrorMessage{ActorDiscordUserID: "moderator", Action: "guild_settings.update", Result: model.AuditResultSuccess, ResourceType: "guild_settings", ResourceID: "internal-settings-id", AuditEntryID: "internal-audit-id"})
	if !strings.Contains(message.Content, "updated Quack settings") {
		t.Fatal("missing semantic action")
	}
	for _, internal := range []string{"guild_settings", "internal-settings-id", "internal-audit-id"} {
		if strings.Contains(message.Content, internal) {
			t.Fatalf("visible internal metadata: %s", message.Content)
		}
	}
}

// TestAuditReversalNoopDoesNotClaimRemoval describes successful absence checks
// without claiming the bot removed an expired or previously lifted punishment.
func TestAuditReversalNoopDoesNotClaimRemoval(t *testing.T) {
	message := AuditMirrorMessage(quack.AuditMirrorMessage{Action: "case_action.succeeded", ActionType: model.ActionRemoveTimeout, Result: model.AuditResultSuccess, ReversalNoop: true})
	if !strings.Contains(message.Content, "had already ended") || strings.Contains(message.Content, "completed") {
		t.Fatal(message.Content)
	}
}

// TestAuditMetadataHasNoVisualGap keeps every supporting line in compact subtext.
func TestAuditMetadataHasNoVisualGap(t *testing.T) {
	message := AuditMirrorMessage(quack.AuditMirrorMessage{Action: "case.create", Result: model.AuditResultSuccess, CaseID: "case", CaseNumber: 42, TargetDiscordUserID: "member", SelectedLevelName: "First case", SelectedOutcome: "Ban", OccurredAt: time.Unix(1700000000, 0)})
	lines := strings.Split(message.Content, "\n")
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "-# ") {
			t.Fatalf("metadata must be adjacent subtext: %q", message.Content)
		}
	}
	if !strings.Contains(message.Content, "<t:1700000000:R>") {
		t.Fatal(message.Content)
	}
}
