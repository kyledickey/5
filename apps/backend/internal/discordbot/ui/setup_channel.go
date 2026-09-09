package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/bwmarrin/discordgo"
)

// SetupChannelKind selects permissions for channels Quack creates. It never
// changes overwrites on channels an administrator supplies or already configured.
type SetupChannelKind int

const (
	// SetupStaffChannel creates a destination visible to managers and moderators.
	SetupStaffChannel SetupChannelKind = iota
	// SetupTicketEntry creates a public button channel with private-thread support.
	SetupTicketEntry
	// SetupHoneypotChannel creates a public trap in which members can send messages.
	SetupHoneypotChannel
)

// SetupChannel uses an explicit destination, reuses an existing configured channel,
// or creates one with suitable permissions. Callers must verify Manage Server
// authority first and validate the bot's operational permissions before saving.
// Only a confirmed missing configured channel is replaced; transient errors must
// not create duplicates. Explicit choices are never edited or silently replaced.
func SetupChannel(ctx context.Context, session *discordgo.Session, guildID, specified, configured, name string, kind SetupChannelKind) (string, error) {
	if specified != "" {
		return specified, nil
	}
	if session == nil {
		return "", errors.New("Quack is not connected. Try again shortly.")
	}
	options := []discordgo.RequestOption{discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)}
	if configured != "" {
		channel, err := session.Channel(configured, options...)
		if err == nil && channel != nil && channel.GuildID == guildID && channel.Type == discordgo.ChannelTypeGuildText {
			return configured, nil
		}
		var rest *discordgo.RESTError
		if !errors.As(err, &rest) || rest.Response == nil || rest.Response.StatusCode != http.StatusNotFound || rest.Message == nil || rest.Message.Code != discordgo.ErrCodeUnknownChannel {
			return "", errors.New("Could not access the configured channel. Check Quack's permissions or specify another channel.")
		}
	}
	guild, err := session.Guild(guildID, options...)
	if err != nil || guild == nil {
		return "", errors.New("Could not read the server's roles. Try again.")
	}
	botID := ""
	if session.State != nil && session.State.User != nil {
		botID = session.State.User.ID
	}
	if botID == "" {
		user, err := session.User("@me", options...)
		if err != nil || user == nil {
			return "", errors.New("Could not identify Quack's server account. Try again.")
		}
		botID = user.ID
	}
	topic, intro := setupChannelPresentation(name, kind)
	channel, err := session.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name: name, Type: discordgo.ChannelTypeGuildText, Topic: topic,
		PermissionOverwrites: setupChannelPermissions(guild, botID, kind),
	}, options...)
	if err != nil || channel == nil || channel.ID == "" {
		return "", fmt.Errorf("Could not create #%s. Quack needs Manage Channels permission. You can also specify an existing channel.", name)
	}
	// The channel already exists. Return its identity even if presentation fails,
	// allowing the caller to save it instead of creating another channel on retry.
	if intro != "" {
		if _, err := session.ChannelMessageSendComplex(channel.ID, Content(intro, false).SendParams(SessionApplicationID(session)), options...); err != nil {
			slog.WarnContext(ctx, "Could not send new channel introduction", "channel_id", channel.ID, "error", err)
		}
	}
	return channel.ID, nil
}

// setupChannelPermissions gives new channels a usable default without guessing
// role names. Managers and moderators can read staff destinations; administrators
// retain Discord's normal bypass. Bot overwrites grant each feature's capabilities.
func setupChannelPermissions(guild *discordgo.Guild, botID string, kind SetupChannelKind) []*discordgo.PermissionOverwrite {
	read := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory)
	write := read | discordgo.PermissionSendMessages | discordgo.PermissionAttachFiles | discordgo.PermissionEmbedLinks
	everyone := &discordgo.PermissionOverwrite{ID: guild.ID, Type: discordgo.PermissionOverwriteTypeRole}
	bot := &discordgo.PermissionOverwrite{ID: botID, Type: discordgo.PermissionOverwriteTypeMember, Allow: write}
	overwrites := []*discordgo.PermissionOverwrite{everyone, bot}
	switch kind {
	case SetupStaffChannel:
		everyone.Deny = discordgo.PermissionViewChannel
		for _, role := range guild.Roles {
			if role != nil && role.ID != guild.ID && role.Permissions&(discordgo.PermissionManageServer|discordgo.PermissionModerateMembers|discordgo.PermissionAdministrator) != 0 {
				overwrites = append(overwrites, &discordgo.PermissionOverwrite{ID: role.ID, Type: discordgo.PermissionOverwriteTypeRole, Allow: write})
			}
		}
	case SetupTicketEntry:
		everyone.Allow = read | discordgo.PermissionSendMessagesInThreads | discordgo.PermissionAttachFiles
		everyone.Deny = discordgo.PermissionSendMessages | discordgo.PermissionCreatePublicThreads | discordgo.PermissionCreatePrivateThreads
		bot.Allow |= discordgo.PermissionCreatePrivateThreads | discordgo.PermissionSendMessagesInThreads | discordgo.PermissionManageThreads
	case SetupHoneypotChannel:
		everyone.Allow = write
		bot.Allow |= discordgo.PermissionManageMessages
	}
	return overwrites
}

// setupChannelPresentation describes newly created destinations. Entry channels
// use their feature's durable panel or warning as the welcome message instead of
// posting a second introduction that would compete with its primary control.
func setupChannelPresentation(name string, kind SetupChannelKind) (topic, intro string) {
	switch kind {
	case SetupTicketEntry:
		return "Need a hand? Open a private ticket with the team below.", ""
	case SetupHoneypotChannel:
		return "Do not post here. Read the warning below for what happens if you do.", ""
	}
	switch name {
	case "appeals":
		return "Case appeals for the team to review.", "# Appeals\nNew appeals will appear here for the team to review."
	case "moderation-log":
		return "Quack case activity, moderation actions, and settings changes.", "# Moderation log\nQuack will keep case activity, moderation actions, and settings changes here."
	case "discord-log":
		return "Message activity, member arrivals and departures, and server changes recorded by Quack.", "# Server activity\nQuack will record message edits and deletions, member arrivals and departures, bans, and server changes here."
	case "ticket-log":
		return "Support tickets and updates for the team.", "# Tickets\nNew tickets and updates will appear here. Open a ticket's thread to help out."
	default:
		return "Updates from Quack for the moderation team.", "# Quack updates\nUpdates for the team will appear here."
	}
}
