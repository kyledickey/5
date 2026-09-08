package commands

import (
	"net/url"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// caseWebLink adds an optional staff dashboard destination without replacing
// native controls or changing response visibility. Callers supply the resolved
// Discord guild ID and record identifiers after the normal authorization check.
// Evidence lives on the existing case page; no evidence URLs or context enter
// the destination. Dashboard authorization remains independent of Discord links.
func caseWebLink(message ui.Message, base, guildID, resource, recordID string) ui.Message {
	if base == "" || len(message.Components) >= 5 {
		return message
	}
	destination, err := url.Parse(base)
	if err != nil || destination.Scheme != "https" || destination.Hostname() == "" || destination.User != nil || destination.RawQuery != "" || destination.ForceQuery || destination.Fragment != "" || destination.Opaque != "" {
		return message
	}
	if !webRecordSegment(guildID) || (recordID != "" && !webRecordSegment(recordID)) || (resource != "cases" && resource != "members") || (resource == "members" && recordID == "") {
		return message
	}
	destination.Path = strings.TrimRight(destination.Path, "/") + "/guilds/" + guildID + "/" + resource
	if recordID != "" {
		destination.Path += "/" + recordID
	}
	destination.RawPath = ""
	if len(destination.String()) > 512 {
		return message
	}
	components := append([]discordgo.MessageComponent(nil), message.Components...)
	message.Components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{ui.LinkButton(destination.String(), "Open on web", false)}})
	return message
}

// webRecordSegment restricts destinations to opaque identifiers, excluding URL
// syntax and traversal even if an interaction payload was manually constructed.
func webRecordSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
}
