package discordbot

import (
	"context"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/actionmods"
)

// SendDM sends dm through the configured external gateway.
func (b *Bot) SendDM(ctx context.Context, userID, message string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || b.Session == nil {
		return nil, actionmods.DiscordError{Code: "discord_session_unavailable", Message: "discord session is unavailable", Retryable: true}
	}
	channel, err := b.Session.UserChannelCreate(userID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordError("send_dm_channel", err)
	}
	sent, err := b.Session.ChannelMessageSendComplex(channel.ID, ui.Signal("message", message, false).SendParams(ui.SessionApplicationID(b.Session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordError("send_dm_message", err)
	}
	result := map[string]any{"channel_id": channel.ID}
	if sent != nil {
		result["message_id"] = sent.ID
	}
	return result, nil
}

// PrepareDM opens the target's direct-message channel before an irreversible membership action.
func (b *Bot) PrepareDM(ctx context.Context, userID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if b == nil || b.Session == nil {
		return "", actionmods.DiscordError{Code: "discord_session_unavailable", Message: "Discord is unavailable", Retryable: true}
	}
	channel, err := b.Session.UserChannelCreate(userID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", classifyDiscordOperation("dm_prepare", err, false)
	}
	return channel.ID, nil
}

// SendPreparedDM sends one final structured case notification through a pre-opened channel.
func (b *Bot) SendPreparedDM(ctx context.Context, channelID, message string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sent, err := b.Session.ChannelMessageSendComplex(channelID, ui.Signal("message", message, false).SendParams(ui.SessionApplicationID(b.Session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordOperation("dm_send", err, true)
	}
	result := map[string]any{"channel_id": channelID}
	if sent != nil {
		result["message_id"] = sent.ID
	}
	return result, nil
}

// SendCaseNotification renders member-safe facts and delivers through a prepared
// or newly opened DM. Every return preserves the rendered attempt body; only an
// appealable snapshot adds appeal controls. Core owns delivery fencing and retries.
func (b *Bot) SendCaseNotification(ctx context.Context, request quack.CaseNotificationRequest) (quack.CaseNotificationReceipt, error) {
	receipt := quack.CaseNotificationReceipt{RenderedMessage: renderCaseNotification(request), ChannelID: request.PreparedChannelDiscordID}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if b == nil || b.Session == nil {
		return receipt, actionmods.DiscordError{Code: "discord_session_unavailable", Message: "Discord is unavailable", Retryable: true}
	}
	if strings.TrimSpace(receipt.ChannelID) == "" {
		channel, err := b.Session.UserChannelCreate(request.TargetDiscordUserID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			return receipt, classifyDiscordOperation("dm_prepare", err, false)
		}
		receipt.ChannelID = channel.ID
	}
	notice := ui.Signal("message", receipt.RenderedMessage, false)
	if request.AppealControl {
		entry, err := views.AppealEntryMessage(request.DashboardBaseURL, request.GuildID, request.CaseID)
		if err != nil {
			return receipt, err
		}
		notice.Components = entry.Components
	}
	sent, err := b.Session.ChannelMessageSendComplex(receipt.ChannelID, notice.SendParams(ui.SessionApplicationID(b.Session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		// A lost response can follow a delivered message regardless of how the
		// DM channel was opened; never classify that send as safely repeatable.
		return receipt, classifyDiscordOperation("dm_send", err, true)
	}
	if sent != nil {
		receipt.MessageID = sent.ID
	}
	return receipt, nil
}
