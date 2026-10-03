package discordbot

import (
	"errors"
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestDiscordMemberAuthorizationCalculatesPermissionsAndHierarchy(t *testing.T) {
	guild := &discordgo.Guild{
		ID: "guild", OwnerID: "owner",
		Roles: []*discordgo.Role{
			{ID: "guild", Position: 0, Permissions: discordgo.PermissionViewChannel},
			{ID: "moderator", Position: 10, Permissions: discordgo.PermissionModerateMembers | discordgo.PermissionKickMembers},
			{ID: "administrator", Position: 5, Permissions: discordgo.PermissionAdministrator},
		},
	}

	moderator := discordMemberAuthorization(guild, &discordgo.Member{
		User: &discordgo.User{ID: "mod", Username: "Moderator"}, Roles: []string{"moderator"},
	})
	if !moderator.Present || moderator.Bot || moderator.TopRolePosition != 10 {
		t.Fatalf("unexpected moderator hierarchy state: %+v", moderator)
	}
	want := uint64(discordgo.PermissionViewChannel | discordgo.PermissionModerateMembers | discordgo.PermissionKickMembers)
	if moderator.PermissionBits&want != want {
		t.Fatalf("expected aggregate permissions %d, got %d", want, moderator.PermissionBits)
	}

	administrator := discordMemberAuthorization(guild, &discordgo.Member{
		User: &discordgo.User{ID: "admin", Username: "Administrator", Bot: true}, Roles: []string{"administrator"},
	})
	if !administrator.Bot || administrator.PermissionBits&uint64(discordgo.PermissionBanMembers) == 0 {
		t.Fatalf("expected administrator expansion and bot identity, got %+v", administrator)
	}

	owner := discordMemberAuthorization(guild, &discordgo.Member{User: &discordgo.User{ID: "owner", Username: "Owner"}})
	if owner.PermissionBits&uint64(discordgo.PermissionAdministrator) == 0 {
		t.Fatalf("expected guild owner full permission expansion, got %+v", owner)
	}
}

func TestGuildAuthorizationErrorPreservesMissingGuildSentinel(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		err := guildAuthorizationError(&discordgo.RESTError{Response: &http.Response{StatusCode: status}})
		if !errors.Is(err, quack.ErrBotNotInGuild) {
			t.Fatalf("status %d: expected bot-not-in-guild sentinel, got %v", status, err)
		}
	}
	transient := guildAuthorizationError(&discordgo.RESTError{Response: &http.Response{StatusCode: http.StatusInternalServerError}})
	if !errors.Is(transient, quack.ErrAuthorizationUnavailable) {
		t.Fatalf("expected transient failure to remain unavailable, got %v", transient)
	}
}
