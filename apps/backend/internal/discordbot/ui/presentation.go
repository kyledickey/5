package ui

import (
	"fmt"
	"github.com/quackdiscord/bot/internal/discordtext"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack/model"
)

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

// WithUserAuthor decorates the first card using an already-resolved Discord identity.
// It performs no network request and leaves mention rendering in the card's body.
func WithUserAuthor(message Message, user *discordgo.User) Message {
	if user == nil || len(message.Embeds) == 0 {
		return message
	}
	copy := *message.Embeds[0]
	copy.Author = &discordgo.MessageEmbedAuthor{Name: TruncateRunes(user.Username, EmbedTitleLimit), IconURL: user.AvatarURL("64")}
	message.Embeds = append([]*discordgo.MessageEmbed(nil), message.Embeds...)
	message.Embeds[0] = &copy
	return message
}

// Notice presents a short outcome without redundant headings or decorative timestamps.
func Notice(body string, ephemeral bool) Message {
	return Signal("success", body, ephemeral)
}
