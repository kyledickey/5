package ui

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// Context is what a Handler receives for one interaction: the trace-bearing
// context.Context, application services, the gateway session and the raw
// interaction. Services and Session may be nil in unit tests that only assert
// on the immediate response.
type Context struct {
	context.Context
	Services    *quack.Services
	Session     *discordgo.Session
	Interaction *discordgo.InteractionCreate
}

// Responder is what an asynchronous Task uses to talk back to Discord after the
// acknowledgement has been sent. EditOriginal, Followup, EditFollowup and
// DeleteOriginal use the interaction webhook token, which expires 15 minutes
// after the interaction; PublishChannel and EditChannel use bot credentials
// and have no such lifetime.
type Responder interface {
	// PublishChannel sends a standalone public notice in the originating channel.
	PublishChannel(context.Context, Message) (*discordgo.Message, error)
	// EditChannel updates that notice with bot credentials, not a webhook token.
	EditChannel(context.Context, string, Edit) (*discordgo.Message, error)
	EditOriginal(Edit) (*discordgo.Message, error)
	Followup(Message) (*discordgo.Message, error)
	EditFollowup(string, Edit) (*discordgo.Message, error)
	DeleteOriginal() error
	UpdateMessage(Edit) (*discordgo.Message, error)
}

// Task is the deferred half of an Async handler. It runs on its own goroutine
// after the acknowledgement was accepted; a returned error is reported privately
// to the invoking user by the dispatcher (see interactions.Dispatcher).
type Task func(context.Context, Responder) error

// HandlerResult tells the dispatcher how to acknowledge an interaction and whether deferred work follows.
type HandlerResult struct {
	Response *discordgo.InteractionResponse
	Task     Task
}

// Handler is the entry point for one command, component or modal interaction.
// It must return within Discord's three-second acknowledgement window, so slow
// work belongs in the Task of an Async result.
type Handler func(Context) HandlerResult

// Immediate answers the interaction with response and schedules no further work.
func Immediate(response *discordgo.InteractionResponse) HandlerResult {
	return HandlerResult{Response: response}
}

// Async sends response (normally a Defer* acknowledgement) and then runs task.
func Async(response *discordgo.InteractionResponse, task Task) HandlerResult {
	return HandlerResult{Response: response, Task: task}
}

// AsyncPublic keeps successful command results on the original public response.
// Discord fixes visibility at acknowledgement, so failures remove the pending
// public response and send one private error instead of exposing its details.
func AsyncPublic(task Task) HandlerResult {
	return Async(DeferPublic(), func(ctx context.Context, responder Responder) error {
		return task(ctx, publicCommandResponder{Responder: responder})
	})
}

// publicCommandResponder changes only error delivery; normal results retain the
// original interaction message and its command attribution.
type publicCommandResponder struct{ Responder }

// EditOriginal prevents an error from inheriting a public acknowledgement.
func (r publicCommandResponder) EditOriginal(edit Edit) (*discordgo.Message, error) {
	if !edit.PrivateError {
		return r.Responder.EditOriginal(edit)
	}
	if err := r.Responder.DeleteOriginal(); err != nil {
		return nil, err
	}
	message := Message{Ephemeral: true, Files: edit.Files, AllowedMentions: edit.AllowedMentions}
	if edit.Content != nil {
		message.Content = *edit.Content
	}
	if edit.Embeds != nil {
		message.Embeds = *edit.Embeds
	}
	if edit.Components != nil {
		message.Components = *edit.Components
	}
	return r.Responder.Followup(message)
}

// Public builds a visible channel reply, clearing any ephemeral flag on message.
func Public(message Message) *discordgo.InteractionResponse {
	message.Ephemeral = false
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: message.ResponseData(),
	}
}

// Ephemeral builds a reply only the invoking user can see.
func Ephemeral(message Message) *discordgo.InteractionResponse {
	message.Ephemeral = true
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: message.ResponseData(),
	}
}

// DeferPublic acknowledges with a visible "thinking" placeholder that a Task
// later replaces with EditOriginal or Publish.
func DeferPublic() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}
}

// DeferEphemeral acknowledges with a placeholder only the invoking user can see.
func DeferEphemeral() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	}
}

// DeferUpdate acknowledges a component click without changing the message it
// belongs to; the Task then edits that message with UpdateMessage.
func DeferUpdate() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	}
}

// Update immediately replaces the message a component belongs to.
func Update(message Message) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: message.ResponseData(),
	}
}

// Autocomplete answers an autocomplete interaction with up to 25 choices.
func Autocomplete(choices []*discordgo.ApplicationCommandOptionChoice) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{
			Choices: choices,
		},
	}
}

// Modal opens a form. It must be the initial response, so handlers that need a
// form cannot defer first.
func Modal(title, customID string, components []discordgo.MessageComponent) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			Title:      title,
			CustomID:   customID,
			Components: components,
		},
	}
}

// Error builds an ephemeral reply carrying content behind the error icon.
func Error(content string) *discordgo.InteractionResponse {
	return Ephemeral(Signal("error", content, true))
}

// ErrorEdit builds an edit carrying content behind the error icon and marks it
// PrivateError so AsyncPublic routes it to the invoking user instead of the channel.
func ErrorEdit(content string) Edit {
	edit := EditMessage(Signal("error", content, false))
	edit.PrivateError = true
	return edit
}

// Publish replaces a public deferred response in place, preserving Discord's
// command attribution. Callers must acknowledge with DeferPublic first because
// Discord fixes the response visibility when the interaction is acknowledged.
func Publish(responder Responder, message Message) (*discordgo.Message, error) {
	return responder.EditOriginal(EditMessage(message))
}

// UserError carries a sentence that is meant to be shown to the person who
// invoked a command, unchanged. It exists so Discord-facing copy ("Could not
// create #appeals. Quack needs Manage Channels permission.") can travel through
// an error return without being reduced to a lowercase log string.
//
// Error returns Message verbatim: callers in other packages already surface
// SetupChannel failures with err.Error(), and the copy they show must not change.
// Callers that want to distinguish user copy from internal failures use UserMessage.
type UserError struct {
	Message string
}

func (e *UserError) Error() string {
	return e.Message
}

// UserMessage reports whether err (or any error it wraps) is a UserError and,
// if so, returns the copy that should be shown to the invoking user.
func UserMessage(err error) (string, bool) {
	var user *UserError
	if errors.As(err, &user) {
		return user.Message, true
	}
	return "", false
}
