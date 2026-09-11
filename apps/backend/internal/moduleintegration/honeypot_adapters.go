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

// systemHoneypotCaseCreator is QP-A's single system-attributed moderation entrypoint.
type systemHoneypotCaseCreator interface {
	CreateSystemHoneypot(context.Context, string, quack.CaseInput) (*quack.CaseResponse, error)
}

// honeypotCaseApplier adapts QP-F exclusively to QP-A's normal system case
// boundary; it has no repository access and cannot bypass case orchestration.
type honeypotCaseApplier struct {
	cases   systemHoneypotCaseCreator
	session *discordgo.Session
}

// ApplyHoneypotCase validates the fixed automation envelope before preserving
// every request field in the core case input.
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

// unattendedTemplateValidator exposes only the core compatibility use case
// needed by honeypot setup and live policy revalidation.
type unattendedTemplateValidator interface {
	ValidateUnattendedTemplate(context.Context, string, string) error
}

// honeypotTemplateValidator maps core policy failures to module availability
// without giving the integration adapter access to template persistence.
type honeypotTemplateValidator struct{ templates unattendedTemplateValidator }

// ValidateHoneypotTemplate checks live core policy and preserves operational
// failures as errors distinct from a policy becoming unavailable.
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

// honeypotChannelValidator checks the current Discord channel and bot access.
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

// currentBotMember loads current bot membership rather than trusting gateway
// message fields or stale optional-module configuration.
func currentBotMember(ctx context.Context, session *discordgo.Session, discordGuildID string) (*discordgo.Guild, *discordgo.Member, error) {
	if session == nil {
		return nil, nil, errors.New("Discord session is not configured")
	}
	botID := currentBotID(session)
	if botID == "" {
		user, err := session.User("@me", discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil || user == nil {
			return nil, nil, errors.New("current Discord bot identity is unavailable")
		}
		botID = user.ID
	}
	guild, err := session.Guild(discordGuildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || guild == nil {
		return nil, nil, errors.New("current Discord guild is unavailable")
	}
	member, err := session.GuildMember(discordGuildID, botID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || member == nil || member.User == nil || member.User.ID != botID {
		return nil, nil, errors.New("current Discord bot membership is unavailable")
	}
	return guild, member, nil
}

// currentBotID returns the gateway-authenticated identity when already known.
func currentBotID(session *discordgo.Session) string {
	if session != nil && session.State != nil && session.State.User != nil {
		return session.State.User.ID
	}
	return ""
}

// projectHoneypotMessage combines a gateway identity with freshly loaded
// member, guild, channel, and permission state.
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

// channelPermissions applies Discord's role and channel-overwrite precedence
// to current REST projections.
func channelPermissions(guild *discordgo.Guild, channel *discordgo.Channel, member *discordgo.Member) int64 {
	if guild == nil || channel == nil || member == nil || member.User == nil {
		return 0
	}
	permissions := int64(0)
	roles := make(map[string]struct{}, len(member.Roles))
	for _, roleID := range member.Roles {
		roles[roleID] = struct{}{}
	}
	for _, role := range guild.Roles {
		if role == nil {
			continue
		}
		if role.ID == guild.ID {
			permissions |= role.Permissions
		}
		if _, ok := roles[role.ID]; ok {
			permissions |= role.Permissions
		}
	}
	if member.User.ID == guild.OwnerID || permissions&discordgo.PermissionAdministrator != 0 {
		return discordgo.PermissionAll
	}
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == guild.ID && overwrite.Type == discordgo.PermissionOverwriteTypeRole {
			permissions = permissions&^overwrite.Deny | overwrite.Allow
			break
		}
	}
	roleDeny, roleAllow := int64(0), int64(0)
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.Type != discordgo.PermissionOverwriteTypeRole {
			continue
		}
		if _, ok := roles[overwrite.ID]; ok {
			roleDeny |= overwrite.Deny
			roleAllow |= overwrite.Allow
		}
	}
	permissions = permissions&^roleDeny | roleAllow
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == member.User.ID && overwrite.Type == discordgo.PermissionOverwriteTypeMember {
			permissions = permissions&^overwrite.Deny | overwrite.Allow
			break
		}
	}
	return permissions
}

// RequiredGatewayIntents keeps subscriptions stable across live module changes.
// Message content supports evidence/transcripts as well as general logging;
// member events support permission repair even when logging is disabled.
func (r *Runtime) RequiredGatewayIntents(ctx context.Context) (discordgo.Intent, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return discordgo.IntentGuilds | discordgo.IntentGuildMembers | discordgo.IntentGuildModeration | discordgo.IntentGuildMessages | discordgo.IntentMessageContent, nil
}

// HandleTemplateChange forwards archive and unattended-compatibility drift to
// the isolated adapter while retaining the selected reference for repair.
func (r *Runtime) HandleTemplateChange(ctx context.Context, guildID, templateID string) {
	if r == nil || r.HoneypotDiscord == nil {
		return
	}
	if err := (r.honeypotTemplates).ValidateHoneypotTemplate(ctx, guildID, templateID); errors.Is(err, honeypot.ErrTemplateUnavailable) {
		_ = r.HoneypotDiscord.HandleTemplateUnavailable(ctx, guildID, templateID)
	}
}
