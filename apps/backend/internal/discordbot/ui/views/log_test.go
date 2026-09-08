package views

import (
	"strings"
	"testing"
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
