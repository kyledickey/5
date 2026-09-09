package ui

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// Context carries the request-scoped context data needed by downstream logic.
type Context struct {
	context.Context
	Services    *quack.Services
	Session     *discordgo.Session
	Interaction *discordgo.InteractionCreate
}

// Responder exposes the Discord response operations available to asynchronous interaction tasks.
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

// Task performs deferred interaction work after the acknowledgement has been sent to Discord.
type Task func(context.Context, Responder) error

// HandlerResult tells the dispatcher how to acknowledge an interaction and whether deferred work follows.
type HandlerResult struct {
	Response *discordgo.InteractionResponse
	Task     Task
}

// Handler handles one unit of work through the package's transport-neutral callback contract.
type Handler func(Context) HandlerResult

// Immediate encapsulates the immediate rule so callers share one consistent package implementation.
func Immediate(response *discordgo.InteractionResponse) HandlerResult {
	return HandlerResult{Response: response}
}

// Async encapsulates the async rule so callers share one consistent package implementation.
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

// Public encapsulates the public rule so callers share one consistent package implementation.
func Public(message Message) *discordgo.InteractionResponse {
	message.Ephemeral = false
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: message.ResponseData(),
	}
}

// Ephemeral encapsulates the ephemeral rule so callers share one consistent package implementation.
func Ephemeral(message Message) *discordgo.InteractionResponse {
	message.Ephemeral = true
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: message.ResponseData(),
	}
}

// DeferPublic encapsulates the defer public rule so callers share one consistent package implementation.
func DeferPublic() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}
}

// DeferEphemeral encapsulates the defer ephemeral rule so callers share one consistent package implementation.
func DeferEphemeral() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	}
}

// DeferUpdate encapsulates the defer update rule so callers share one consistent package implementation.
func DeferUpdate() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	}
}

// Update updates update while retaining validation, compatibility, and audit requirements.
func Update(message Message) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: message.ResponseData(),
	}
}

// Autocomplete encapsulates the autocomplete rule so callers share one consistent package implementation.
func Autocomplete(choices []*discordgo.ApplicationCommandOptionChoice) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{
			Choices: choices,
		},
	}
}

// Modal encapsulates the modal rule so callers share one consistent package implementation.
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

// Error formats - as a standard Go error without discarding its classification.
func Error(content string) *discordgo.InteractionResponse {
	return Ephemeral(Signal("error", content, true))
}

// ErrorEdit encapsulates the error edit rule so callers share one consistent package implementation.
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
