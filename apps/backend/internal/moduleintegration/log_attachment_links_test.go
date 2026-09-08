package moduleintegration

import (
	"encoding/json"
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"strings"
	"testing"
)

// TestDeletedAttachmentLinksSurviveGatewayProjection checks both single and bulk
// log representations against actual gateway attachment metadata.
func TestDeletedAttachmentLinksSurviveGatewayProjection(t *testing.T) {
	const link = "https://cdn.discordapp.com/attachments/channel/file/proof.png?ex=123&sig=abc"
	cached := cachedMessage("guild", &discordgo.Message{ID: "message", ChannelID: "channel", Author: &discordgo.User{ID: "member"}, Attachments: []*discordgo.MessageAttachment{{ID: "file", Filename: "proof.png", URL: link}}})
	for _, bulk := range []bool{false, true} {
		payload := map[string]any{"event": "message_delete", "attachments": cached.Attachments}
		if bulk {
			payload = map[string]any{"event": "message_bulk_delete", "messages": []any{map[string]any{"message_id": cached.MessageDiscordID, "actor_id": cached.AuthorDiscordUserID, "attachments": cached.Attachments}}}
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		message := views.StaffLogMessage(string(encoded))
		if !strings.Contains(message.Content, "[proof.png](<"+link+">)") {
			t.Fatalf("bulk=%v lost downloadable link: %s", bulk, message.Content)
		}
	}
}
