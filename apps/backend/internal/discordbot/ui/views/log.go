package views

import (
	"encoding/json"
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
	target := "A member"
	if event.Metadata["target_id"] != "" {
		target = "<@" + event.Metadata["target_id"] + ">"
	}
	var lead string
	switch event.Type {
	case "message_edit":
		lead = "A message from " + actor + " was edited."
	case "message_delete":
		lead = "A message from " + actor + " was deleted."
	case "message_bulk_delete":
		lead = "Messages were deleted."
		if count := event.Metadata["message_count"]; count != "" {
			lead = ui.PlainText(count) + " messages were deleted."
		}
	case "member_join":
		lead = actor + " joined the server."
	case "member_leave":
		lead = actor + " left the server."
	case "discord_ban":
		lead = target + " was banned by " + actor + "."
	case "discord_unban":
		lead = target + " was unbanned by " + actor + "."
	case "guild_change":
		lead = "Server settings changed."
	case "channel_change":
		lead = "A channel changed."
		switch event.Metadata["operation"] {
		case "created":
			lead = "A channel was created."
		case "deleted":
			lead = "A channel was deleted."
		case "updated":
			lead = "Channel settings changed."
		}
		if name := event.Metadata["name"]; name != "" {
			lead = strings.TrimSuffix(lead, ".") + ": " + ui.PlainText(name) + "."
		}
	default:
		lead = "Server activity was recorded."
	}
	if event.ChannelID != "" && event.Type != "channel_change" {
		lead = strings.TrimSuffix(lead, ".") + " in <#" + event.ChannelID + ">."
	}
	parts := []string{}
	if event.Before != "" {
		before := ui.Quote(ui.PlainText(event.Before))
		if event.Type == "message_edit" {
			before = "Before:\n" + before
		}
		parts = append(parts, before)
	}
	if event.After != "" {
		parts = append(parts, "After:\n"+ui.Quote(ui.PlainText(event.After)))
	}
	if len(event.Attachments) > 0 {
		names := []string{}
		for _, attachment := range event.Attachments {
			names = append(names, ui.PlainText(attachment.Filename))
		}
		parts = append(parts, "Files: "+strings.Join(names, ", "))
	}
	if len(event.EmbedTypes) > 0 {
		parts = append(parts, "Included embeds: "+ui.PlainText(strings.Join(event.EmbedTypes, ", "))+".")
	}
	if reason := event.Metadata["reason"]; reason != "" {
		parts = append(parts, "Reason:\n"+ui.Quote(ui.PlainText(reason)))
	}
	if event.Type == "guild_change" && event.Metadata["name"] != "" {
		parts = append(parts, "Server: "+ui.PlainText(event.Metadata["name"]))
	}
	if event.Type == "message_bulk_delete" && event.Metadata["cached_count"] != "" {
		parts = append(parts, "Messages with saved content: "+ui.PlainText(event.Metadata["cached_count"])+".")
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
