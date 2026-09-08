package generallogging

import "testing"

// TestRouteAllToMovesEveryEventTogether preserves bounded delivery settings while
// replacing legacy per-event destinations with the selected shared channel.
func TestRouteAllToMovesEveryEventTogether(t *testing.T) {
	settings := Defaults()
	settings.Channels[MessageDelete] = "old"
	settings.CacheEntriesPerGuild = 500
	settings.MaxDeliveryAttempts = 2
	settings = settings.RouteAllTo("staff")
	for _, event := range []EventType{MessageEdit, MessageDelete, MessageBulkDelete, MemberJoin, MemberLeave, DiscordBan, DiscordUnban, GuildChange, ChannelChange} {
		if settings.Channels[event] != "staff" {
			t.Fatalf("event %s retained another route", event)
		}
	}
	if !settings.IncludeMessageContent || !settings.IncludeAttachmentMetadata || !settings.IncludeEmbedMetadata || settings.CacheEntriesPerGuild != 500 || settings.MaxDeliveryAttempts != 2 {
		t.Fatalf("incorrect setup settings: %+v", settings)
	}
}
