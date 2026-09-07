package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// caseMember uses identities already supplied by Discord, avoiding a profile API call on the response path.
// A cache miss leaves the card without an author row; it never substitutes the moderator for the target.
func caseMember(ctx ui.Context, targetID string) *discordgo.User {
	if targetID == "" || ctx.Interaction == nil {
		return nil
	}
	if ctx.Interaction.Type == discordgo.InteractionApplicationCommand {
		data := ctx.Interaction.ApplicationCommandData()
		if data.Resolved != nil && data.Resolved.Users[targetID] != nil {
			return data.Resolved.Users[targetID]
		}
	}
	if ctx.Interaction.Member != nil && ctx.Interaction.Member.User != nil && ctx.Interaction.Member.User.ID == targetID {
		return ctx.Interaction.Member.User
	}
	if ctx.Session != nil && ctx.Session.State != nil {
		if member, err := ctx.Session.State.Member(ctx.Interaction.GuildID, targetID); err == nil && member != nil {
			return member.User
		}
	}
	return nil
}
