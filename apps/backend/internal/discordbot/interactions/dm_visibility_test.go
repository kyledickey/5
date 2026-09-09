package interactions_test

import (
	"context"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// TestDMRepliesNeverCarryEphemeralFlags checks both acknowledgement and followup
// transport, even when a reused handler asks for a private server-style response.
func TestDMRepliesNeverCarryEphemeralFlags(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate", true: "deferred"}[deferred], func(t *testing.T) {
			client := &fakeClient{}
			done := make(chan struct{})
			dispatcher := &interactions.Dispatcher{Client: client, Commands: fakeCommands{"dm": func(ui.Context) ui.HandlerResult {
				if !deferred {
					return ui.Immediate(ui.Error("Please try again."))
				}
				return ui.Async(ui.DeferEphemeral(), func(_ context.Context, r ui.Responder) error {
					defer close(done)
					_, err := r.Followup(ui.Content("Please try again.", true))
					return err
				})
			}}}
			interaction := commandInteraction("dm", discordgo.InteractionApplicationCommand)
			interaction.GuildID = ""
			dispatcher.Handle(nil, interaction)
			if deferred {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("DM task timed out")
				}
			}
			if len(client.responses) != 1 || client.responses[0].Data != nil && client.responses[0].Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
				t.Fatal("DM acknowledgement carried ephemeral flag")
			}
			if deferred && (len(client.followups) != 1 || client.followups[0].Flags&discordgo.MessageFlagsEphemeral != 0) {
				t.Fatal("DM followup carried ephemeral flag")
			}
		})
	}
}
