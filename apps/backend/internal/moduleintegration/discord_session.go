package moduleintegration

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
)

// restOptions are the request options used for every REST call this package
// makes on behalf of a module: bound to ctx, no automatic retries and no
// waiting out rate limits. Callers decide whether a failure is retried, which
// keeps non-idempotent sends from being repeated behind their back.
func restOptions(ctx context.Context) []discordgo.RequestOption {
	return []discordgo.RequestOption{
		discordgo.WithContext(ctx),
		discordgo.WithRestRetries(0),
		discordgo.WithRetryOnRatelimit(false),
	}
}

// currentBotID returns the bot user ID from gateway state, or "" before the
// session has identified.
func currentBotID(session *discordgo.Session) string {
	if session != nil && session.State != nil && session.State.User != nil {
		return session.State.User.ID
	}
	return ""
}

// currentBotMember loads the guild and the bot's own membership through REST so
// permission checks reflect the current state rather than gateway cache or a
// module's stored configuration. It falls back to /users/@me when the gateway
// has not identified yet.
func currentBotMember(ctx context.Context, session *discordgo.Session, discordGuildID string) (*discordgo.Guild, *discordgo.Member, error) {
	botID := currentBotID(session)
	if botID == "" {
		user, err := session.User("@me", restOptions(ctx)...)
		if err != nil || user == nil {
			return nil, nil, errors.New("current Discord bot identity is unavailable")
		}
		botID = user.ID
	}
	guild, err := session.Guild(discordGuildID, restOptions(ctx)...)
	if err != nil || guild == nil {
		return nil, nil, errors.New("current Discord guild is unavailable")
	}
	member, err := session.GuildMember(discordGuildID, botID, restOptions(ctx)...)
	if err != nil || member == nil || member.User == nil || member.User.ID != botID {
		return nil, nil, errors.New("current Discord bot membership is unavailable")
	}
	return guild, member, nil
}

// channelPermissions computes member's effective permissions in channel using
// Discord's precedence: base role permissions, then @everyone, role and member
// overwrites; owners and administrators get everything. Pass a channel with
// only GuildID set to obtain guild-level permissions. Any nil input yields 0.
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
