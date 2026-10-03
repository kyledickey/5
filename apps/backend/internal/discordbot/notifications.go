package discordbot

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/actionmods"
	"github.com/quackdiscord/bot/internal/quack/model"
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
		return nil, classifyDiscordOperation("send_dm_channel", err, false)
	}
	sent, err := b.Session.ChannelMessageSendComplex(channel.ID, ui.Signal("message", message, false).SendParams(ui.SessionApplicationID(b.Session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordOperation("send_dm_message", err, false)
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

// renderCaseNotification describes the rule and recorded outcome without staff
// context, evidence or moderator identity.
func renderCaseNotification(request quack.CaseNotificationRequest) string {
	guildName := "this server"
	if strings.TrimSpace(request.GuildName) != "" {
		guildName = request.GuildName
	}
	server := "**" + discordtext.Plain(guildName) + "**"
	icon, lead := "warn", "You received a warning in "+server
	primary := -1
	removed := false
	for i, action := range request.Outcomes {
		if action.Status != model.ActionExecutionSucceeded {
			continue
		}
		switch action.ActionType {
		case model.ActionTimeoutUser:
			icon, lead = "timeout", "You’ve been timed out in "+server
		case model.ActionKickUser:
			icon, lead = "kick", "You’ve been removed from "+server
		case model.ActionBanUser:
			icon, lead = "ban", "You’ve been banned from "+server
		case model.ActionRemoveTimeout:
			icon, lead, removed = "untimeout", "Your timeout in "+server+" has ended", true
		case model.ActionUnbanUser:
			icon, lead, removed = "unban", "You’re no longer banned from "+server, true
		default:
			continue
		}
		primary = i
		break
	}
	parts := []string{}
	if strings.TrimSpace(request.RuleName) != "" {
		rule := "**" + discordtext.Plain(request.RuleName) + "**"
		if removed {
			parts = append(parts, "This updates your case for "+rule+".")
		} else {
			lead += " for " + rule
		}
	}
	if strings.TrimSpace(request.Introduction) != "" {
		parts = append(parts, discordtext.Plain(strings.TrimSpace(request.Introduction)))
	}
	for i, action := range request.Outcomes {
		if action.Status == model.ActionExecutionFailed && primary == -1 {
			icon = "error"
		}
		if i != primary && action.ActionType != model.ActionSendDM {
			parts = append(parts, discordtext.ActionSentence(action.ActionType, action.Status))
		}
		if action.Status != model.ActionExecutionSucceeded || action.ActionType != model.ActionTimeoutUser {
			continue
		}
		if action.TimeoutUntil != nil {
			until := action.TimeoutUntil
			parts = append(parts, fmt.Sprintf("You can chat again <t:%d:R> — <t:%d:f>.", until.Unix(), until.Unix()))
		}
	}
	if request.IncludeAppealInstructions {
		parts = append(parts, "Use the Appeal decision button below to ask the moderators to review this case.")
	}
	if strings.TrimSpace(request.Footer) != "" {
		parts = append(parts, discordtext.Plain(strings.TrimSpace(request.Footer)))
	}
	meta := fmt.Sprintf("Case #%d", request.CaseNumber)
	if !request.CreatedAt.IsZero() {
		meta += fmt.Sprintf(" · <t:%d:R>", request.CreatedAt.Unix())
	}
	return discordtext.Conversation(icon, lead+".", discordtext.Plain(request.Reason), strings.Join(parts, "\n\n"), meta)
}

// appealMemberNotificationBody keeps review decisions distinct from enforcement and
// quotes staff-authored context without disclosing the reviewing staff member.
func appealMemberNotificationBody(notice quack.AppealMemberNotification) string {
	if notice.Intent == nil {
		return notice.LegacyBody
	}
	status, reason := notice.Intent.Status, notice.Intent.Reason
	icon, lead, next := "appeal", "Your appeal was closed.", ""
	switch status {
	case model.AppealStatusNeedsInformation:
		icon, lead, next = "reply", "Staff need a little more information to review your appeal.", "You can reply from your Quack dashboard."
	case model.AppealStatusAccepted:
		icon, lead, next = "accept", "Your appeal was accepted.", "Your case was voided. Quack will try to remove any ban or timeout from it."
	case model.AppealStatusRejected:
		icon, lead = "decline", "Your appeal was rejected."
	}
	meta := ""
	if notice.Intent.CaseNumber > 0 {
		meta = fmt.Sprintf("Case #%d", notice.Intent.CaseNumber)
	} else if notice.Intent.CaseID != "" {
		meta = "Case " + discordtext.Plain(notice.Intent.CaseID)
	}
	if notice.Intent.GuildName != "" {
		if meta != "" {
			meta += " · "
		}
		meta += discordtext.Plain(notice.Intent.GuildName)
	}
	body := discordtext.Conversation(icon, lead, discordtext.Plain(reason), next, meta)
	if notice.Intent.RejoinURL != "" {
		body += "\n\nIf you left or were banned, you can rejoin once any ban has been removed: " + notice.Intent.RejoinURL
	}
	return body
}

// appealMemberNotificationMessage adds a rejoin control only to accepted typed
// intent with its validated, immutable invite URL. Legacy bodies remain literal
// and never become a source of executable links or inferred decision state.
func appealMemberNotificationMessage(notice quack.AppealMemberNotification) ui.Message {
	message := ui.Signal("appeal", appealMemberNotificationBody(notice), false)
	if notice.Intent != nil && notice.Intent.Status == model.AppealStatusAccepted && notice.Intent.RejoinURL != "" {
		message.Components = []discordgo.MessageComponent{ui.Row(ui.LinkButton(notice.Intent.RejoinURL, "Rejoin Server", false))}
	}
	return message
}
