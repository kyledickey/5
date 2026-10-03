package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	discordadapter "github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
)

// PublishTicketQueue updates the original queue message, attaching the preserved
// transcript on closure. It verifies current bot delivery permissions before every send.
func (c ticketDiscordClient) PublishTicketQueue(ctx context.Context, ticket *tickets.Ticket, settings tickets.Settings, transcript *tickets.Transcript) (*tickets.QueueReceipt, error) {
	guildID, err := c.resolver.discordID(ctx, ticket.GuildID)
	if err != nil {
		return nil, errors.Join(tickets.ErrQueueNotSent, err)
	}
	if err := (&discordadapter.Bot{Session: c.session}).ValidateStaffChannel(ctx, guildID, settings.QueueChannelDiscordID); err != nil {
		return nil, errors.Join(tickets.ErrQueueNotSent, err)
	}
	content := fmt.Sprintf("<@%s> opened a ticket: <#%s>.", ticket.OwnerDiscordUserID, ticket.ThreadDiscordChannelID)
	message := ui.Signal("ticket", content, false)
	message.Components = []discordgo.MessageComponent{ui.Row(
		ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "view", Version: "v1", Payload: ticket.ID}), "Recovery", discordgo.SecondaryButton, false),
		ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "close", Version: "v1", Payload: ticket.ID}), "Close", discordgo.DangerButton, false),
	)}
	if transcript != nil {
		message.Content = fmt.Sprintf("{{quack:ticket}} The ticket for <@%s> was closed. The transcript is attached.", ticket.OwnerDiscordUserID)
		message.Components = []discordgo.MessageComponent{ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "view", Version: "v1", Payload: ticket.ID}), "Recovery", discordgo.SecondaryButton, false))}
		message.Files = []*discordgo.File{{Name: "ticket-" + ticket.ID + ".txt", ContentType: "text/plain; charset=utf-8", Reader: strings.NewReader(transcript.Content)}}
	}
	payload := message.SendParams(ui.SessionApplicationID(c.session))
	var sent *discordgo.Message
	if ticket.LogMessageDiscordID != "" && ticket.LogChannelDiscordID == settings.QueueChannelDiscordID {
		edit := &discordgo.MessageEdit{ID: ticket.LogMessageDiscordID, Channel: settings.QueueChannelDiscordID, Content: &payload.Content, Components: &payload.Components, Embeds: &payload.Embeds, Files: payload.Files, AllowedMentions: payload.AllowedMentions}
		if transcript != nil {
			// Files alone append to an existing post. Retain only this request's
			// files[0] upload so adopted receipts and retries replace older copies.
			edit.Attachments = &[]*discordgo.MessageAttachment{{ID: "0", Filename: payload.Files[0].Name}}
		}
		sent, err = c.session.ChannelMessageEditComplex(edit, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	} else {
		sent, err = c.session.ChannelMessageSendComplex(settings.QueueChannelDiscordID, payload, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	}
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownMessage {
		return nil, tickets.ErrQueueMessageMissing
	}
	if err != nil {
		return nil, ticketQueueSendError(err)
	}
	if sent == nil || sent.ID == "" {
		return nil, errors.New("ticket queue message was not returned")
	}
	if transcript != nil && len(sent.Attachments) == 0 {
		return nil, errors.New("ticket transcript attachment was not returned")
	}
	return &tickets.QueueReceipt{MessageID: sent.ID, URL: fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, settings.QueueChannelDiscordID, sent.ID)}, nil
}

// TicketQueueMessageExists verifies a saved queue receipt without mutating it.
// Only Discord's explicit missing-resource responses permit replacement; access
// failures and network uncertainty must keep the source conversation intact.
func (c ticketDiscordClient) TicketQueueMessageExists(ctx context.Context, channelID, messageID string) (bool, error) {
	_, err := c.session.ChannelMessage(channelID, messageID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil && (rest.Message.Code == discordgo.ErrCodeUnknownMessage || rest.Message.Code == discordgo.ErrCodeUnknownChannel) {
		return false, nil
	}
	return err == nil, err
}

// ticketQueueSendError distinguishes definite Discord rejection from a send that
// may have committed despite a missing response. Only definite rejection is safe
// to retry when the ticket has no message receipt.
func ticketQueueSendError(err error) error {
	var limit *discordgo.RateLimitError
	if errors.As(err, &limit) {
		return errors.Join(tickets.ErrQueueNotSent, err)
	}
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Response != nil {
		switch rest.Response.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
			return errors.Join(tickets.ErrQueueNotSent, err)
		}
	}
	return err
}

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

// DeliverTicketCloseNotice sends the member's own transcript with a server and
// ticket reference. A retry after uncertain delivery searches bot-authored DMs
// instead of risking another notice. No staff-channel link is exposed to members.
func (c ticketDiscordClient) DeliverTicketCloseNotice(ctx context.Context, ticket *tickets.Ticket, transcript *tickets.Transcript, reconcileOnly bool) (string, error) {
	options := []discordgo.RequestOption{discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)}
	channel, err := c.session.UserChannelCreate(ticket.OwnerDiscordUserID, options...)
	if err != nil || channel == nil {
		if err == nil {
			err = errors.New("member DM channel unavailable")
		}
		if reconcileOnly {
			return "", err
		}
		return "", errors.Join(tickets.ErrCloseNoticeNotSent, err)
	}
	filename := "ticket-" + ticket.ID + ".txt"
	if reconcileOnly {
		before := ""
		for {
			messages, err := c.session.ChannelMessages(channel.ID, 100, before, "", "", options...)
			if err != nil {
				return "", err
			}
			for _, message := range messages {
				if message.Author != nil && message.Author.ID == ui.SessionApplicationID(c.session) && strings.Contains(message.Content, "Ticket "+ticket.ID) {
					for _, attachment := range message.Attachments {
						if attachment.Filename == filename {
							return message.ID, nil
						}
					}
				}
			}
			if len(messages) < 100 {
				break
			}
			last := messages[len(messages)-1]
			if ticket.ResolvedAt != nil && last.Timestamp.Before(*ticket.ResolvedAt) {
				break
			}
			before = last.ID
		}
		return "", errors.New("member close notice delivery is unconfirmed")
	}
	guildID, err := c.resolver.discordID(ctx, ticket.GuildID)
	if err != nil {
		return "", errors.Join(tickets.ErrCloseNoticeNotSent, err)
	}
	guild, err := c.session.State.Guild(guildID)
	if err != nil {
		guild, err = c.session.Guild(guildID, options...)
	}
	if err != nil || guild == nil {
		return "", errors.Join(tickets.ErrCloseNoticeNotSent, errors.New("ticket server name unavailable"), err)
	}
	payload := &discordgo.MessageSend{
		Content:         fmt.Sprintf("Your ticket in **%s** is closed. Here’s a copy of the conversation for your records.\n-# Ticket %s", ui.PlainText(guild.Name), ticket.ID),
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Files:           []*discordgo.File{{Name: filename, ContentType: "text/plain; charset=utf-8", Reader: strings.NewReader(transcript.Content)}},
	}
	message, err := c.session.ChannelMessageSendComplex(channel.ID, payload, options...)
	if err != nil {
		if errors.Is(ticketQueueSendError(err), tickets.ErrQueueNotSent) {
			return "", errors.Join(tickets.ErrCloseNoticeNotSent, err)
		}
		return "", err
	}
	if message == nil || message.ID == "" || len(message.Attachments) == 0 {
		return "", errors.New("member transcript receipt unavailable")
	}
	return message.ID, nil
}
