package moduleintegration

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// publishTicketEntry edits the existing panel when setup keeps the same channel.
// Moving the entry first retires the old panel. A failed retirement keeps its
// saved reference available for retry and prevents another active panel appearing.
// Only an explicit missing-message response permits falling back to a new send.
func (c ticketDiscordClient) publishTicketEntry(ctx context.Context, settings tickets.Settings) (*discordgo.Message, error) {
	if settings.EntryPanelMessageID != "" && settings.EntryPanelChannelID != "" && settings.EntryPanelChannelID != settings.EntryChannelDiscordID {
		if err := c.retireTicketEntry(ctx, settings); err != nil {
			return nil, err
		}
	}
	message := ui.Message{Content: "# Need a hand?\nTalk privately with the mod team. Open a ticket and tell us what’s going on."}
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

// retireTicketEntry replaces a moved panel with a pointer to the new entry and
// removes all controls. It is safe to repeat after a partial setup failure. A
// deleted message or channel already satisfies retirement; permission and network
// failures remain visible so setup cannot lose the old panel's recovery reference.
func (c ticketDiscordClient) retireTicketEntry(ctx context.Context, settings tickets.Settings) error {
	content := fmt.Sprintf("Tickets have moved to <#%s>. Open a ticket there.", settings.EntryChannelDiscordID)
	components := []discordgo.MessageComponent{}
	_, err := c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID: settings.EntryPanelMessageID, Channel: settings.EntryPanelChannelID,
		Content: &content, Components: &components, AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil && (rest.Message.Code == discordgo.ErrCodeUnknownMessage || rest.Message.Code == discordgo.ErrCodeUnknownChannel) {
		return nil
	}
	return err
}
