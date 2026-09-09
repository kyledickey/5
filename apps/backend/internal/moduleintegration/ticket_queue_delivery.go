package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bwmarrin/discordgo"
	discordadapter "github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
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
