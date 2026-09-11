package interactions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestDispatcherSendsImmediateCommandResponse proves an Immediate result is sent
// once and schedules no edits.
func TestDispatcherSendsImmediateCommandResponse(t *testing.T) {
	client := &fakeClient{}
	dispatcher := &interactions.Dispatcher{
		Commands: fakeCommands{
			"ping": func(ctx ui.Context) ui.HandlerResult {
				return ui.Immediate(ui.Public(ui.Content("pong", false)))
			},
		},
		Client: client,
	}

	dispatcher.Handle(nil, commandInteraction("ping", discordgo.InteractionApplicationCommand))

	if len(client.responses) != 1 {
		t.Fatalf("expected one response, got %d", len(client.responses))
	}
	if client.responses[0].Data.Content != "pong" {
		t.Fatalf("unexpected response: %+v", client.responses[0])
	}
	if len(client.edits) != 0 {
		t.Fatalf("expected no edits, got %d", len(client.edits))
	}
}

// TestDispatcherAddsDiscordTraceContext proves both the handler and its async
// task see request and correlation IDs derived from the interaction ID.
func TestDispatcherAddsDiscordTraceContext(t *testing.T) {
	client := &fakeClient{done: make(chan struct{}, 1)}
	dispatcher := &interactions.Dispatcher{
		Commands: fakeCommands{
			"trace": func(ctx ui.Context) ui.HandlerResult {
				if quack.RequestIDFromContext(ctx.Context) != "discord:interaction-1" ||
					quack.CorrelationIDFromContext(ctx.Context) != "discord:interaction-1" {
					t.Fatalf("expected discord trace context, got request=%q correlation=%q",
						quack.RequestIDFromContext(ctx.Context), quack.CorrelationIDFromContext(ctx.Context))
				}
				return ui.Async(ui.DeferPublic(), func(ctx context.Context, responder ui.Responder) error {
					if quack.RequestIDFromContext(ctx) != "discord:interaction-1" ||
						quack.CorrelationIDFromContext(ctx) != "discord:interaction-1" {
						t.Fatalf("expected async discord trace context, got request=%q correlation=%q",
							quack.RequestIDFromContext(ctx), quack.CorrelationIDFromContext(ctx))
					}
					_, err := responder.EditOriginal(ui.EditMessage(ui.Content("traced", false)))
					return err
				})
			},
		},
		Client: client,
	}

	dispatcher.Handle(nil, commandInteraction("trace", discordgo.InteractionApplicationCommand))
	client.wait(t)
}

// TestDispatcherDefersThenEditsAsyncCommand proves an Async result sends the
// deferred acknowledgement and then the task's edit.
func TestDispatcherDefersThenEditsAsyncCommand(t *testing.T) {
	client := &fakeClient{done: make(chan struct{}, 1)}
	dispatcher := &interactions.Dispatcher{
		Commands: fakeCommands{
			"slow": func(ctx ui.Context) ui.HandlerResult {
				return ui.Async(ui.DeferPublic(), func(ctx context.Context, responder ui.Responder) error {
					_, err := responder.EditOriginal(ui.EditMessage(ui.Content("finished", false)))
					return err
				})
			},
		},
		Client: client,
	}

	dispatcher.Handle(nil, commandInteraction("slow", discordgo.InteractionApplicationCommand))
	client.wait(t)

	if len(client.responses) != 1 || client.responses[0].Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected one deferred response, got %+v", client.responses)
	}
	if len(client.edits) != 1 || client.edits[0].Content == nil || *client.edits[0].Content != "finished" {
		t.Fatalf("expected final edit, got %+v", client.edits)
	}
}

// TestDispatcherConvertsAsyncErrorsToErrorEdit proves a task error after a public
// defer deletes the placeholder and sends one private followup.
func TestDispatcherConvertsAsyncErrorsToErrorEdit(t *testing.T) {
	client := &fakeClient{done: make(chan struct{}, 1)}
	dispatcher := &interactions.Dispatcher{
		Commands: fakeCommands{
			"slow": func(ctx ui.Context) ui.HandlerResult {
				return ui.Async(ui.DeferPublic(), func(ctx context.Context, responder ui.Responder) error {
					return errors.New("boom")
				})
			},
		},
		Client: client,
	}

	dispatcher.Handle(nil, commandInteraction("slow", discordgo.InteractionApplicationCommand))
	client.wait(t)

	if len(client.edits) != 0 || client.deleted != 1 || len(client.followups) != 1 ||
		client.followups[0].Flags&discordgo.MessageFlagsEphemeral == 0 ||
		client.followups[0].Content != "I couldn’t finish that. Try again in a moment." {
		t.Fatalf("expected a private error after removing the public defer: %+v", client)
	}
}

// TestDispatcherRoutesComponentsAndModals proves custom IDs reach the handlers
// registered for their namespace and action.
func TestDispatcherRoutesComponentsAndModals(t *testing.T) {
	client := &fakeClient{}
	registry := interactions.NewComponentRegistry()
	if err := registry.RegisterComponent("case", "next", func(ctx ui.Context) ui.HandlerResult {
		return ui.Immediate(ui.Update(ui.Content("next page", false)))
	}); err != nil {
		t.Fatalf("register component: %v", err)
	}
	if err := registry.RegisterModal("case", "note", func(ctx ui.Context) ui.HandlerResult {
		return ui.Immediate(ui.Ephemeral(ui.Content("saved", true)))
	}); err != nil {
		t.Fatalf("register modal: %v", err)
	}

	dispatcher := &interactions.Dispatcher{Components: registry, Client: client}
	dispatcher.Handle(nil, componentInteraction("case:next:v1:user=1"))
	dispatcher.Handle(nil, modalInteraction("case:note:v1:user=1"))

	if len(client.responses) != 2 {
		t.Fatalf("expected two responses, got %d", len(client.responses))
	}
	if client.responses[0].Type != discordgo.InteractionResponseUpdateMessage {
		t.Fatalf("expected component update response, got %v", client.responses[0].Type)
	}
	if client.responses[1].Data.Content != "saved" {
		t.Fatalf("expected modal response, got %+v", client.responses[1])
	}
}

// fakeCommands is an in-memory CommandLookup keyed by command name.
type fakeCommands map[string]ui.Handler

func (f fakeCommands) LookupCommand(name string) (ui.Handler, bool) {
	handler, ok := f[name]
	return handler, ok
}

// fakeClient records every Discord call the dispatcher makes and, when done is
// set, signals after each edit or followup so tests can wait for async tasks.
// It is not safe for concurrent use; the race test uses a real session instead.
type fakeClient struct {
	responses []*discordgo.InteractionResponse
	edits     []*discordgo.WebhookEdit
	followups []*discordgo.WebhookParams
	deleted   int
	done      chan struct{}
}

func (f *fakeClient) InteractionRespond(interaction *discordgo.Interaction, response *discordgo.InteractionResponse) error {
	f.responses = append(f.responses, response)
	return nil
}

func (f *fakeClient) InteractionResponseEdit(interaction *discordgo.Interaction, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
	f.edits = append(f.edits, edit)
	if f.done != nil {
		f.done <- struct{}{}
	}
	return &discordgo.Message{ID: "message-1"}, nil
}

func (f *fakeClient) FollowupMessageCreate(interaction *discordgo.Interaction, wait bool, params *discordgo.WebhookParams) (*discordgo.Message, error) {
	f.followups = append(f.followups, params)
	if f.done != nil {
		f.done <- struct{}{}
	}
	return &discordgo.Message{ID: "followup-1"}, nil
}

func (f *fakeClient) FollowupMessageEdit(interaction *discordgo.Interaction, messageID string, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
	f.edits = append(f.edits, edit)
	return &discordgo.Message{ID: messageID}, nil
}

func (f *fakeClient) InteractionResponseDelete(interaction *discordgo.Interaction) error {
	f.deleted++
	return nil
}

// wait blocks until the async task under test has edited or followed up.
func (f *fakeClient) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for async task")
	}
}

// commandInteraction builds a guild slash-command interaction for name.
func commandInteraction(name string, interactionType discordgo.InteractionType) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID:      "interaction-1",
		Type:    interactionType,
		GuildID: "guild-1",
		Data: discordgo.ApplicationCommandInteractionData{
			Name: name,
		},
	}}
}

// componentInteraction builds a guild button click carrying customID.
func componentInteraction(customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID:      "interaction-1",
		Type:    discordgo.InteractionMessageComponent,
		GuildID: "guild-1",
		Data: discordgo.MessageComponentInteractionData{
			CustomID: customID,
		},
	}}
}

// modalInteraction builds a guild modal submission carrying customID.
func modalInteraction(customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID:      "interaction-2",
		Type:    discordgo.InteractionModalSubmit,
		GuildID: "guild-1",
		Data: discordgo.ModalSubmitInteractionData{
			CustomID: customID,
		},
	}}
}

// TestComponentTaskErrorPreservesSharedMessage prevents private action failures
// from replacing the permanent case view for everyone in the channel.
func TestComponentTaskErrorPreservesSharedMessage(t *testing.T) {
	client := &fakeClient{done: make(chan struct{}, 1)}
	registry := interactions.NewComponentRegistry()
	if err := registry.RegisterComponent("case", "list_next", func(ui.Context) ui.HandlerResult {
		return ui.Async(ui.DeferUpdate(), func(context.Context, ui.Responder) error { return errors.New("permission denied") })
	}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &interactions.Dispatcher{Client: client, Components: registry}
	dispatcher.Handle(nil, componentInteraction("case:list_next:v1:1"))
	client.wait(t)
	if len(client.edits) != 0 || len(client.followups) != 1 || client.followups[0].Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected private error without editing shared result: %+v", client)
	}
}

// TestPermissionFailuresExplainDenialPrivately distinguishes ordinary denied
// access from an unexpected failure without exposing shared message contents.
func TestPermissionFailuresExplainDenialPrivately(t *testing.T) {
	for _, update := range []bool{false, true} {
		client := &fakeClient{done: make(chan struct{}, 1)}
		registry := interactions.NewComponentRegistry()
		if err := registry.RegisterComponent("case", "evidence", func(ui.Context) ui.HandlerResult {
			response := ui.DeferEphemeral()
			if update {
				response = ui.DeferUpdate()
			}
			return ui.Async(response, func(context.Context, ui.Responder) error { return quack.ErrCasePermissionDenied })
		}); err != nil {
			t.Fatal(err)
		}
		dispatcher := &interactions.Dispatcher{Client: client, Components: registry}
		dispatcher.Handle(nil, componentInteraction("case:evidence:v1:case"))
		client.wait(t)
		const want = "You do not have permission to use this control."
		if update {
			if len(client.edits) != 0 || len(client.followups) != 1 || client.followups[0].Content != want ||
				client.followups[0].Flags&discordgo.MessageFlagsEphemeral == 0 {
				t.Fatal("denial must be a private followup")
			}
		} else if len(client.edits) != 1 || client.edits[0].Content == nil || *client.edits[0].Content != want {
			t.Fatal("missing private permission explanation")
		}
	}
}

// ChannelMessageSend records standalone bot messages at the interaction channel.
func (f *fakeClient) ChannelMessageSend(_ context.Context, channelID string, message *discordgo.MessageSend) (*discordgo.Message, error) {
	return &discordgo.Message{ID: "channel-message", ChannelID: channelID}, nil
}

// ChannelMessageEdit implements the bot-message edit dependency for dispatcher tests.
func (f *fakeClient) ChannelMessageEdit(_ context.Context, edit *discordgo.MessageEdit) (*discordgo.Message, error) {
	return &discordgo.Message{ID: edit.ID, ChannelID: edit.Channel}, nil
}
