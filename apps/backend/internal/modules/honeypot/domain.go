// Package honeypot implements Quack's optional automated trap-channel module.
package honeypot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/quackdiscord/bot/internal/modules"
)

var (
	// ErrDisabled reports a guild whose honeypot module is not active.
	ErrDisabled = errors.New("honeypot module is disabled")
	// ErrPermissionDenied reports a settings or status operation without Manage Guild.
	ErrPermissionDenied = errors.New("honeypot permission denied")
	// ErrDuplicate reports a gateway replay that has already been claimed.
	ErrDuplicate = errors.New("honeypot message already handled")
	// ErrExempt reports a message deliberately excluded by the safety policy.
	ErrExempt = errors.New("honeypot author is exempt")
	// ErrNotTrigger reports an event outside the configured trap channel or without a human-authored message.
	ErrNotTrigger = errors.New("message does not qualify for honeypot processing")
	// ErrChannelUnavailable reports a deleted or inaccessible trap channel.
	ErrChannelUnavailable = errors.New("honeypot channel is unavailable")
	// ErrTemplateUnavailable reports an archived, missing, or automation-incompatible template.
	ErrTemplateUnavailable = errors.New("honeypot template is unavailable")
)

const (
	// SourceHoneypot is the canonical case source the core case adapter requires.
	SourceHoneypot = "honeypot"
	// ActorTypeSystem represents automation without inventing a staff identity.
	ActorTypeSystem = "system"
)

type Settings struct {
	WarningMessageID string `json:"warning_message_id,omitempty"`
	WarningText      string `json:"warning_text,omitempty"`
	ChannelDiscordID string `json:"channel_discord_id"`
	TemplateID       string `json:"template_id"`
	DisabledReason   string `json:"disabled_reason,omitempty"`
}

type Actor struct {
	GuildID, DiscordUserID string
	CanManage              bool
}

type Message struct {
	GuildID, ChannelDiscordID, MessageDiscordID, AuthorDiscordUserID string
	MessageURL                                                       string
	IsBot, IsQuack, IsWebhook, AuthorCanModerate                     bool
}

// ApplyRequest asks the injected core boundary to run its normal case transaction.
// The adapter must preserve every field and must not write directly to case storage.
type ApplyRequest struct {
	GuildID, TemplateID, TargetDiscordUserID                     string
	ContextChannelDiscordID, ContextMessageDiscordID, ContextURL string
	IdempotencyKey, Source, ActorType, ActorDiscordUserID        string
}

type ApplyResult struct {
	CaseID string
}

type CaseApplier interface {
	ApplyHoneypotCase(context.Context, ApplyRequest) (ApplyResult, error)
}

type TemplateValidator interface {
	ValidateHoneypotTemplate(context.Context, string, string) error
}

type ChannelValidator interface {
	ValidateHoneypotChannel(context.Context, string, string) error
}

type Outcome string

const (
	OutcomePending Outcome = "pending"
	OutcomeCreated Outcome = "created"
	OutcomeFailed  Outcome = "failed"
	OutcomeExempt  Outcome = "exempt"
)

type Statistics struct {
	Total   uint64 `json:"total"`
	Pending uint64 `json:"pending"`
	Created uint64 `json:"created"`
	Failed  uint64 `json:"failed"`
	Exempt  uint64 `json:"exempt"`
}

type Status struct {
	Enabled          bool       `json:"enabled"`
	Configured       bool       `json:"configured"`
	ChannelDiscordID string     `json:"channel_discord_id,omitempty"`
	TemplateID       string     `json:"template_id,omitempty"`
	DisabledReason   string     `json:"disabled_reason,omitempty"`
	Statistics       Statistics `json:"statistics"`
}

func Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: modules.Honeypots, DisplayName: "Honeypots", Validate: validateSettingsJSON}
}

func validateSettingsJSON(raw string) error {
	var settings Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return err
	}
	return validateSettings(settings, false)
}

func validateSettings(settings Settings, enabled bool) error {
	settings.ChannelDiscordID = strings.TrimSpace(settings.ChannelDiscordID)
	settings.TemplateID = strings.TrimSpace(settings.TemplateID)
	if enabled && (settings.ChannelDiscordID == "" || settings.TemplateID == "") {
		return errors.New("enabled honeypots require a channel and active template")
	}
	return nil
}

// ValidateEnabledConfiguration re-runs the enabled-state invariants without
// writing configuration, for the core settings enablement hook.
func ValidateEnabledConfiguration(raw string) error {
	var settings Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return err
	}
	return validateSettings(settings, true)
}
