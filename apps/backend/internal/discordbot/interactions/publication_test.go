package interactions_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// TestPublicResultAndEnforcementUpdateKeepOriginalResponse covers public thinking,
// result replacement, later enforcement updates, and independent private errors.
func TestPublicResultAndEnforcementUpdateKeepOriginalResponse(t *testing.T) {
	client := &fakeClient{}
	done := make(chan struct{})
	dispatcher := &interactions.Dispatcher{Client: client, Commands: fakeCommands{
		"case": func(ui.Context) ui.HandlerResult {
			return ui.Async(ui.DeferPublic(), func(_ context.Context, responder ui.Responder) error {
				defer close(done)
				if _, err := ui.Publish(responder, ui.Signal("pending", "The timeout is queued.", false)); err != nil {
					return err
				}
				if _, err := responder.EditOriginal(ui.EditMessage(ui.Signal("timeout", "The timeout is in place.", false))); err != nil {
					return err
				}
				_, err := responder.Followup(ui.Signal("error", "A private note.", true))
				return err
			})
		},
	}}
	interaction := commandInteraction("case", discordgo.InteractionApplicationCommand)
	interaction.AppID, interaction.ChannelID = "819019613371236432", "testing"
	dispatcher.Handle(nil, interaction)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publication timed out")
	}
	if len(client.responses) != 1 || client.responses[0].Type != discordgo.InteractionResponseDeferredChannelMessageWithSource ||
		(client.responses[0].Data != nil && client.responses[0].Data.Flags&discordgo.MessageFlagsEphemeral != 0) {
		t.Fatal("thinking acknowledgement was not public")
	}
	if client.deleted != 0 || len(client.edits) != 2 {
		t.Fatalf("original response was replaced: %+v", client)
	}
	for i, icon := range []string{"pending", "timeout"} {
		if client.edits[i].Content == nil || !strings.Contains(*client.edits[i].Content, "<:quack_"+icon+":") {
			t.Fatalf("missing in-place result %d", i)
		}
	}
	if len(client.followups) != 1 || client.followups[0].Flags&discordgo.MessageFlagsEphemeral == 0 ||
		!strings.Contains(client.followups[0].Content, "<:quack_error:") {
		t.Fatal("private reply leaked to channel")
	}
}
