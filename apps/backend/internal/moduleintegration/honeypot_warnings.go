package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack/model"
)

type honeypotWarningPolicy interface {
	UnattendedTemplateActions(context.Context, string, string) ([]model.ActionType, error)
}

// honeypotCounter updates the existing warning from durable incident counts.
// Per-guild serialization prevents a slower update from overwriting a newer count.
// Other guilds remain independent; deleted warnings are recreated from saved text.
type honeypotCounter struct {
	templates   honeypotWarningPolicy
	session     *discordgo.Session
	service     *honeypot.Service
	resolver    guildResolver
	guildLocks  sync.Map
	sharedLocks *sync.Map
}

// IncidentCreated schedules the warning refresh after case persistence. Delivery
// is presentation only and never writes another staff audit event.
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

// refresh reloads settings under the shared presentation lock before validating
// the destination and updating or repairing its warning.
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
	warning, err := resolveHoneypotWarning(ctx, c.templates, guildID, settings)
	if err != nil {
		return err
	}
	content := honeypotWarningContent(warning, status.Statistics.Created)
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

// legacyHoneypotWarning identifies the old generated default so it can follow live policy.
const legacyHoneypotWarning = "# Warning!\nThis channel catches spam and scam accounts. Do not post here. Posting here triggers this server's honeypot moderation rule."

// honeypotWarningContent uses incident counts because editable policies may not ban.
func honeypotWarningContent(warning string, count uint64) string {
	if strings.TrimSpace(warning) == "" {
		warning = "# Do not post here\nThis channel catches spam and scam accounts."
	}
	incidentLabel := "incidents"
	if count == 1 {
		incidentLabel = "incident"
	}
	return fmt.Sprintf("%s\n\n-# %d %s caught.", warning, count, incidentLabel)
}

// resolveHoneypotWarning preserves administrator copy and derives generated copy
// from live policy. Empty saved text remains a default, so later setup and count
// refreshes can reflect a changed punishment without overwriting custom warnings.
func resolveHoneypotWarning(ctx context.Context, templates honeypotWarningPolicy, guildID string, settings honeypot.Settings) (string, error) {
	if strings.TrimSpace(settings.WarningText) != "" && settings.WarningText != legacyHoneypotWarning {
		return settings.WarningText, nil
	}
	if templates == nil {
		return "", errors.New("honeypot warning policy is unavailable")
	}
	actions, err := templates.UnattendedTemplateActions(ctx, guildID, settings.TemplateID)
	if err != nil {
		return "", err
	}
	outcomes := []string{}
	for _, action := range actions {
		var outcome string
		switch action {
		case model.ActionBanUser:
			outcome = "ban you from this server"
		case model.ActionKickUser:
			outcome = "kick you from this server"
		case model.ActionTimeoutUser:
			outcome = "time you out"
		case model.ActionSendDM:
			outcome = "send you a warning by DM"
		case "":
			outcome = "record a moderation case"
		default:
			return "", errors.New("honeypot warning has an unsupported action")
		}
		if !slices.Contains(outcomes, outcome) {
			outcomes = append(outcomes, outcome)
		}
	}
	if len(outcomes) == 0 {
		return "", errors.New("honeypot warning has no configured outcome")
	}
	consequence := "Posting here will " + outcomes[0] + "."
	if len(outcomes) > 1 {
		consequence = "Depending on your previous cases, posting here can " + strings.Join(outcomes, " or ") + "."
	}
	return "# Do not post here\n" + consequence + " This channel catches spam and scam accounts.", nil
}

// RunPresentation reconciles configured warnings once after restart, then polls
// bounded due work. Its dedicated worker cannot delay incident enforcement.
func (c *honeypotCounter) RunPresentation(ctx context.Context) {
	cursor := ""
	seeded := false
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if !seeded {
			ids, err := c.service.ConfiguredWarningGuilds(ctx, cursor)
			if err == nil {
				for _, id := range ids {
					if err = c.service.RequestWarningRefresh(ctx, id); err != nil {
						break
					}
					cursor = id
				}
				seeded = err == nil && len(ids) < 32
			}
			if err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "Honeypot warning startup reconciliation will retry")
			}
		}
		if err := c.processPresentation(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Honeypot warning refresh will retry", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *honeypotCounter) processPresentation(ctx context.Context) error {
	rows, err := c.service.WarningRefreshes(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		taskCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		deliveryErr := c.refresh(taskCtx, row.GuildID)
		cancel()
		if err := c.service.CompleteWarningRefresh(ctx, row, deliveryErr != nil); err != nil {
			return err
		}
		if deliveryErr != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Honeypot warning delivery will retry", "guild_id", row.GuildID, "error", deliveryErr)
		}
	}
	return nil
}

// sendWarningReplacement fences a non-idempotent POST across retries/restarts.
// An unknown outcome is retained for inspection instead of creating duplicates.
func (c *honeypotCounter) sendWarningReplacement(ctx context.Context, guildID string, settings honeypot.Settings, content string) (*discordgo.Message, error) {
	if err := c.service.ReserveWarningSend(ctx, guildID, settings); err != nil {
		return nil, err
	}
	message, err := c.session.ChannelMessageSendComplex(settings.ChannelDiscordID, &discordgo.MessageSend{Content: content, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil && warningDefinitelyNotSent(err) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, c.service.ReleaseWarningSend(cleanupCtx, guildID, settings))
	}
	if err != nil && !warningDefinitelyNotSent(err) {
		err = errors.Join(honeypot.ErrWarningDeliveryUnknown, err)
	}
	if err == nil && (message == nil || message.ID == "") {
		err = honeypot.ErrWarningDeliveryUnknown
	}
	return message, err
}

// warningDefinitelyNotSent allows another POST only for a definite Discord
// rejection; network and server errors can conceal successful message creation.
func warningDefinitelyNotSent(err error) bool {
	var limit *discordgo.RateLimitError
	if errors.As(err, &limit) {
		return true
	}
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Response != nil {
		switch rest.Response.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
			return true
		}
	}
	return false
}
