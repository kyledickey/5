package moduleintegration

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

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

// processPresentation reloads settings/counts under the same guild lock used by
// setup, retiring disabled configurations and preserving concurrent generations.
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
