package quack_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestSystemHoneypotBotAndStaffPolicy preserves the trap's legacy exemption
// boundary without weakening ordinary staff-attributed moderation safety.
func TestSystemHoneypotBotAndStaffPolicy(t *testing.T) {
	for _, scenario := range []struct {
		name, id string
		bot      bool
		bits     uint64
		denied   bool
	}{
		{name: "ordinary bot", id: "third-party", bot: true},
		{name: "moderator bot", id: "third-party", bot: true, bits: uint64(discordgo.PermissionModerateMembers), denied: true},
		{name: "administrator bot", id: "third-party", bot: true, bits: uint64(discordgo.PermissionAdministrator), denied: true},
		{name: "human moderator", id: "human", bits: uint64(discordgo.PermissionModerateMembers), denied: true},
		{name: "Quack", id: "quack", bot: true, denied: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repository := newMigratedStore(t)
			snapshot := authorizationSnapshot("owner", "actor", uint64(discordgo.PermissionModerateMembers))
			snapshot.Target = &quack.DiscordMemberAuthorization{DiscordUserID: scenario.id, Present: true, Bot: scenario.bot, PermissionBits: scenario.bits, TopRolePosition: 1}
			service := quack.NewGuildService(repository, fakeDiscordClient{botGuild: &snapshot.Guild, authorization: snapshot})
			guildContext := &quack.GuildStaffContext{Guild: &model.Guild{DiscordGuildID: "guild-1"}}
			err := service.PreflightSystemCase(context.Background(), guildContext, scenario.id, model.ActionBanUser)
			if errors.Is(err, quack.ErrAuthorizationDenied) != scenario.denied || (err != nil && !scenario.denied) {
				t.Fatal("system policy mismatch", err)
			}
		})
	}
}
