package ui

import (
	"strings"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
)

// Conversation applies the approved text layout while retaining message controls.
func Conversation(icon, lead, quote, detail, meta string, ephemeral bool) Message {
	return Content(discordtext.Conversation(icon, lead, quote, detail, meta), ephemeral)
}

// Signal decorates a short result or an older queued notification exactly once.
func Signal(icon, body string, ephemeral bool) Message {
	return Content(discordtext.WithIcon(icon, body), ephemeral)
}

// Quote renders already-escaped context consistently across commands and logs.
func Quote(body string) string { return discordtext.Quote(body) }

// SessionApplicationID reads the connected bot identity without a profile request.
func SessionApplicationID(session *discordgo.Session) string {
	if session == nil || session.State == nil {
		return ""
	}
	session.State.RLock()
	defer session.State.RUnlock()
	if session.State.User == nil {
		return ""
	}
	return session.State.User.ID
}

// ForApplication resolves the sending bot's icons and keeps long staff records
// complete in an attachment sent to the same authorized destination.
func (m Message) ForApplication(applicationID string) Message {
	m.Content = ResolveCommandMentions(discordtext.Resolve(m.Content, applicationID), applicationID)
	if len(utf16.Encode([]rune(m.Content))) <= 2000 {
		return m
	}
	full := m.Content
	// Prefer complete paragraphs so a link, quote, or emoji is never cut in half.
	paragraphs := strings.Split(full, "\n\n")
	kept := []string{}
	for _, paragraph := range paragraphs {
		candidate := strings.Join(append(append([]string(nil), kept...), paragraph), "\n\n")
		if len(utf16.Encode([]rune(candidate))) > 1750 {
			break
		}
		kept = append(kept, paragraph)
	}
	m.Content = strings.Join(kept, "\n\n")
	if m.Content == "" {
		m.Content = "The full message is attached."
	} else {
		m.Content += "\n\n-# Full details are attached."
	}
	m.Files = append(append([]*discordgo.File(nil), m.Files...), &discordgo.File{Name: "message.txt", ContentType: "text/plain; charset=utf-8", Reader: strings.NewReader(full)})
	return m
}

// ForApplication prepares an edit without changing which fields it replaces.
func (e Edit) ForApplication(applicationID string) Edit {
	if e.Content == nil {
		return e
	}
	m := (Message{Content: *e.Content, Files: e.Files}).ForApplication(applicationID)
	e.Content, e.Files = &m.Content, m.Files
	return e
}

// SendParams prepares a channel or DM message with mention and link-preview suppression.
func (m Message) SendParams(applicationID string) *discordgo.MessageSend {
	m = m.ForApplication(applicationID)
	mentions := m.AllowedMentions
	if mentions == nil {
		mentions = &discordgo.MessageAllowedMentions{}
	}
	return &discordgo.MessageSend{Content: m.Content, Embeds: m.Embeds, Components: m.Components, Files: m.Files, AllowedMentions: mentions, Flags: discordgo.MessageFlagsSuppressEmbeds}
}

// PrepareResponse resolves only message response payloads, preserving modal and
// autocomplete structures as well as deferred acknowledgement visibility.
func PrepareResponse(response *discordgo.InteractionResponse, applicationID string) *discordgo.InteractionResponse {
	if response == nil || response.Data == nil || (response.Type != discordgo.InteractionResponseChannelMessageWithSource && response.Type != discordgo.InteractionResponseUpdateMessage) {
		return response
	}
	result, data := *response, *response.Data
	m := (Message{Content: data.Content, Files: data.Files}).ForApplication(applicationID)
	data.Content, data.Files = m.Content, m.Files
	data.Flags |= discordgo.MessageFlagsSuppressEmbeds
	if data.AllowedMentions == nil {
		data.AllowedMentions = &discordgo.MessageAllowedMentions{}
	}
	result.Data = &data
	return &result
}
