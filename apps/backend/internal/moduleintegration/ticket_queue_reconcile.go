package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
)

// errTicketQueueVerificationUnavailable keeps transient or denied reads distinct
// from a confirmed mismatched receipt without exposing transport diagnostics.
var errTicketQueueVerificationUnavailable = errors.New("ticket queue message could not be verified")

// ValidateTicketQueueMessage verifies a manually located publication using fresh
// Discord reads only. Its guild, recorded destination, author and controls must
// identify this ticket before core may reconcile the uncertain delivery receipt.
func (c ticketDiscordClient) ValidateTicketQueueMessage(ctx context.Context, ticket *tickets.Ticket, rawURL string) (*tickets.QueueReceipt, error) {
	if ticket == nil || ticket.ID == "" || ticket.LogChannelDiscordID == "" || c.session == nil || c.resolver.db == nil || len(rawURL) > 2048 {
		return nil, tickets.ErrInvalidQueueReceipt
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, tickets.ErrInvalidQueueReceipt
	}
	ref, err := quack.ParseDiscordMessageLink(rawURL)
	if err != nil || ref.ChannelID != ticket.LogChannelDiscordID {
		return nil, tickets.ErrInvalidQueueReceipt
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	guildID, err := c.resolver.discordID(ctx, ticket.GuildID)
	if err != nil {
		return nil, errTicketQueueVerificationUnavailable
	}
	if ref.GuildID != guildID {
		return nil, tickets.ErrInvalidQueueReceipt
	}
	options := []discordgo.RequestOption{discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)}
	message, err := c.session.ChannelMessage(ref.ChannelID, ref.MessageID, options...)
	if err != nil {
		return nil, errTicketQueueVerificationUnavailable
	}
	if message == nil || message.ID != ref.MessageID || message.ChannelID != ref.ChannelID || message.Author == nil || message.WebhookID != "" || (message.GuildID != "" && message.GuildID != guildID) {
		return nil, tickets.ErrInvalidQueueReceipt
	}
	// Message REST responses may omit guild_id, so independently verify the channel
	// rather than treating the user-supplied guild segment as authoritative.
	channel, err := c.session.Channel(ref.ChannelID, options...)
	if err != nil {
		return nil, errTicketQueueVerificationUnavailable
	}
	if channel == nil || channel.ID != ref.ChannelID || channel.GuildID != guildID {
		return nil, tickets.ErrInvalidQueueReceipt
	}
	bot, err := c.session.User("@me", options...)
	if err != nil {
		return nil, errTicketQueueVerificationUnavailable
	}
	if bot == nil || bot.ID == "" || message.Author.ID != bot.ID {
		return nil, tickets.ErrInvalidQueueReceipt
	}
	if !ticketQueueControlsMatch(message.Components, ticket.ID) {
		return nil, tickets.ErrInvalidQueueReceipt
	}
	return &tickets.QueueReceipt{MessageID: message.ID, URL: fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, ref.ChannelID, message.ID)}, nil
}

// ticketQueueControl retains only routing fields from Discord's nested component
// layouts; attachment names and message prose cannot establish ticket identity.
type ticketQueueControl struct {
	CustomID   string               `json:"custom_id"`
	Components []ticketQueueControl `json:"components"`
	Accessory  *ticketQueueControl  `json:"accessory"`
}

// ticketQueueControlsMatch accepts the open and closed queue shapes, requiring a
// view control and rejecting any conflicting or unsupported interactive identity.
func ticketQueueControlsMatch(components []discordgo.MessageComponent, ticketID string) bool {
	raw, err := json.Marshal(components)
	if err != nil {
		return false
	}
	var controls []ticketQueueControl
	if json.Unmarshal(raw, &controls) != nil {
		return false
	}
	foundView := false
	var check func([]ticketQueueControl) bool
	check = func(items []ticketQueueControl) bool {
		for _, item := range items {
			if item.CustomID != "" {
				id, err := ui.DecodeCustomID(item.CustomID)
				if err != nil || id.Namespace != "ticket" || id.Version != "v1" || id.Payload != ticketID || (id.Action != "view" && id.Action != "close") {
					return false
				}
				foundView = foundView || id.Action == "view"
			}
			if !check(item.Components) {
				return false
			}
			if item.Accessory != nil && !check([]ticketQueueControl{*item.Accessory}) {
				return false
			}
		}
		return true
	}
	return check(controls) && foundView
}
