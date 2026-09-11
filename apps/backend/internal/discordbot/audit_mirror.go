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
// A destination that fails validation or that Discord rejects with 403/404 is
// reported as quack.ErrAuditMirrorChannelUnavailable so the worker can pause
// that guild; any other failure is retried by the worker's normal policy.
func (b *Bot) SendAuditMirror(ctx context.Context, message quack.AuditMirrorMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.ValidateStaffChannel(ctx, message.DiscordGuildID, message.ChannelDiscordID); err != nil {
		return fmt.Errorf("%w: private destination validation failed", quack.ErrAuditMirrorChannelUnavailable)
	}
	notice := views.AuditMirrorMessage(message)
	params := notice.SendParams(ui.SessionApplicationID(b.Session))
	_, err := b.Session.ChannelMessageSendComplex(message.ChannelDiscordID, params, singleAttempt(ctx)...)
	if err == nil {
		return nil
	}
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil &&
		(restErr.Response.StatusCode == http.StatusForbidden || restErr.Response.StatusCode == http.StatusNotFound) {
		return fmt.Errorf("%w: Discord rejected configured channel", quack.ErrAuditMirrorChannelUnavailable)
	}
	return errors.New("discord audit mirror delivery failed")
}
