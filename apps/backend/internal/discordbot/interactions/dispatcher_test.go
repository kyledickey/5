package interactions_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/idutil"
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
				if idutil.RequestIDFromContext(ctx.Context) != "discord:interaction-1" ||
					idutil.CorrelationIDFromContext(ctx.Context) != "discord:interaction-1" {
					t.Fatalf("expected discord trace context, got request=%q correlation=%q",
						idutil.RequestIDFromContext(ctx.Context), idutil.CorrelationIDFromContext(ctx.Context))
				}
				return ui.Async(ui.DeferPublic(), func(ctx context.Context, responder ui.Responder) error {
					if idutil.RequestIDFromContext(ctx) != "discord:interaction-1" ||
						idutil.CorrelationIDFromContext(ctx) != "discord:interaction-1" {
						t.Fatalf("expected async discord trace context, got request=%q correlation=%q",
							idutil.RequestIDFromContext(ctx), idutil.CorrelationIDFromContext(ctx))
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

// acceptingTransport answers every interaction callback with 204 and counts
// them atomically, standing in for Discord during concurrent dispatch.
type acceptingTransport struct{ responses atomic.Int64 }

// RoundTrip records one accepted callback.
func (t *acceptingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.responses.Add(1)
	return &http.Response{StatusCode: 204, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
}

// TestDispatcherHandlesConcurrentInteractionsWithLazyClient proves that a
// dispatcher whose Client and Deduper are left nil, as commands.Register leaves
// them, can be driven from many gateway goroutines at once: the session-backed
// client and the deduper are created once, every distinct interaction is
// answered, and the race detector stays quiet.
func TestDispatcherHandlesConcurrentInteractionsWithLazyClient(t *testing.T) {
	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	transport := &acceptingTransport{}
	session.Client = &http.Client{Transport: transport}
	var handled atomic.Int64
	dispatcher := &interactions.Dispatcher{Commands: fakeCommands{
		"ping": func(ui.Context) ui.HandlerResult {
			handled.Add(1)
			return ui.Immediate(ui.Public(ui.Content("pong", false)))
		},
	}}

	const workers = 64
	var wait sync.WaitGroup
	for i := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			interaction := commandInteraction("ping", discordgo.InteractionApplicationCommand)
			interaction.ID = "interaction-" + string(rune('A'+i%26)) + string(rune('a'+i/26))
			dispatcher.Handle(session, interaction)
		}()
	}
	wait.Wait()

	if handled.Load() != workers || transport.responses.Load() != workers {
		t.Fatalf("handled=%d responses=%d, want %d each", handled.Load(), transport.responses.Load(), workers)
	}
	if dispatcher.Client == nil || dispatcher.Deduper == nil {
		t.Fatal("lazy client or deduper was not installed")
	}
}

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
			if len(client.responses) != 1 ||
				client.responses[0].Data != nil && client.responses[0].Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
				t.Fatal("DM acknowledgement carried ephemeral flag")
			}
			if deferred && (len(client.followups) != 1 || client.followups[0].Flags&discordgo.MessageFlagsEphemeral != 0) {
				t.Fatal("DM followup carried ephemeral flag")
			}
		})
	}
}

// failingResponseClient reproduces net/http's credential-bearing URL error.
type failingResponseClient struct {
	fakeClient
	err error
}

// InteractionRespond fails every initial response with the configured error.
func (c *failingResponseClient) InteractionRespond(*discordgo.Interaction, *discordgo.InteractionResponse) error {
	return c.err
}

// TestDispatcherLogsNeverExposeWebhookCredentials proves response, task and
// panic logs omit the interaction token even when the error text contains it.
func TestDispatcherLogsNeverExposeWebhookCredentials(t *testing.T) {
	const secret = "private-interaction-token"
	transportErr := &url.Error{
		Op:  "Post",
		URL: "https://discord.com/api/v10/webhooks/application/" + secret,
		Err: errors.New("connection reset"),
	}
	prior := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prior) })
	for _, mode := range []string{"response", "task", "panic"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			client := &fakeClient{done: make(chan struct{}, 1)}
			dispatcher := &interactions.Dispatcher{Client: client}
			dispatcher.Commands = fakeCommands{"test": func(ui.Context) ui.HandlerResult {
				if mode == "response" {
					return ui.Immediate(ui.Public(ui.Content("done", false)))
				}
				return ui.Async(ui.DeferEphemeral(), func(context.Context, ui.Responder) error {
					if mode == "panic" {
						panic(secret)
					}
					return transportErr
				})
			}}
			if mode == "response" {
				dispatcher.Client = &failingResponseClient{err: transportErr}
			}
			dispatcher.Handle(nil, commandInteraction("test", discordgo.InteractionApplicationCommand))
			if mode != "response" {
				client.wait(t)
			}
			if strings.Contains(output.String(), secret) || output.Len() == 0 {
				t.Fatalf("unsafe or missing operational log: %s", output.String())
			}
		})
	}
}

// TestDiscordRejectionLogsIncludeSafeDiagnostics makes initial-response failures
// actionable while protecting the error body's potentially sensitive content.
func TestDiscordRejectionLogsIncludeSafeDiagnostics(t *testing.T) {
	const secret = "private-interaction-token"
	var output bytes.Buffer
	prior := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prior) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	client := &failingResponseClient{err: &discordgo.RESTError{
		Response:     &http.Response{StatusCode: 404},
		Message:      &discordgo.APIErrorMessage{Code: 10062, Message: secret},
		ResponseBody: []byte(secret),
	}}
	dispatcher := &interactions.Dispatcher{Client: client, Commands: fakeCommands{"test": func(ui.Context) ui.HandlerResult {
		return ui.Immediate(ui.DeferEphemeral())
	}}}
	dispatcher.Handle(nil, commandInteraction("test", discordgo.InteractionApplicationCommand))
	logged := output.String()
	for _, want := range []string{`"http_status":404`, `"discord_code":10062`, `"interaction_type":2`, `"response_type":5`, `"elapsed_ms":`} {
		if !strings.Contains(logged, want) {
			t.Fatalf("missing %s in %s", want, logged)
		}
	}
	if strings.Contains(logged, secret) {
		t.Fatalf("logged response secrets: %s", logged)
	}
}

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
