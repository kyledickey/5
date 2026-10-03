// Package ui owns the Discord response model shared by every command, component
// and worker in the bot: Message and Edit, the handler contract (Context,
// Handler, HandlerResult, Task, Responder), custom-ID routing for components,
// button builders, text pagination, command-mention resolution and SetupChannel.
// It depends on discordgo and discordtext only; it must not import quack
// services, commands, interactions or views.
//
// Response lifecycle. A Handler returns a HandlerResult: Immediate sends one
// InteractionResponse and finishes; Async sends the acknowledgement (DeferPublic,
// DeferEphemeral or DeferUpdate) and then runs a Task on a goroutine with a
// Responder. Discord fixes a reply's visibility at acknowledgement time, so a
// Task cannot make a public defer private. AsyncPublic encodes the rule used by
// every slash command: success edits the original public response in place
// (Publish), while an error marked with ErrorEdit deletes the public placeholder
// and sends one ephemeral followup instead. Errors are never edited into a shared
// message. In DMs the dispatcher strips ephemeral flags because Discord rejects them.
//
// UserError carries copy that is safe to show to the invoking user; every other
// error is internal and must be mapped to copy by the caller.
package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// Message is the transport-neutral body shared by initial responses, followups,
// channel sends and DMs. Content may contain discordtext icon placeholders and
// command references; ForApplication resolves both before anything is sent.
type Message struct {
	Content         string
	Embeds          []*discordgo.MessageEmbed
	Components      []discordgo.MessageComponent
	Files           []*discordgo.File
	Ephemeral       bool
	AllowedMentions *discordgo.MessageAllowedMentions
}

// Edit describes changes to an existing Discord interaction response.
type Edit struct {
	// PrivateError routes failures privately when a command deferred publicly.
	PrivateError    bool
	Content         *string
	Embeds          *[]*discordgo.MessageEmbed
	Components      *[]discordgo.MessageComponent
	Files           []*discordgo.File
	AllowedMentions *discordgo.MessageAllowedMentions
}

// Content constructs a text-only Discord message.
func Content(content string, ephemeral bool) Message {
	return Message{Content: content, Ephemeral: ephemeral}
}

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

// PlainText escapes member-controlled text before placing it inside bot-authored Markdown.
// Mention parsing is suppressed separately by the transport's AllowedMentions policy.
func PlainText(value string) string {
	return discordtext.Plain(value)
}

// RelativeTime uses Discord's localized live timestamp, omitting unknown dates.
func RelativeTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return fmt.Sprintf("<t:%d:R>", value.Unix())
}

// ActionSentence shares enforcement wording with durable member notifications.
func ActionSentence(action model.ActionType, status model.ActionExecutionStatus) string {
	return discordtext.ActionSentence(action, status)
}

// TruncateRunes enforces Discord text limits without splitting a UTF-8 code point.
func TruncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// EditMessage converts a Message into an Edit that replaces content, embeds and
// components (each pointer is set, so previous values are cleared, not merged).
func EditMessage(m Message) Edit {
	content := m.Content
	embeds := append([]*discordgo.MessageEmbed{}, m.Embeds...)
	components := append([]discordgo.MessageComponent{}, m.Components...)
	return Edit{
		Content:         &content,
		Embeds:          &embeds,
		Components:      &components,
		Files:           m.Files,
		AllowedMentions: m.AllowedMentions,
	}
}

// ResponseData converts Message into Discord's initial interaction response payload.
func (m Message) ResponseData() *discordgo.InteractionResponseData {
	data := &discordgo.InteractionResponseData{
		Content:         m.Content,
		Embeds:          m.Embeds,
		Components:      m.Components,
		Files:           m.Files,
		AllowedMentions: m.AllowedMentions,
	}
	if data.AllowedMentions == nil {
		data.AllowedMentions = &discordgo.MessageAllowedMentions{}
	}
	if m.Ephemeral {
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	return data
}

// WebhookParams converts Message into Discord followup parameters.
func (m Message) WebhookParams() *discordgo.WebhookParams {
	params := &discordgo.WebhookParams{
		Content:         m.Content,
		Embeds:          m.Embeds,
		Components:      m.Components,
		Files:           m.Files,
		AllowedMentions: m.AllowedMentions,
	}
	if params.AllowedMentions == nil {
		params.AllowedMentions = &discordgo.MessageAllowedMentions{}
	}
	params.Flags = discordgo.MessageFlagsSuppressEmbeds
	if m.Ephemeral {
		params.Flags |= discordgo.MessageFlagsEphemeral
	}
	return params
}

// WebhookEdit converts Edit into Discord's original-response edit payload.
func (e Edit) WebhookEdit() *discordgo.WebhookEdit {
	edit := &discordgo.WebhookEdit{
		Content:         e.Content,
		Embeds:          e.Embeds,
		Components:      e.Components,
		Files:           e.Files,
		AllowedMentions: e.AllowedMentions,
	}
	// An edit that rewrites the body drops the previous attachments; Discord keeps
	// them unless the request sends an explicit empty attachment list.
	if e.Content != nil {
		attachments := []*discordgo.MessageAttachment{}
		edit.Attachments = &attachments
	}
	if edit.AllowedMentions == nil {
		edit.AllowedMentions = &discordgo.MessageAllowedMentions{}
	}
	return edit
}

// SendParams prepares a channel or DM message with mention and link-preview suppression.
func (m Message) SendParams(applicationID string) *discordgo.MessageSend {
	m = m.ForApplication(applicationID)
	mentions := m.AllowedMentions
	if mentions == nil {
		mentions = &discordgo.MessageAllowedMentions{}
	}
	return &discordgo.MessageSend{
		Content:         m.Content,
		Embeds:          m.Embeds,
		Components:      m.Components,
		Files:           m.Files,
		AllowedMentions: mentions,
		Flags:           discordgo.MessageFlagsSuppressEmbeds,
	}
}

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
	m.Files = append(append([]*discordgo.File(nil), m.Files...), &discordgo.File{
		Name:        "message.txt",
		ContentType: "text/plain; charset=utf-8",
		Reader:      strings.NewReader(full),
	})
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

// PrepareResponse resolves only message response payloads, preserving modal and
// autocomplete structures as well as deferred acknowledgement visibility.
func PrepareResponse(response *discordgo.InteractionResponse, applicationID string) *discordgo.InteractionResponse {
	if response == nil || response.Data == nil {
		return response
	}
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource &&
		response.Type != discordgo.InteractionResponseUpdateMessage {
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

// pageLink matches the single-line Markdown links emitted by Quack's record views.
var pageLink = regexp.MustCompile(`\[[^\n]*?\]\([^\n]*?\)`)

// TextPages splits rendered text into lossless UTF-16-bounded pages. It prefers
// whole lines, then spaces, and only splits an uninterrupted line when necessary.
// Callers reserve room for their heading and controls before choosing the limit.
func TextPages(text string, limit int) []string {
	if limit < 2 {
		panic("text page limit must accommodate a Unicode character")
	}
	if text == "" {
		return []string{""}
	}
	var pages []string
	for text != "" {
		units, end := 0, len(text)
		for index, char := range text {
			width := 1
			if char > 0xffff {
				width = 2
			}
			if units+width > limit {
				end = index
				break
			}
			units += width
		}
		if end < len(text) {
			if split := strings.LastIndexByte(text[:end], '\n'); split >= 0 {
				end = split + 1
			} else if split := strings.LastIndexByte(text[:end], ' '); split >= 0 {
				end = split + 1
			}
			// Move a link to the next page rather than cutting its label or URL.
			// A single link longer than the entire budget still has to be split.
			for _, link := range pageLink.FindAllStringIndex(text, -1) {
				if link[0] >= end {
					break
				}
				if link[1] > end {
					if link[0] > 0 {
						end = link[0]
					} else if len(utf16.Encode([]rune(text[:link[1]]))) <= limit {
						end = link[1]
					}
					break
				}
			}
		}
		pages = append(pages, text[:end])
		text = text[end:]
	}
	return pages
}
