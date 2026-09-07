package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	EmbedTitleLimit       = 256
	EmbedDescriptionLimit = 4096
	EmbedFieldNameLimit   = 256
	EmbedFieldValueLimit  = 1024
	EmbedFieldLimit       = 25
	EmbedFooterLimit      = 2048
	CustomIDLimit         = 100
)

const (
	ColorMain    = 0xE5AA2C
	ColorSuccess = ColorMain
	ColorWarning = ColorMain
	ColorError   = 0xED4245
	ColorMuted   = ColorMain
)

// Message is the package-owned Discord response model used for both initial responses and followups.
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
	Content         *string
	Embeds          *[]*discordgo.MessageEmbed
	Components      *[]discordgo.MessageComponent
	Files           []*discordgo.File
	AllowedMentions *discordgo.MessageAllowedMentions
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
	if e.Content != nil {
		attachments := []*discordgo.MessageAttachment{}
		edit.Attachments = &attachments
	}
	if edit.AllowedMentions == nil {
		edit.AllowedMentions = &discordgo.MessageAllowedMentions{}
	}
	return edit
}

// EditMessage converts edit message into its transport presentation without leaking transport types into the core.
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

// Content constructs a text-only Discord message.
func Content(content string, ephemeral bool) Message {
	return Message{Content: content, Ephemeral: ephemeral}
}

// WithEmbeds returns a copy of a message with the supplied embeds, preserving value-style composition.
func WithEmbeds(embeds ...*discordgo.MessageEmbed) Message {
	return Message{Embeds: embeds}
}

// EmbedMessage converts embed message into its transport presentation without leaking transport types into the core.
func EmbedMessage(embed *discordgo.MessageEmbed, ephemeral bool) Message {
	return Message{Embeds: []*discordgo.MessageEmbed{embed}, Ephemeral: ephemeral}
}

// EmbedsMessage converts embeds message into its transport presentation without leaking transport types into the core.
func EmbedsMessage(ephemeral bool, embeds ...*discordgo.MessageEmbed) Message {
	return Message{Embeds: embeds, Ephemeral: ephemeral}
}

// SuccessEmbed converts success embed into its transport presentation without leaking transport types into the core.
func SuccessEmbed(title, description string) *discordgo.MessageEmbed {
	return NewEmbed().SetTitle(title).SetDescription(description).SetColor(ColorSuccess).Build()
}

// ErrorEmbed converts error embed into its transport presentation without leaking transport types into the core.
func ErrorEmbed(description string) *discordgo.MessageEmbed {
	return NewEmbed().SetTitle("Couldn’t do that").SetDescription(description).SetColor(ColorError).Build()
}

// WarningEmbed converts warning embed into its transport presentation without leaking transport types into the core.
func WarningEmbed(title, description string) *discordgo.MessageEmbed {
	return NewEmbed().SetTitle(title).SetDescription(description).SetColor(ColorWarning).Build()
}

// InfoEmbed converts info embed into its transport presentation without leaking transport types into the core.
func InfoEmbed(title, description string) *discordgo.MessageEmbed {
	return NewEmbed().SetTitle(title).SetDescription(description).SetColor(ColorMain).Build()
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

// Embed is a fluent builder that centralizes Quack's Discord embed formatting rules.
type Embed struct {
	embed *discordgo.MessageEmbed
}

// NewEmbed constructs embed with required dependencies explicit so callers control lifecycle and substitution.
func NewEmbed() *Embed {
	return &Embed{embed: &discordgo.MessageEmbed{Color: ColorMain}}
}

// NewInfoEmbed constructs info embed with required dependencies explicit so callers control lifecycle and substitution.
func NewInfoEmbed(title, description string) *Embed {
	return NewEmbed().SetTitle(title).SetDescription(description).SetColor(ColorMain)
}

// NewSuccessEmbed constructs success embed with required dependencies explicit so callers control lifecycle and substitution.
func NewSuccessEmbed(title, description string) *Embed {
	return NewEmbed().SetTitle(title).SetDescription(description).SetColor(ColorSuccess)
}

// NewErrorEmbed constructs error embed with required dependencies explicit so callers control lifecycle and substitution.
func NewErrorEmbed(description string) *Embed {
	return NewEmbed().SetTitle("Couldn’t do that").SetDescription(description).SetColor(ColorError)
}

// SetTitle encapsulates the set title rule so callers share one consistent package implementation.
func (e *Embed) SetTitle(title string) *Embed {
	e.embed.Title = TruncateRunes(strings.TrimSpace(title), EmbedTitleLimit)
	return e
}

// SetDescription encapsulates the set description rule so callers share one consistent package implementation.
func (e *Embed) SetDescription(description string) *Embed {
	e.embed.Description = TruncateRunes(description, EmbedDescriptionLimit)
	return e
}

// AddField encapsulates the add field rule so callers share one consistent package implementation.
func (e *Embed) AddField(name string, value any, inline bool) *Embed {
	if len(e.embed.Fields) >= EmbedFieldLimit {
		return e
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "\u200b"
	}
	fieldValue := fmt.Sprint(value)
	if strings.TrimSpace(fieldValue) == "" {
		fieldValue = "\u200b"
	}
	e.embed.Fields = append(e.embed.Fields, &discordgo.MessageEmbedField{
		Name:   TruncateRunes(name, EmbedFieldNameLimit),
		Value:  TruncateRunes(fieldValue, EmbedFieldValueLimit),
		Inline: inline,
	})
	return e
}

// AddFields encapsulates the add fields rule so callers share one consistent package implementation.
func (e *Embed) AddFields(fields ...*discordgo.MessageEmbedField) *Embed {
	for _, field := range fields {
		if field == nil {
			continue
		}
		e.AddField(field.Name, field.Value, field.Inline)
	}
	return e
}

// SetFooter encapsulates the set footer rule so callers share one consistent package implementation.
func (e *Embed) SetFooter(text string) *Embed {
	e.embed.Footer = &discordgo.MessageEmbedFooter{Text: TruncateRunes(text, EmbedFooterLimit)}
	return e
}

// SetAuthor encapsulates the set author rule so callers share one consistent package implementation.
func (e *Embed) SetAuthor(name, iconURL string) *Embed {
	e.embed.Author = &discordgo.MessageEmbedAuthor{
		Name:    TruncateRunes(strings.TrimSpace(name), EmbedTitleLimit),
		IconURL: strings.TrimSpace(iconURL),
	}
	return e
}

// SetThumbnail encapsulates the set thumbnail rule so callers share one consistent package implementation.
func (e *Embed) SetThumbnail(url string) *Embed {
	e.embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: strings.TrimSpace(url)}
	return e
}

// SetTimestamp encapsulates the set timestamp rule so callers share one consistent package implementation.
func (e *Embed) SetTimestamp(t time.Time) *Embed {
	if t.IsZero() {
		t = time.Now()
	}
	e.embed.Timestamp = t.UTC().Format(time.RFC3339)
	return e
}

// SetColor encapsulates the set color rule so callers share one consistent package implementation.
func (e *Embed) SetColor(color int) *Embed {
	e.embed.Color = color
	return e
}

// SetNamedColor encapsulates the set named color rule so callers share one consistent package implementation.
func (e *Embed) SetNamedColor(name string) *Embed {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "success", "green":
		return e.SetColor(ColorSuccess)
	case "warning", "yellow":
		return e.SetColor(ColorWarning)
	case "error", "red", "danger":
		return e.SetColor(ColorError)
	case "muted", "gray", "grey":
		return e.SetColor(ColorMuted)
	default:
		return e.SetColor(ColorMain)
	}
}

// Field encapsulates the field rule so callers share one consistent package implementation.
func Field(name string, value any, inline bool) *discordgo.MessageEmbedField {
	return &discordgo.MessageEmbedField{
		Name:   fmt.Sprint(name),
		Value:  fmt.Sprint(value),
		Inline: inline,
	}
}

// Build bounds the aggregate card text as well as each field. Discord rejects
// an entire message above 6000 characters, so optional trailing details yield first.
func (e *Embed) Build() *discordgo.MessageEmbed {
	result := *e.embed
	remaining := 6000 - len([]rune(result.Title))
	if result.Author != nil {
		remaining -= len([]rune(result.Author.Name))
	}
	if result.Footer != nil {
		remaining -= len([]rune(result.Footer.Text))
	}
	result.Description = TruncateRunes(result.Description, remaining)
	remaining -= len([]rune(result.Description))
	result.Fields = nil
	for _, field := range e.embed.Fields {
		cost := len([]rune(field.Name))
		if remaining <= cost {
			break
		}
		copy := *field
		copy.Value = TruncateRunes(copy.Value, remaining-cost)
		result.Fields = append(result.Fields, &copy)
		remaining -= cost + len([]rune(copy.Value))
	}
	return &result
}
