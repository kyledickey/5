package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

// honeypotCounter updates the existing warning from durable incident counts.
// Per-guild serialization prevents a slower update from overwriting a newer count.
// Other guilds remain independent; deleted warnings are recreated from saved text.
type honeypotCounter struct {
	session    *discordgo.Session
	service    *honeypot.Service
	resolver   guildResolver
	guildLocks sync.Map
}

// IncidentCreated refreshes the current configured warning after case persistence.
// Delivery is presentation only and never writes another staff audit event.
func (c *honeypotCounter) IncidentCreated(ctx context.Context, guildID string) error {
	value, _ := c.guildLocks.LoadOrStore(guildID, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	settings, status, err := c.service.Settings(ctx, honeypot.Actor{GuildID: guildID, CanManage: true})
	if err != nil {
		return err
	}
	if !status.Enabled || settings.ChannelDiscordID == "" {
		return nil
	}
	discordGuildID, err := c.resolver.discordID(ctx, guildID)
	if err != nil {
		return err
	}
	channel, err := c.session.Channel(settings.ChannelDiscordID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return err
	}
	if channel == nil || channel.GuildID != discordGuildID {
		return errors.New("honeypot counter destination is outside the guild")
	}
	content := honeypotWarningContent(settings.WarningText, status.Statistics.Created)
	if settings.WarningMessageID != "" {
		_, err = c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: settings.WarningMessageID, Channel: channel.ID, Content: &content, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err == nil {
			return nil
		}
		var rest *discordgo.RESTError
		if !errors.As(err, &rest) || rest.Message == nil || rest.Message.Code != discordgo.ErrCodeUnknownMessage {
			return err
		}
	}
	message, err := c.session.ChannelMessageSendComplex(channel.ID, ui.Message{Content: content}.SendParams(ui.SessionApplicationID(c.session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return err
	}
	if message == nil || message.ID == "" {
		return errors.New("honeypot warning delivery returned no message")
	}
	return c.service.RecordWarningReplacement(ctx, guildID, settings, message.ID)
}

const defaultHoneypotWarning = "# Warning!\nThis channel catches spam and scam accounts. Do not post here. Posting here triggers this server's honeypot moderation rule."

// honeypotWarningContent uses incident counts because editable policies may not ban.
func honeypotWarningContent(warning string, count uint64) string {
	if strings.TrimSpace(warning) == "" {
		warning = defaultHoneypotWarning
	}
	return fmt.Sprintf("%s\n\n-# %d incidents caught.", warning, count)
}
