package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Sentinel errors returned by EncodeCustomID and DecodeCustomID. The dispatcher
// answers both with a generic "component not available" reply.
var (
	ErrCustomIDInvalid = errors.New("custom id is invalid")
	ErrCustomIDTooLong = errors.New("custom id exceeds Discord limit")
)

// CustomID is the routing identity embedded in an interactive Discord component
// as "namespace:action:version:payload". Namespace and Action select the handler
// in the component registry; Payload is opaque, handler-owned data (case IDs,
// page numbers) and must never carry authority or member-controlled content.
type CustomID struct {
	Namespace string
	Action    string
	Version   string
	Payload   string
}

// EncodeCustomID renders id as "namespace:action:version:payload". It fails with
// ErrCustomIDInvalid when a routing part is empty or contains ':' and with
// ErrCustomIDTooLong when the result exceeds Discord's 100-character limit.
func EncodeCustomID(id CustomID) (string, error) {
	namespace := strings.TrimSpace(id.Namespace)
	action := strings.TrimSpace(id.Action)
	version := strings.TrimSpace(id.Version)
	if namespace == "" || action == "" || version == "" {
		return "", ErrCustomIDInvalid
	}
	if strings.Contains(namespace, ":") || strings.Contains(action, ":") || strings.Contains(version, ":") {
		return "", ErrCustomIDInvalid
	}

	value := namespace + ":" + action + ":" + version + ":" + strings.TrimSpace(id.Payload)
	if len([]rune(value)) > CustomIDLimit {
		return "", ErrCustomIDTooLong
	}
	return value, nil
}

// DecodeCustomID parses a value produced by EncodeCustomID. Payload may contain
// further ':' characters; only the first three separators are routing.
func DecodeCustomID(value string) (CustomID, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 4)
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return CustomID{}, ErrCustomIDInvalid
	}
	if len([]rune(value)) > CustomIDLimit {
		return CustomID{}, ErrCustomIDTooLong
	}
	return CustomID{
		Namespace: parts[0],
		Action:    parts[1],
		Version:   parts[2],
		Payload:   parts[3],
	}, nil
}

// MustCustomID encodes a component identity and panics only when a programmer-provided ID is invalid.
func MustCustomID(id CustomID) string {
	value, err := EncodeCustomID(id)
	if err != nil {
		panic(fmt.Sprintf("invalid custom id: %v", err))
	}
	return value
}

// Button constructs a routed Discord button using Quack's custom-ID format.
func Button(customID, label string, style discordgo.ButtonStyle, disabled bool) discordgo.Button {
	// Colors distinguish primary actions, confirmations, and destructive controls.
	if style == 0 {
		style = discordgo.SecondaryButton
	}
	return discordgo.Button{
		CustomID: customID,
		Label:    TruncateRunes(label, 80),
		Style:    style,
		Disabled: disabled,
	}
}

// LinkButton constructs a Discord link button, which uses a URL instead of an interaction custom ID.
func LinkButton(url, label string, disabled bool) discordgo.Button {
	return discordgo.Button{
		URL:      url,
		Label:    TruncateRunes(label, 80),
		Style:    discordgo.LinkButton,
		Disabled: disabled,
	}
}

// Row groups components into one action row, silently keeping only the first
// five because Discord rejects larger rows.
func Row(components ...discordgo.MessageComponent) discordgo.ActionsRow {
	if len(components) > 5 {
		components = components[:5]
	}
	return discordgo.ActionsRow{Components: components}
}

// Pagination builds consistent previous and next controls while disabling unavailable directions.
func Pagination(namespace, prefix, payload string, page, totalPages int) ([]discordgo.MessageComponent, error) {
	prevID, err := EncodeCustomID(CustomID{
		Namespace: namespace,
		Action:    prefix + "_prev",
		Version:   "v1",
		Payload:   payload,
	})
	if err != nil {
		return nil, err
	}
	nextID, err := EncodeCustomID(CustomID{
		Namespace: namespace,
		Action:    prefix + "_next",
		Version:   "v1",
		Payload:   payload,
	})
	if err != nil {
		return nil, err
	}

	return []discordgo.MessageComponent{
		Row(
			Button(prevID, "Prev", discordgo.SecondaryButton, page <= 1),
			Button(nextID, "Next", discordgo.PrimaryButton, page >= totalPages),
		),
	}, nil
}
