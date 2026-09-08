package discordbot

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

// TestStatementBrowsingIsPrivateAndRechecksAuthority verifies shared queue clicks
// open privately and old private pages cannot retain revoked staff permissions.
func TestStatementBrowsingIsPrivateAndRechecksAuthority(t *testing.T) {
	repository := testutil.NewSQLiteStore(t)
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	services := &quack.Services{Guilds: quack.NewGuildService(repository, &appealReviewAuthorization{})}
	appeals := quack.NewAppealService(repository)
	for _, private := range []bool{false, true} {
		message := &discordgo.Message{}
		if private {
			message.Flags = discordgo.MessageFlagsEphemeral
		}
		interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "guild", Member: &discordgo.Member{User: &discordgo.User{ID: "mod"}, Permissions: int64(discordgo.PermissionModerateMembers)}, Message: message, Data: discordgo.MessageComponentInteractionData{CustomID: "appeal:statement_next:v1:1|appeal"}}}
		result := appealStatementPage(services, appeals, 1)(ui.Context{Context: context.Background(), Interaction: interaction})
		if result.Task == nil {
			t.Fatal("missing deferred statement read")
		}
		if private {
			if result.Response.Type != discordgo.InteractionResponseDeferredMessageUpdate {
				t.Fatal("private reading position was not updated")
			}
		} else if result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
			t.Fatal("shared queue browsing was not private")
		}
		responder := &appealTestResponder{}
		if err := result.Task(context.Background(), responder); !errors.Is(err, quack.ErrAppealPermissionDenied) {
			t.Fatalf("stale permissions were trusted: %v", err)
		}
		if responder.content != "" {
			t.Fatal("revoked moderator received statement content")
		}
	}
}
