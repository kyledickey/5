package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// systemHoneypotCaseCreator is the core's only system-attributed case entrypoint.
type systemHoneypotCaseCreator interface {
	CreateSystemHoneypot(context.Context, string, quack.CaseInput) (*quack.CaseResponse, error)
}

// honeypotCaseApplier reaches the core only through the normal system case
// boundary; it has no repository access and cannot bypass case orchestration.
type honeypotCaseApplier struct {
	cases   systemHoneypotCaseCreator
	session *discordgo.Session
}

// ApplyHoneypotCase validates the fixed automation envelope before passing every
// request field to the core case input.
func (a honeypotCaseApplier) ApplyHoneypotCase(ctx context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	if a.cases == nil {
		return honeypot.ApplyResult{}, errors.New("honeypot case application is not configured")
	}
	if request.Source != honeypot.SourceHoneypot || request.ActorType != honeypot.ActorTypeSystem || strings.TrimSpace(request.ActorDiscordUserID) != "" {
		return honeypot.ApplyResult{}, errors.New("honeypot case attribution is invalid")
	}
	if strings.TrimSpace(request.GuildID) == "" || strings.TrimSpace(request.TemplateID) == "" || strings.TrimSpace(request.TargetDiscordUserID) == "" || strings.TrimSpace(request.ContextChannelDiscordID) == "" || strings.TrimSpace(request.ContextMessageDiscordID) == "" || strings.TrimSpace(request.ContextURL) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return honeypot.ApplyResult{}, errors.New("honeypot case request is incomplete")
	}
	created, err := a.cases.CreateSystemHoneypot(ctx, request.GuildID, quack.CaseInput{
		TemplateID: request.TemplateID, TargetDiscordUserID: request.TargetDiscordUserID,
		Source:                  model.CaseSourceHoneypot,
		ContextChannelDiscordID: request.ContextChannelDiscordID,
		ContextMessageDiscordID: request.ContextMessageDiscordID,
		ContextURL:              request.ContextURL, IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return honeypot.ApplyResult{}, err
	}
	if created == nil || created.ID == "" {
		return honeypot.ApplyResult{}, errors.New("honeypot case creation returned no saved case")
	}
	return honeypot.ApplyResult{CaseID: created.ID}, nil
}

// DeleteHoneypotMessage performs only the durable worker's cleanup operation.
// Missing channels/messages already satisfy deletion, including after a restart
// between the Discord delete and its local completion receipt.
func (a honeypotCaseApplier) DeleteHoneypotMessage(ctx context.Context, channelID, messageID string) error {
	if a.session == nil {
		return errors.New("honeypot Discord cleanup is not configured")
	}
	err := a.session.ChannelMessageDelete(channelID, messageID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil && (rest.Message.Code == discordgo.ErrCodeUnknownMessage || rest.Message.Code == discordgo.ErrCodeUnknownChannel) {
		return nil
	}
	return err
}

type unattendedTemplateValidator interface {
	ValidateUnattendedTemplate(context.Context, string, string) error
}

type honeypotTemplateValidator struct{ templates unattendedTemplateValidator }

// ValidateHoneypotTemplate keeps operational failures distinct from a policy
// becoming unavailable, which the module treats as configuration drift.
func (v honeypotTemplateValidator) ValidateHoneypotTemplate(ctx context.Context, guildID, templateID string) error {
	if v.templates == nil {
		return errors.New("honeypot template service is not configured")
	}
	err := v.templates.ValidateUnattendedTemplate(ctx, guildID, templateID)
	if errors.Is(err, quack.ErrUnattendedTemplateUnavailable) {
		return fmt.Errorf("%w: %v", honeypot.ErrTemplateUnavailable, err)
	}
	return err
}

type honeypotChannelValidator struct {
	session  *discordgo.Session
	resolver guildResolver
}

// ValidateHoneypotChannel requires an exact live guild/channel match and bot
// access for warning delivery, evidence reads and trigger cleanup.
func (v honeypotChannelValidator) ValidateHoneypotChannel(ctx context.Context, guildID, channelID string) error {
	if v.session == nil {
		return errors.New("honeypot Discord session is not configured")
	}
	discordGuildID, err := v.resolver.discordID(ctx, strings.TrimSpace(guildID))
	if err != nil {
		return err
	}
	channel, err := v.session.Channel(strings.TrimSpace(channelID), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || channel == nil || channel.GuildID != discordGuildID || channel.Type != discordgo.ChannelTypeGuildText {
		return honeypot.ErrChannelUnavailable
	}
	guild, member, err := currentBotMember(ctx, v.session, discordGuildID)
	if err != nil {
		return err
	}
	permissions := channelPermissions(guild, channel, member)
	var missing []string
	for _, required := range []struct {
		bit  int64
		name string
	}{
		{discordgo.PermissionViewChannel, "View Channel"},
		{discordgo.PermissionSendMessages, "Send Messages"},
		{discordgo.PermissionReadMessageHistory, "Read Message History"},
		{discordgo.PermissionManageMessages, "Manage Messages"},
	} {
		if permissions&required.bit == 0 {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: Quack needs %s in the honeypot channel", honeypot.ErrChannelUnavailable, strings.Join(missing, ", "))
	}
	return nil
}

func projectHoneypotMessage(internalGuildID string, event *discordgo.MessageCreate, guild *discordgo.Guild, channel *discordgo.Channel, member *discordgo.Member, botID string) (honeypot.Message, error) {
	if strings.TrimSpace(internalGuildID) == "" || event == nil || event.Message == nil || event.GuildID == "" || guild == nil || guild.ID != event.GuildID || channel == nil || channel.GuildID != event.GuildID || member == nil || member.User == nil || member.User.ID == "" {
		return honeypot.Message{}, errors.New("honeypot message projection is incomplete")
	}
	if event.Author == nil || event.Author.ID != member.User.ID {
		return honeypot.Message{}, errors.New("honeypot message author does not match current member")
	}
	// Guild moderation authority is independent of trap-channel overwrites.
	// An overwrite must neither exempt an ordinary member nor remove a moderator's exemption.
	permissions := channelPermissions(guild, &discordgo.Channel{GuildID: guild.ID}, member)
	staffPermissions := int64(discordgo.PermissionAdministrator | discordgo.PermissionModerateMembers)
	return honeypot.Message{
		GuildID: strings.TrimSpace(internalGuildID), ChannelDiscordID: channel.ID,
		MessageDiscordID: event.ID, AuthorDiscordUserID: member.User.ID,
		MessageURL: fmt.Sprintf("https://discord.com/channels/%s/%s/%s", event.GuildID, channel.ID, event.ID),
		IsBot:      member.User.Bot, IsQuack: member.User.ID == botID,
		IsWebhook:         event.WebhookID != "",
		AuthorCanModerate: permissions&staffPermissions != 0,
	}, nil
}

// systemHoneypotCaseFinder is a read-only lookup for one exact deterministic
// system incident; recovery never exposes it to members.
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

// HandleTemplateChange forwards template drift to the module while retaining the
// selected reference for repair.
func (r *Runtime) HandleTemplateChange(ctx context.Context, guildID, templateID string) {
	if r == nil || r.HoneypotDiscord == nil {
		return
	}
	if err := (r.honeypotTemplates).ValidateHoneypotTemplate(ctx, guildID, templateID); errors.Is(err, honeypot.ErrTemplateUnavailable) {
		_ = r.HoneypotDiscord.HandleTemplateUnavailable(ctx, guildID, templateID)
	}
}
