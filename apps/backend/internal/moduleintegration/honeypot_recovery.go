package moduleintegration

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// systemHoneypotCaseFinder is a read-only core boundary restricted to an exact
// deterministic system incident; recovery never exposes this lookup to members.
type systemHoneypotCaseFinder interface {
	FindSystemHoneypot(context.Context, string, quack.CaseInput) (*quack.CaseResponse, error)
}

// FindHoneypotCase reconciles a case committed before its module receipt without
// consulting Discord or scheduling moderation a second time.
func (a honeypotCaseApplier) FindHoneypotCase(ctx context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	finder, ok := a.cases.(systemHoneypotCaseFinder)
	if !ok {
		return honeypot.ApplyResult{}, errors.New("honeypot case lookup is unavailable")
	}
	saved, err := finder.FindSystemHoneypot(ctx, request.GuildID, quack.CaseInput{
		Source: model.CaseSourceHoneypot, TemplateID: request.TemplateID, TargetDiscordUserID: request.TargetDiscordUserID,
		ContextChannelDiscordID: request.ContextChannelDiscordID, ContextMessageDiscordID: request.ContextMessageDiscordID, ContextURL: request.ContextURL, IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil || saved == nil {
		return honeypot.ApplyResult{}, err
	}
	return honeypot.ApplyResult{CaseID: saved.ID}, nil
}

// PrepareHoneypotRecovery rechecks the original message and current author before
// a missing case can enter the normal live preflight/evidence transaction. URL is
// rebuilt from live channel identity so old pending records need no new column.
func (a honeypotCaseApplier) PrepareHoneypotRecovery(ctx context.Context, request honeypot.ApplyRequest) (honeypot.ApplyRequest, error) {
	if a.session == nil {
		return request, errors.New("honeypot Discord recovery is unavailable")
	}
	channel, err := a.session.Channel(request.ContextChannelDiscordID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return request, err
	}
	if channel == nil || channel.ID != request.ContextChannelDiscordID || channel.GuildID == "" || channel.Type != discordgo.ChannelTypeGuildText {
		return request, honeypot.ErrNotTrigger
	}
	message, err := a.session.ChannelMessage(channel.ID, request.ContextMessageDiscordID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		var rest *discordgo.RESTError
		if errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownMessage {
			return request, honeypot.ErrNotTrigger
		}
		return request, err
	}
	if message == nil || message.ID != request.ContextMessageDiscordID || message.ChannelID != channel.ID || message.Author == nil || message.Author.ID != request.TargetDiscordUserID {
		return request, honeypot.ErrNotTrigger
	}
	if message.Author.ID == currentBotID(a.session) || message.WebhookID != "" {
		return request, honeypot.ErrExempt
	}
	guild, err := a.session.Guild(channel.GuildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return request, err
	}
	member, err := a.session.GuildMember(channel.GuildID, request.TargetDiscordUserID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return request, err
	}
	if guild == nil || guild.ID != channel.GuildID || member == nil || member.User == nil || member.User.ID != request.TargetDiscordUserID {
		return request, errors.New("honeypot recovery member is unavailable")
	}
	permissions := channelPermissions(guild, &discordgo.Channel{GuildID: guild.ID}, member)
	if member.User.ID == currentBotID(a.session) || permissions&(discordgo.PermissionAdministrator|discordgo.PermissionModerateMembers) != 0 {
		return request, honeypot.ErrExempt
	}
	request.ContextURL = fmt.Sprintf("https://discord.com/channels/%s/%s/%s", channel.GuildID, channel.ID, message.ID)
	return request, nil
}
