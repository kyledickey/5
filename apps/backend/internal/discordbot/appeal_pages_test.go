package discordbot

import (
	"context"
	"strings"
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
		message := &discordgo.Message{ID: "queue", Content: "PRIVATE STATEMENT"}
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
		if err := result.Task(context.Background(), responder); err != nil {
			t.Fatalf("permission error was not rendered: %v", err)
		}
		if !responder.lastEdit.PrivateError || responder.edits != 1 || !strings.Contains(responder.content, "Moderate Members") {
			t.Fatalf("missing private permission feedback: %+v", responder)
		}
		if strings.Contains(responder.content, "PRIVATE STATEMENT") || strings.Contains(responder.content, "Received an appeal") || (responder.lastEdit.Components != nil && len(*responder.lastEdit.Components) != 0) {
			t.Fatal("revoked moderator received statement content or controls")
		}
		if responder.followups != 0 || message.Content != "PRIVATE STATEMENT" {
			t.Fatal("statement browsing changed the shared queue")
		}
	}
}
