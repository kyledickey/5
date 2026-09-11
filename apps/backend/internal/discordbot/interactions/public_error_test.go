package interactions_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// TestUnexpectedPublicTaskErrorsStayPrivate protects both an unfinished defer and
// a successfully published result when later asynchronous work fails.
func TestUnexpectedPublicTaskErrorsStayPrivate(t *testing.T) {
	for _, published := range []bool{false, true} {
		client := &fakeClient{done: make(chan struct{}, 2)}
		dispatcher := &interactions.Dispatcher{Client: client, Commands: fakeCommands{"test": func(ui.Context) ui.HandlerResult {
			return ui.AsyncPublic(func(_ context.Context, r ui.Responder) error {
				if published {
					if _, err := r.EditOriginal(ui.EditMessage(ui.Content("Saved.", false))); err != nil {
						return err
					}
				}
				return errors.New("PRIVATE DATABASE ERROR")
			})
		}}}
		dispatcher.Handle(nil, commandInteraction("test", discordgo.InteractionApplicationCommand))
		client.wait(t)
		if published {
			client.wait(t)
		}
		if len(client.followups) != 1 || client.followups[0].Flags&discordgo.MessageFlagsEphemeral == 0 {
			t.Fatal("error was not private")
		}
		if published && (client.deleted != 0 || len(client.edits) != 1 || *client.edits[0].Content != "Saved.") {
			t.Fatal("committed result lost")
		}
		if !published && (client.deleted != 1 || len(client.edits) != 0) {
			t.Fatal("public error or orphan defer")
		}
	}
}
