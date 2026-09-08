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
