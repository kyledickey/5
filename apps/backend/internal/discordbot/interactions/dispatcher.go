package interactions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// CommandLookup resolves a slash, user or message command name to its handler.
// The commands package's Registry implements it.
type CommandLookup interface {
	LookupCommand(name string) (ui.Handler, bool)
}

// Client is the slice of the Discord REST API the dispatcher and its Responder
// need. Production uses sessionClient over a discordgo.Session; tests substitute
// an in-memory fake.
type Client interface {
	ChannelMessageSend(context.Context, string, *discordgo.MessageSend) (*discordgo.Message, error)
	ChannelMessageEdit(context.Context, *discordgo.MessageEdit) (*discordgo.Message, error)
	InteractionRespond(*discordgo.Interaction, *discordgo.InteractionResponse) error
	InteractionResponseEdit(*discordgo.Interaction, *discordgo.WebhookEdit) (*discordgo.Message, error)
	FollowupMessageCreate(*discordgo.Interaction, bool, *discordgo.WebhookParams) (*discordgo.Message, error)
	FollowupMessageEdit(*discordgo.Interaction, string, *discordgo.WebhookEdit) (*discordgo.Message, error)
	InteractionResponseDelete(*discordgo.Interaction) error
}

// Dispatcher routes Discord interactions to registered handlers and applies the
// response lifecycle described in the package documentation. Handle is invoked
// concurrently by discordgo, so every lazily initialised field is guarded by mu.
//
// Commands and Components are optional: an interaction whose kind has no lookup
// is ignored (commands) or answered with a generic error (components). Client
// defaults to the session Handle receives; Deduper defaults to a process-local
// InteractionDeduper. Both defaults are created once, under mu.
type Dispatcher struct {
	Services   *quack.Services
	Commands   CommandLookup
	Components *ComponentRegistry
	Client     Client
	Deduper    *InteractionDeduper
	mu         sync.Mutex
}

// NewDispatcher wires a dispatcher with an empty component registry and a
// process-local deduper. Client is filled in from the gateway session on the
// first interaction unless the caller sets it first.
func NewDispatcher(services *quack.Services, commands CommandLookup) *Dispatcher {
	return &Dispatcher{
		Services:   services,
		Commands:   commands,
		Components: NewComponentRegistry(),
		Deduper:    NewInteractionDeduper(15*time.Minute, 10000),
	}
}

// Handle is the discordgo event handler for InteractionCreate. It is safe to
// call concurrently. Nil interactions and unsupported interaction types are ignored.
func (d *Dispatcher) Handle(session *discordgo.Session, interaction *discordgo.InteractionCreate) {
	if interaction == nil || interaction.Interaction == nil {
		return
	}
	d.ensureClient(session)

	switch interaction.Type {
	case discordgo.InteractionApplicationCommand, discordgo.InteractionApplicationCommandAutocomplete:
		d.handleCommand(session, interaction)
	case discordgo.InteractionMessageComponent:
		d.handleComponent(session, interaction)
	case discordgo.InteractionModalSubmit:
		d.handleModal(session, interaction)
	}
}

// ensureClient installs a session-backed Client exactly once when none was
// configured. It takes mu because Handle runs on discordgo's event goroutines.
func (d *Dispatcher) ensureClient(session *discordgo.Session) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Client == nil {
		d.Client = sessionClient{session: session}
	}
}

// client reads the configured Client under mu so it is never observed half-written.
func (d *Dispatcher) client() Client {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Client
}

// interactionDeduper returns the configured deduper, creating the process-local
// default once when the caller left Deduper nil.
func (d *Dispatcher) interactionDeduper() *InteractionDeduper {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Deduper == nil {
		d.Deduper = NewInteractionDeduper(15*time.Minute, 10000)
	}
	return d.Deduper
}

// handleCommand looks up the command by name and executes it. Unknown commands
// and command interactions with unexpected data are dropped without a reply.
func (d *Dispatcher) handleCommand(session *discordgo.Session, interaction *discordgo.InteractionCreate) {
	if d.Commands == nil {
		return
	}
	data, valid := interaction.Data.(discordgo.ApplicationCommandInteractionData)
	if !valid {
		return
	}
	handler, ok := d.Commands.LookupCommand(data.Name)
	if !ok {
		return
	}
	d.execute(session, interaction, data.Name, handler)
}

// handleComponent routes a button or select click by its custom ID. A custom ID
// that no longer resolves (old message, retired handler) gets a private
// "not available" reply rather than silence, because the user clicked something.
func (d *Dispatcher) handleComponent(session *discordgo.Session, interaction *discordgo.InteractionCreate) {
	if d.Components == nil {
		_ = d.respond(interaction, ui.Error("That component is not available.")) // best-effort: nothing else to tell the user
		return
	}
	data, valid := interaction.Data.(discordgo.MessageComponentInteractionData)
	if !valid {
		return
	}
	handler, ok, err := d.Components.LookupComponent(data.CustomID)
	if err != nil || !ok {
		_ = d.respond(interaction, ui.Error("That component is not available.")) // best-effort: nothing else to tell the user
		return
	}
	d.execute(session, interaction, "component:"+data.CustomID, handler)
}

// handleModal routes a submitted form by its custom ID, with the same
// "not available" fallback as components.
func (d *Dispatcher) handleModal(session *discordgo.Session, interaction *discordgo.InteractionCreate) {
	if d.Components == nil {
		_ = d.respond(interaction, ui.Error("That modal is not available.")) // best-effort: nothing else to tell the user
		return
	}
	data, valid := interaction.Data.(discordgo.ModalSubmitInteractionData)
	if !valid {
		return
	}
	handler, ok, err := d.Components.LookupModal(data.CustomID)
	if err != nil || !ok {
		_ = d.respond(interaction, ui.Error("That modal is not available.")) // best-effort: nothing else to tell the user
		return
	}
	d.execute(session, interaction, "modal:"+data.CustomID, handler)
}

// execute runs one handler through the full lifecycle: claim the interaction ID
// so a redelivery cannot run it twice, build the traced context, call the
// handler with panic recovery, send its immediate response, and if it returned a
// Task start that on a goroutine. A rejected initial response is logged with
// timing so an expired three-second window is distinguishable from a bad payload.
func (d *Dispatcher) execute(
	session *discordgo.Session,
	interaction *discordgo.InteractionCreate,
	name string,
	handler ui.Handler,
) {
	started := time.Now()
	if !d.interactionDeduper().Claim(interaction.ID) {
		return
	}
	ctx := quack.ContextWithAuditSource(interactionTraceContext(interaction), model.AuditSourceDiscord)
	result := d.safeHandle(ctx, session, interaction, name, handler)
	if result.Response == nil {
		return
	}
	// Separate preparation from the Discord request when an acknowledgement fails.
	// Both consume the interaction deadline, but require different recovery work.
	responseStarted := time.Now()
	if err := d.respond(interaction, result.Response); err != nil {
		attrs := discordErrorAttrs(err)
		attrs = append(attrs,
			"interaction", name,
			"interaction_type", int(interaction.Type),
			"response_type", int(result.Response.Type),
			"elapsed_ms", time.Since(started).Milliseconds(),
			"prepare_ms", responseStarted.Sub(started).Milliseconds(),
			"response_ms", time.Since(responseStarted).Milliseconds(),
		)
		if created, timestampErr := discordgo.SnowflakeTimestamp(interaction.ID); timestampErr == nil {
			attrs = append(attrs, "interaction_age_ms", time.Since(created).Milliseconds())
		}
		slog.ErrorContext(ctx, "failed to respond to Discord interaction", attrs...)
		return
	}
	if result.Task == nil {
		return
	}

	go d.runTask(ctx, interaction, name, result.Task, result.Response)
}

// safeHandle calls the handler and converts a panic into a private generic
// error response so one interaction cannot take down the gateway goroutine.
func (d *Dispatcher) safeHandle(
	ctx context.Context,
	session *discordgo.Session,
	interaction *discordgo.InteractionCreate,
	name string,
	handler ui.Handler,
) (result ui.HandlerResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("Discord interaction handler panicked",
				"interaction", name,
				"request_id", quack.RequestIDFromContext(ctx),
				"correlation_id", quack.CorrelationIDFromContext(ctx),
				"panic_type", fmt.Sprintf("%T", recovered),
				"stack", debug.Stack(),
			)
			result = ui.Immediate(ui.Error("Quack could not handle that interaction."))
		}
	}()
	return handler(ui.Context{
		Context:     ctx,
		Services:    d.Services,
		Session:     session,
		Interaction: interaction,
	})
}

// runTask executes the deferred half of an Async handler. Errors and panics are
// reported to the user through taskError; the error type is logged but never its
// text, which may contain member content.
func (d *Dispatcher) runTask(
	ctx context.Context,
	interaction *discordgo.InteractionCreate,
	name string,
	task ui.Task,
	response *discordgo.InteractionResponse,
) {
	tracked := &taskResponder{Responder: d.responder(interaction)}
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("Discord interaction task panicked",
				"interaction", name,
				"request_id", quack.RequestIDFromContext(ctx),
				"correlation_id", quack.CorrelationIDFromContext(ctx),
				"panic_type", fmt.Sprintf("%T", recovered),
				"stack", debug.Stack(),
			)
			d.taskError(interaction, response, tracked.published.Load(), nil)
		}
	}()
	if err := task(ctx, tracked); err != nil {
		slog.Error("Discord interaction task failed",
			"error_type", fmt.Sprintf("%T", err),
			"interaction", name,
			"request_id", quack.RequestIDFromContext(ctx),
			"correlation_id", quack.CorrelationIDFromContext(ctx),
		)
		d.taskError(interaction, response, tracked.published.Load(), err)
	}
}

// interactionTraceContext derives request and correlation IDs from the
// interaction snowflake so logs and audit rows for one click share an ID.
func interactionTraceContext(interaction *discordgo.InteractionCreate) context.Context {
	requestID := quack.NewTraceID()
	correlationID := requestID
	if interaction != nil && interaction.ID != "" {
		requestID = "discord:" + interaction.ID
		correlationID = requestID
	}
	return quack.ContextWithTrace(context.Background(), requestID, correlationID)
}

// respond resolves icons and command mentions for the sending application and
// sends the initial response. Ephemeral flags are cleared for DMs (no GuildID)
// because Discord rejects them outside guilds.
func (d *Dispatcher) respond(interaction *discordgo.InteractionCreate, response *discordgo.InteractionResponse) error {
	response = ui.PrepareResponse(response, interaction.AppID)
	if interaction.GuildID == "" && response != nil && response.Data != nil {
		copyResponse, data := *response, *response.Data
		data.Flags &^= discordgo.MessageFlagsEphemeral
		copyResponse.Data = &data
		response = &copyResponse
	}
	return d.client().InteractionRespond(interaction.Interaction, response)
}

// responder binds the configured Client to one interaction for use by a Task.
func (d *Dispatcher) responder(interaction *discordgo.InteractionCreate) ui.Responder {
	return responder{client: d.client(), interaction: interaction.Interaction}
}

// responder implements ui.Responder for one interaction. Webhook-token methods
// stop working 15 minutes after the interaction; the channel methods do not.
type responder struct {
	client      Client
	interaction *discordgo.Interaction
}

// EditOriginal replaces the initial response through the interaction webhook.
func (r responder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	return r.client.InteractionResponseEdit(r.interaction, edit.ForApplication(r.interaction.AppID).WebhookEdit())
}

// Followup sends an additional reply. In DMs it is forced public because Discord
// rejects ephemeral followups there.
func (r responder) Followup(message ui.Message) (*discordgo.Message, error) {
	if r.interaction.GuildID == "" {
		message.Ephemeral = false
	}
	return r.client.FollowupMessageCreate(r.interaction, true, message.ForApplication(r.interaction.AppID).WebhookParams())
}

// PublishChannel sends a standalone notice without a reply reference to the
// hidden interaction acknowledgement. Ephemeral content is rejected rather than
// silently made public. Non-idempotent delivery is attempted only once.
func (r responder) PublishChannel(ctx context.Context, message ui.Message) (*discordgo.Message, error) {
	if message.Ephemeral {
		return nil, errors.New("cannot publish an ephemeral message to a channel")
	}
	if r.interaction.ChannelID == "" {
		return nil, errors.New("interaction channel unavailable")
	}
	return r.client.ChannelMessageSend(ctx, r.interaction.ChannelID, message.SendParams(r.interaction.AppID))
}

// EditChannel refreshes a standalone notice through the same channel coordinate
// using bot credentials; it does not depend on an interaction webhook lifetime.
func (r responder) EditChannel(ctx context.Context, messageID string, edit ui.Edit) (*discordgo.Message, error) {
	if r.interaction.ChannelID == "" || messageID == "" {
		return nil, errors.New("message coordinate unavailable")
	}
	prepared := edit.ForApplication(r.interaction.AppID).WebhookEdit()
	return r.client.ChannelMessageEdit(ctx, &discordgo.MessageEdit{
		ID:              messageID,
		Channel:         r.interaction.ChannelID,
		Content:         prepared.Content,
		Embeds:          prepared.Embeds,
		Components:      prepared.Components,
		Files:           prepared.Files,
		Attachments:     prepared.Attachments,
		AllowedMentions: prepared.AllowedMentions,
	})
}

// EditFollowup updates a previously published public result after asynchronous work reaches a terminal state.
func (r responder) EditFollowup(messageID string, edit ui.Edit) (*discordgo.Message, error) {
	return r.client.FollowupMessageEdit(r.interaction, messageID, edit.ForApplication(r.interaction.AppID).WebhookEdit())
}

// DeleteOriginal removes the initial response, typically an unused public placeholder.
func (r responder) DeleteOriginal() error {
	return r.client.InteractionResponseDelete(r.interaction)
}

// UpdateMessage edits the message a component belongs to. After DeferUpdate the
// original response is that message, so this is the same call as EditOriginal.
func (r responder) UpdateMessage(edit ui.Edit) (*discordgo.Message, error) {
	return r.EditOriginal(edit)
}

// sessionClient adapts a discordgo.Session to Client.
type sessionClient struct {
	session *discordgo.Session
}

// ChannelMessageSend publishes without automatic retries because an uncertain
// POST could already have created the notice. The private case receipt survives.
func (c sessionClient) ChannelMessageSend(
	ctx context.Context,
	channelID string,
	message *discordgo.MessageSend,
) (*discordgo.Message, error) {
	return c.session.ChannelMessageSendComplex(channelID, message,
		discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
}

// ChannelMessageEdit performs a context-bound bot edit of the persisted notice.
func (c sessionClient) ChannelMessageEdit(ctx context.Context, edit *discordgo.MessageEdit) (*discordgo.Message, error) {
	return c.session.ChannelMessageEditComplex(edit,
		discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
}

// InteractionRespond sends the initial response for an interaction.
func (c sessionClient) InteractionRespond(interaction *discordgo.Interaction, response *discordgo.InteractionResponse) error {
	return c.session.InteractionRespond(interaction, response)
}

// InteractionResponseEdit edits the initial response through the webhook token.
func (c sessionClient) InteractionResponseEdit(
	interaction *discordgo.Interaction,
	edit *discordgo.WebhookEdit,
) (*discordgo.Message, error) {
	return c.session.InteractionResponseEdit(interaction, edit)
}

// FollowupMessageCreate sends a followup through the webhook token.
func (c sessionClient) FollowupMessageCreate(
	interaction *discordgo.Interaction,
	wait bool,
	params *discordgo.WebhookParams,
) (*discordgo.Message, error) {
	return c.session.FollowupMessageCreate(interaction, wait, params)
}

// FollowupMessageEdit updates an interaction followup through the application webhook token.
func (c sessionClient) FollowupMessageEdit(
	interaction *discordgo.Interaction,
	messageID string,
	edit *discordgo.WebhookEdit,
) (*discordgo.Message, error) {
	return c.session.WebhookMessageEdit(interaction.AppID, interaction.Token, messageID, edit)
}

// InteractionResponseDelete deletes the initial response through the webhook token.
func (c sessionClient) InteractionResponseDelete(interaction *discordgo.Interaction) error {
	return c.session.InteractionResponseDelete(interaction)
}

// Key builds the registry key "namespace:action" used for component and modal handlers.
func Key(namespace, action string) string {
	return strings.TrimSpace(namespace) + ":" + strings.TrimSpace(action)
}

// taskError preserves a shared component message when an action fails, reporting
// the error only to the person who clicked it. Private defers remain private.
// A public defer that was never edited is deleted so the channel does not keep
// a stale placeholder; one that was already published is left as the result.
func (d *Dispatcher) taskError(
	interaction *discordgo.InteractionCreate,
	response *discordgo.InteractionResponse,
	published bool,
	err error,
) {
	message := "I couldn’t finish that. Try again in a moment."
	if errors.Is(err, quack.ErrCasePermissionDenied) || errors.Is(err, quack.ErrAuthorizationDenied) {
		message = "You do not have permission to use this control."
	}
	responder := d.responder(interaction)
	deferredUpdate := response.Type == discordgo.InteractionResponseDeferredMessageUpdate
	deferredPublic := response.Type == discordgo.InteractionResponseDeferredChannelMessageWithSource &&
		(response.Data == nil || response.Data.Flags&discordgo.MessageFlagsEphemeral == 0)
	if deferredUpdate || deferredPublic {
		if deferredPublic && !published {
			_ = responder.DeleteOriginal() // best-effort: the private error below is what matters
		}
		_, _ = responder.Followup(ui.Signal("error", message, true)) // best-effort: the failure is already logged
		return
	}
	_, _ = responder.EditOriginal(ui.ErrorEdit(message)) // best-effort: the failure is already logged
}

// discordErrorAttrs retains Discord's numeric rejection details without logging
// request URLs, interaction tokens, response bodies, or submitted case content.
func discordErrorAttrs(err error) []any {
	attrs := []any{"error_type", fmt.Sprintf("%T", err)}
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) {
		if restErr.Response != nil {
			attrs = append(attrs, "http_status", restErr.Response.StatusCode)
		}
		if restErr.Message != nil {
			attrs = append(attrs, "discord_code", restErr.Message.Code)
		}
	}
	return attrs
}

// taskResponder remembers successful response edits so a later unexpected failure
// cannot erase a committed public result while reporting the error privately.
type taskResponder struct {
	ui.Responder
	published atomic.Bool
}

// EditOriginal records a confirmed edit; the underlying transport remains unchanged.
func (r *taskResponder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	message, err := r.Responder.EditOriginal(edit)
	if err == nil {
		r.published.Store(true)
	}
	return message, err
}
