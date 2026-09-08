package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

// honeypotCounter updates the existing warning from durable incident counts.
// Per-guild serialization prevents a slower update from overwriting a newer count.
// Other guilds remain independent; deleted warnings are recreated from saved text.
type honeypotCounter struct {
	session     *discordgo.Session
	service     *honeypot.Service
	resolver    guildResolver
	guildLocks  sync.Map
	sharedLocks *sync.Map
}

// IncidentCreated schedules the current warning after case persistence.
// Delivery is presentation only and never writes another staff audit event.
func (c *honeypotCounter) IncidentCreated(ctx context.Context, guildID string) error {
	return c.service.RequestWarningRefresh(ctx, guildID)
}

// WarningDeleted schedules only the currently configured warning, including bulk
// deletions. Stale duplicate events cannot create another replacement.
func (c *honeypotCounter) WarningDeleted(ctx context.Context, guildID, channelID string, messageIDs []string) error {
	if channelID == "" || len(messageIDs) == 0 {
		return nil
	}
	settings, _, err := c.service.Settings(ctx, honeypot.Actor{GuildID: guildID, CanManage: true})
	if err != nil {
		return err
	}
	if settings.ChannelDiscordID != channelID || !slices.Contains(messageIDs, settings.WarningMessageID) {
		return nil
	}
	return c.service.RequestWarningRefresh(ctx, guildID)
}

// refresh reloads current settings under the shared presentation lock before
// validating the destination and updating or repairing its warning.
func (c *honeypotCounter) refresh(ctx context.Context, guildID string) error {
	locks := c.sharedLocks
	if locks == nil {
		locks = &c.guildLocks
	}
	release, err := lockGuildOperation(ctx, locks, guildID)
	if err != nil {
		return err
	}
	defer release()
	settings, status, err := c.service.Settings(ctx, honeypot.Actor{GuildID: guildID, CanManage: true})
	if err != nil {
		return err
	}
	if !status.Enabled || !status.Configured {
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
	message, err := c.sendWarningReplacement(ctx, guildID, settings, content)
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
	incidentLabel := "incidents"
	if count == 1 {
		incidentLabel = "incident"
	}
	return fmt.Sprintf("%s\n\n-# %d %s caught.", warning, count, incidentLabel)
}
