// Package discordtext formats bot-owned prose without depending on Discord's
// transport types. The sending adapter resolves icons for its own application.
package discordtext

import (
	"regexp"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// iconPattern recognizes only product-owned placeholders, never arbitrary emoji IDs.
var iconPattern = regexp.MustCompile(`\{\{quack:([a-z_]+)\}\}`)

// Icon defers emoji selection until the sending application's identity is known.
func Icon(key string) string { return "{{quack:" + key + "}}" }

// Resolve uses application-owned emoji IDs; unconfigured bots retain readable prose.
func Resolve(content, applicationID string) string {
	return strings.TrimSpace(iconPattern.ReplaceAllStringFunc(content, func(marker string) string {
		key := strings.TrimSuffix(strings.TrimPrefix(marker, "{{quack:"), "}}")
		return applicationIcons[applicationID][key]
	}))
}

// Plain escapes untrusted text before it is placed in product-owned Markdown.
// Mentions are additionally disabled by each sending adapter.
func Plain(value string) string {
	return strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`", "~", "\\~", "|", "\\|", ">", "\\>", "[", "\\[", "]", "\\]", "#", "\\#", "{", "\\{", "}", "\\}").Replace(value)
}

// Quote keeps every line of already-escaped contextual prose within one quote.
func Quote(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "> " + strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\n", "\n> ")
}

// Conversation leads with an outcome, optionally quotes context, then gives next
// steps and a quiet reference line. Callers escape member-controlled values.
func Conversation(icon, lead, quote, detail, meta string) string {
	parts := []string{strings.TrimSpace(Icon(icon) + " " + lead)}
	if quote = Quote(quote); quote != "" {
		parts = append(parts, quote)
	}
	if strings.TrimSpace(detail) != "" {
		parts = append(parts, detail)
	}
	body := strings.Join(parts, "\n\n")
	if strings.TrimSpace(meta) != "" {
		body += "\n-# " + strings.ReplaceAll(meta, "\n", " · ")
	}
	return body
}

// WithIcon decorates older queued notices without doubling a rendered message's icon.
func WithIcon(key, body string) string {
	if strings.HasPrefix(body, "{{quack:") {
		return body
	}
	return Icon(key) + " " + body
}

// ActionSentence makes enforcement status explicit without exposing internal enum names.
func ActionSentence(action model.ActionType, status model.ActionExecutionStatus) string {
	name := action.Label()
	switch status {
	case model.ActionExecutionSucceeded:
		switch action {
		case model.ActionTimeoutUser:
			return "Timeout applied."
		case model.ActionKickUser:
			return "Member kicked."
		case model.ActionBanUser:
			return "Member banned."
		case model.ActionRemoveTimeout:
			return "Member is no longer timed out."
		case model.ActionUnbanUser:
			return "Member is no longer banned."
		case model.ActionSendDM:
			return "Message sent."
		}
		return name + " completed."
	case model.ActionExecutionPending:
		return name + " queued."
	case model.ActionExecutionRunning:
		return name + " in progress."
	case model.ActionExecutionRetrying:
		return name + " will be retried."
	case model.ActionExecutionFailed:
		return name + " couldn’t be completed. Staff review needed."
	case model.ActionExecutionSkipped:
		return name + " skipped."
	case model.ActionExecutionCancelled:
		return name + " cancelled."
	default:
		return name + " status is unavailable."
	}
}
