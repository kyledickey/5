// Package ui owns the Discord response model shared by every command, component
// and worker in the bot: Message and Edit, the handler contract (Context,
// Handler, HandlerResult, Task, Responder), custom-ID routing for components,
// embed and button builders, text pagination, command-mention resolution and
// SetupChannel. It depends on discordgo and discordtext only; it must not import
// quack services, commands, interactions or views.
//
// Response lifecycle. A Handler returns a HandlerResult: Immediate sends one
// InteractionResponse and finishes; Async sends the acknowledgement (DeferPublic,
// DeferEphemeral or DeferUpdate) and then runs a Task on a goroutine with a
// Responder. Discord fixes a reply's visibility at acknowledgement time, so a
// Task cannot make a public defer private. AsyncPublic encodes the rule used by
// every slash command: success edits the original public response in place
// (Publish), while an error marked with ErrorEdit deletes the public placeholder
// and sends one ephemeral followup instead. Errors are never edited into a shared
// message. In DMs the dispatcher strips ephemeral flags because Discord rejects them.
//
// UserError carries copy that is safe to show to the invoking user; every other
// error is internal and must be mapped to copy by the caller.
package ui
