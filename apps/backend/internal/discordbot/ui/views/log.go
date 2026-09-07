package views

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// StaffLogMessage presents the logging module's already-redacted payload at the Discord boundary.
// The module retains its structured JSON contract and controls which content may be included.
func StaffLogMessage(payload string) ui.Message {
	var event struct {
		Type        string `json:"event"`
		ChannelID   string `json:"channel_id"`
		MessageID   string `json:"message_id"`
		ActorID     string `json:"actor_id"`
		Before      string `json:"before"`
		After       string `json:"after"`
		Attachments []struct {
			Filename string `json:"filename"`
		} `json:"attachments"`
		EmbedTypes []string          `json:"embed_types"`
		Metadata   map[string]string `json:"metadata"`
	}
	if json.Unmarshal([]byte(payload), &event) != nil {
		return ui.Signal("info", "An event was recorded, but its details are unavailable.", false)
	}
	actor := "A member"
	if event.ActorID != "" {
		actor = "<@" + event.ActorID + ">"
	}
	var lead string
	switch event.Type {
	case "message_edit":
		lead = "A message from " + actor + " was edited."
	case "message_delete":
		lead = "A message from " + actor + " was deleted."
	case "message_bulk_delete":
		lead = "Messages were deleted."
	case "member_join":
		lead = actor + " joined the server."
	case "member_leave":
		lead = actor + " left the server."
	case "discord_ban":
		lead = "A ban was recorded."
	case "discord_unban":
		lead = "A ban was removed."
	case "guild_change":
		lead = "Server settings changed."
	case "channel_change":
		lead = "A channel changed."
	default:
		lead = "Server activity was recorded."
	}
	if event.ChannelID != "" {
		lead = strings.TrimSuffix(lead, ".") + " in <#" + event.ChannelID + ">."
	}
	parts := []string{}
	if event.Before != "" {
		before := ui.Quote(ui.PlainText(event.Before))
		if event.Type == "message_edit" {
			before = "Previously:\n" + before
		}
		parts = append(parts, before)
	}
	if event.After != "" {
		parts = append(parts, "It now reads:\n"+ui.Quote(ui.PlainText(event.After)))
	}
	if len(event.Attachments) > 0 {
		names := []string{}
		for _, attachment := range event.Attachments {
			names = append(names, ui.PlainText(attachment.Filename))
		}
		parts = append(parts, "Included "+strings.Join(names, ", ")+".")
	}
	if len(event.EmbedTypes) > 0 {
		parts = append(parts, "Included embeds: "+ui.PlainText(strings.Join(event.EmbedTypes, ", "))+".")
	}
	keys := make([]string, 0, len(event.Metadata))
	for key := range event.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s — %s", ui.PlainText(strings.ReplaceAll(key, "_", " ")), ui.PlainText(event.Metadata[key])))
	}
	meta := ""
	if event.MessageID != "" {
		meta = "Message " + ui.PlainText(event.MessageID)
	}
	icon := map[string]string{"message_edit": "edit", "message_delete": "delete", "message_bulk_delete": "delete", "member_join": "join", "member_leave": "leave", "discord_ban": "ban", "discord_unban": "unban", "guild_change": "settings", "channel_change": "settings"}[event.Type]
	if icon == "" {
		icon = "info"
	}
	return ui.Conversation(icon, lead, "", strings.Join(parts, "\n\n"), meta, false)
}
