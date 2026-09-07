package discordbot

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
)

// SendAuditMirror sends one core audit event to its configured staff-only channel.
// It does not share formatting, queues, or state with optional general logging.
func (b *Bot) SendAuditMirror(ctx context.Context, message quack.AuditMirrorMessage) error {
	if b == nil || b.Session == nil {
		return errors.New("Discord audit mirror adapter is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.ValidateStaffChannel(ctx, message.DiscordGuildID, message.ChannelDiscordID); err != nil {
		return fmt.Errorf("%w: private destination validation failed", quack.ErrAuditMirrorChannelUnavailable)
	}
	notice := views.AuditMirrorMessage(message)
	_, err := b.Session.ChannelMessageSendComplex(message.ChannelDiscordID, notice.SendParams(ui.SessionApplicationID(b.Session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err == nil {
		return nil
	}
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil && (restErr.Response.StatusCode == http.StatusForbidden || restErr.Response.StatusCode == http.StatusNotFound) {
		return fmt.Errorf("%w: Discord rejected configured channel", quack.ErrAuditMirrorChannelUnavailable)
	}
	return errors.New("Discord audit mirror delivery failed")
}
