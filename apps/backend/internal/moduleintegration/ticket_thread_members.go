package moduleintegration

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
)

// syncTicketThreadMembers removes invitations held by former moderators.
// It inspects only existing thread members, never the entire guild, and does not
// invite staff who have not chosen to join the conversation.
func (c ticketDiscordClient) syncTicketThreadMembers(ctx context.Context, guildID, threadID, ownerID string) error {
	botID, err := c.botUserID(ctx)
	if err != nil {
		return err
	}
	guild, err := c.session.Guild(guildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return err
	}
	if guild == nil {
		return errors.New("guild authorization unavailable")
	}
	roles := make(map[string]int64)
	for _, role := range guild.Roles {
		if role != nil {
			roles[role.ID] = role.Permissions
		}
	}
	after := ""
	for {
		members, err := c.session.ThreadMembers(threadID, 100, false, after, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			return err
		}
		for _, member := range members {
			if member == nil || member.UserID == "" {
				return errors.New("Discord returned an invalid thread member")
			}
			if member.UserID == ownerID || member.UserID == botID || member.UserID == guild.OwnerID {
				continue
			}
			current, err := c.session.GuildMember(guildID, member.UserID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
			var rest *discordgo.RESTError
			if err != nil && !(errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownMember) {
				return err
			}
			permissions := roles[guildID]
			if current != nil {
				for _, id := range current.Roles {
					permissions |= roles[id]
				}
			}
			if current != nil && permissions&(discordgo.PermissionAdministrator|discordgo.PermissionModerateMembers) != 0 {
				continue
			}
			if err := c.session.ThreadMemberRemove(threadID, member.UserID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)); err != nil {
				return err
			}
		}
		if len(members) < 100 {
			break
		}
		next := members[len(members)-1].UserID
		if next == after {
			return errors.New("Discord repeated a thread-member page")
		}
		after = next
	}
	return nil
}
