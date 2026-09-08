package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

// honeypotCounter updates the existing warning from durable incident counts.
// Per-guild serialization prevents a slower update from overwriting a newer count.
// Other guilds remain independent; setup owns missing-message repair.
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
	if !status.Enabled || settings.WarningMessageID == "" || settings.ChannelDiscordID == "" {
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
	_, err = c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: settings.WarningMessageID, Channel: channel.ID, Content: &content, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// honeypotWarningContent uses incident counts because editable policies may not ban.
func honeypotWarningContent(warning string, count uint64) string {
	return fmt.Sprintf("%s\n\n-# %d incidents caught.", warning, count)
}
