package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	discordadapter "github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// PublishTicketQueue updates the original queue message, attaching the preserved
// transcript on closure. It verifies current destination privacy before every send.
func (c ticketDiscordClient) PublishTicketQueue(ctx context.Context, ticket *tickets.Ticket, settings tickets.Settings, transcript *tickets.Transcript) (*tickets.QueueReceipt, error) {
	guildID, err := c.resolver.discordID(ctx, ticket.GuildID)
	if err != nil {
		return nil, err
	}
	if err := (&discordadapter.Bot{Session: c.session}).ValidateStaffChannel(ctx, guildID, settings.QueueChannelDiscordID); err != nil {
		return nil, err
	}
	content := fmt.Sprintf("<@%s> opened a ticket: <#%s>.", ticket.OwnerDiscordUserID, ticket.ThreadDiscordChannelID)
	message := ui.Signal("ticket", content, false)
	message.Components = []discordgo.MessageComponent{ui.Row(
		ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "view", Version: "v1", Payload: ticket.ID}), "Join thread", discordgo.PrimaryButton, false),
		ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "close", Version: "v1", Payload: ticket.ID}), "Close", discordgo.DangerButton, false),
	)}
	if transcript != nil {
		message.Content = fmt.Sprintf("{{quack:ticket}} The ticket for <@%s> was closed. The transcript is attached.", ticket.OwnerDiscordUserID)
		message.Components = []discordgo.MessageComponent{}
		message.Files = []*discordgo.File{{Name: "ticket-" + ticket.ID + ".txt", ContentType: "text/plain; charset=utf-8", Reader: strings.NewReader(transcript.Content)}}
	}
	payload := message.SendParams(ui.SessionApplicationID(c.session))
	var sent *discordgo.Message
	if ticket.LogMessageDiscordID != "" && ticket.LogChannelDiscordID == settings.QueueChannelDiscordID {
		sent, err = c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: ticket.LogMessageDiscordID, Channel: settings.QueueChannelDiscordID, Content: &payload.Content, Components: &payload.Components, Embeds: &payload.Embeds, Files: payload.Files, AllowedMentions: payload.AllowedMentions}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	} else {
		sent, err = c.session.ChannelMessageSendComplex(settings.QueueChannelDiscordID, payload, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	}
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownMessage {
		// The queue message was deleted; recreate it with a fresh file reader.
		if transcript != nil {
			payload.Files[0].Reader = strings.NewReader(transcript.Content)
		}
		sent, err = c.session.ChannelMessageSendComplex(settings.QueueChannelDiscordID, payload, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	}
	if err != nil {
		return nil, err
	}
	if sent == nil || sent.ID == "" {
		return nil, errors.New("ticket queue message was not returned")
	}
	if transcript != nil && len(sent.Attachments) == 0 {
		return nil, errors.New("ticket transcript attachment was not returned")
	}
	return &tickets.QueueReceipt{MessageID: sent.ID, URL: fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, settings.QueueChannelDiscordID, sent.ID)}, nil
}
