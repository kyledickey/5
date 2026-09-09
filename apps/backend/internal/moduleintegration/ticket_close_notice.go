package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

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
