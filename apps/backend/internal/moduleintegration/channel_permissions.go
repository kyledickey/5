package moduleintegration

import (
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// validateStaffOnlyACL requires an explicit everyone denial and, when supplied,
// explicit visibility for every configured staff role.
func validateStaffOnlyACL(channel *discordgo.Channel, guildID string, staffRoleIDs []string) error {
	if channel == nil || channel.GuildID != guildID {
		return errors.New("channel is outside the configured guild")
	}
	deniedEveryone := false
	allowedRoles := map[string]bool{}
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == guildID && overwrite.Type == discordgo.PermissionOverwriteTypeRole {
			if overwrite.Allow&discordgo.PermissionViewChannel != 0 {
				return errors.New("channel explicitly grants everyone visibility")
			}
			if overwrite.Deny&discordgo.PermissionViewChannel != 0 {
				deniedEveryone = true
			}
		}
		if overwrite.Type == discordgo.PermissionOverwriteTypeRole && overwrite.Allow&discordgo.PermissionViewChannel != 0 {
			allowedRoles[overwrite.ID] = true
		}
	}
	if !deniedEveryone {
		return errors.New("channel is not staff-only")
	}
	for _, roleID := range staffRoleIDs {
		if !allowedRoles[roleID] {
			return fmt.Errorf("staff role %s cannot view ticket channel", roleID)
		}
	}
	return nil
}

var _ tickets.DiscordClient = ticketDiscordClient{}

var _ generallogging.DeliveryClient = loggingDiscordClient{}
