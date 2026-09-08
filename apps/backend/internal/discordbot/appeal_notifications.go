package discordbot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
)

// AppealStaffChannelResolver returns the configured staff-only destination for an appeal event.
type AppealStaffChannelResolver interface {
	AppealStaffChannel(context.Context, string) (string, error)
}

// AppealNotificationAdapter sends appeal outbox messages through Discord without embedding staff identity.
type AppealNotificationAdapter struct {
	Session  *discordgo.Session
	Resolver AppealStaffChannelResolver
}

// SendAppealMemberNotification delivers one member-owned status update through DM.
func (a *AppealNotificationAdapter) SendAppealMemberNotification(ctx context.Context, discordUserID, body string) (string, error) {
	if a == nil || a.Session == nil || strings.TrimSpace(discordUserID) == "" {
		return "", fmt.Errorf("%w: member adapter unavailable", quack.ErrAppealDeliveryDeferred)
	}
	channel, err := a.Session.UserChannelCreate(discordUserID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", appealMemberSendError(err)
	}
	message, err := a.Session.ChannelMessageSendComplex(channel.ID, ui.Signal("appeal", body, false).SendParams(ui.SessionApplicationID(a.Session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", appealMemberSendError(err)
	}
	return message.ID, nil
}

// SendAppealStaffNotification delivers one queue entry only to a configured staff destination.
func (a *AppealNotificationAdapter) SendAppealStaffNotification(ctx context.Context, guildID string, appeal *quack.AppealResponse) (string, error) {
	if a == nil || a.Session == nil || a.Resolver == nil {
		return "", fmt.Errorf("%w: staff adapter unavailable", quack.ErrAppealDeliveryDeferred)
	}
	channelID, err := a.Resolver.AppealStaffChannel(ctx, guildID)
	if err != nil {
		return "", fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, err)
	}
	if strings.TrimSpace(channelID) == "" {
		return "", fmt.Errorf("%w: staff channel unavailable", quack.ErrAppealDeliveryDeferred)
	}
	message, err := a.Session.ChannelMessageSendComplex(channelID, views.AppealStaffMessage(appeal).SendParams(ui.SessionApplicationID(a.Session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", appealSendError(err)
	}
	return message.ID, nil
}

// appealSendError retries only explicit Discord rejections, never ambiguous
// network errors after a send may have reached Discord.
func appealSendError(err error) error {
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Response != nil {
		switch rest.Response.StatusCode {
		case http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
			return fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, err)
		}
	}
	return err
}

// appealMemberSendError leaves blocked/closed DMs as recorded failures rather
// than probing the member indefinitely. Rate limits remain safe to retry.
func appealMemberSendError(err error) error {
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Response != nil && rest.Response.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, err)
	}
	return err
}
