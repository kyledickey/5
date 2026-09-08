package moduleintegration

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// publishTicketEntry edits the existing panel when setup keeps the same channel.
// Only an explicit missing-message response permits falling back to a new send.
func (c ticketDiscordClient) publishTicketEntry(ctx context.Context, settings tickets.Settings) (*discordgo.Message, error) {
	message := ui.Signal("ticket", "Need to talk to a moderator? Open a private ticket below.", false)
	message.Components = []discordgo.MessageComponent{ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "open", Version: "v1"}), "Open ticket", discordgo.PrimaryButton, false))}
	payload := message.SendParams(ui.SessionApplicationID(c.session))
	var sent *discordgo.Message
	var err error
	if settings.EntryPanelMessageID != "" && settings.EntryPanelChannelID == settings.EntryChannelDiscordID {
		sent, err = c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: settings.EntryPanelMessageID, Channel: settings.EntryChannelDiscordID, Content: &payload.Content, Components: &payload.Components, AllowedMentions: payload.AllowedMentions}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		var rest *discordgo.RESTError
		if err != nil && !(errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownMessage) {
			return nil, err
		}
	}
	if sent == nil {
		sent, err = c.session.ChannelMessageSendComplex(settings.EntryChannelDiscordID, payload, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	}
	if err != nil {
		return nil, err
	}
	if sent == nil || sent.ID == "" {
		return nil, errors.New("Discord did not return the entry panel message")
	}
	return sent, nil
}
