package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// RegisterComponents installs the ticket lifecycle buttons into the process's
// single interaction dispatcher. The namespace/action/version strings below are
// baked into buttons on messages already posted in Discord: never change them.
func (r *Runtime) RegisterComponents(registry *interactions.ComponentRegistry) error {
	if r == nil || r.TicketDiscord == nil || r.Tickets == nil {
		return errors.New("ticket Discord runtime is not configured")
	}
	for _, entry := range []struct {
		action  string
		handler ui.Handler
	}{
		{"open", r.openTicketComponent},
		{"queue", r.ticketQueueComponent},
		{"view", r.viewTicketComponent},
		{"close", r.closeTicketComponent},
		{"repair", r.repairTicketComponent},
	} {
		if err := registry.RegisterComponent("ticket", entry.action, entry.handler); err != nil {
			return err
		}
	}
	return r.registerTicketQueueRecovery(registry)
}

func (r *Runtime) ticketActor(ctx ui.Context) (tickets.Actor, error) {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return tickets.Actor{}, errors.New("ticket interactions require a guild")
	}
	userID := interactionUserID(ctx.Interaction)
	if userID == "" {
		return tickets.Actor{}, errors.New("ticket interaction user is unavailable")
	}
	if r == nil || r.services == nil || r.services.Guilds == nil {
		return tickets.Actor{}, errors.New("live ticket authorization is unavailable")
	}
	guildContext, err := r.services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
		DiscordGuildID: ctx.Interaction.GuildID, DiscordUserID: userID,
	})
	if err != nil || guildContext == nil || !guildContext.Live.Actor.Present {
		return tickets.Actor{}, quack.ErrAuthorizationDenied
	}
	return tickets.Actor{
		GuildID: guildContext.Guild.ID, DiscordUserID: userID,
		CanManage:   guildContext.Can(model.PermissionActionGuildSettingsWrite),
		CanModerate: guildContext.Can(model.PermissionActionTicketResolve),
	}, nil
}

// openTicketComponent acknowledges quickly, then provisions the private ticket.
func (r *Runtime) openTicketComponent(ctx ui.Context) ui.HandlerResult {
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		ticket, err := r.TicketDiscord.Open(taskCtx, actor)
		if errors.Is(err, tickets.ErrDuplicateOpen) {
			active, lookupErr := r.Tickets.ActiveForMember(taskCtx, actor)
			if lookupErr == nil {
				_, err = responder.EditOriginal(ui.EditMessage(existingTicketMessage(active)))
				return err
			}
		}
		if err != nil && ticket == nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		message := ui.Signal("ticket", "Your ticket is ready: <#"+ticket.ThreadDiscordChannelID+">. Type there whenever you’re ready; a moderator will join you.", true)
		if err != nil {
			// The ticket is committed but Discord setup did not finish; point the
			// member at recovery rather than letting them open a second ticket.
			message = ui.Signal("ticket", "Your ticket is saved: <#"+ticket.ThreadDiscordChannelID+">, but setup did not finish. If access or the staff queue post is missing, ask a server administrator to use Repair ticket on this ticket. Opening again will return this same ticket.", true)
			message.Components = []discordgo.MessageComponent{ui.Row(queueRecoveryButton("view", ticket.ID, "Recovery", discordgo.SecondaryButton))}
		}
		_, err = responder.EditOriginal(ui.EditMessage(message))
		return err
	})
}

// ticketQueueComponent returns a bounded staff queue without transcript content.
func (r *Runtime) ticketQueueComponent(ctx ui.Context) ui.HandlerResult {
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		queue, err := r.Tickets.Queue(taskCtx, actor, tickets.StatusOpen, 25)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		lines := []string{fmt.Sprintf("You have **%d open tickets** to review.", len(queue))}
		for _, ticket := range queue {
			lines = append(lines, fmt.Sprintf("<#%s> · <@%s> · %s", ticket.ThreadDiscordChannelID, ticket.OwnerDiscordUserID, ui.RelativeTime(ticket.CreatedAt)))
		}
		if len(queue) == 0 {
			lines = []string{"No open tickets right now."}
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("ticket", strings.Join(lines, "\n\n"), true)))
		return err
	})
}

// viewTicketComponent keeps recovery and retained transcripts reachable from staff controls.
func (r *Runtime) viewTicketComponent(ctx ui.Context) ui.HandlerResult {
	payload, err := ticketComponentID(ctx)
	if err != nil {
		return ui.Immediate(ui.Error("That ticket is unavailable."))
	}
	ticketID, page := ticketDetailPayload(payload)
	// Refresh private entry receipts in place, replacing stale thread mentions
	// after closure. Public queue and thread controls always get a private reply.
	privateView := ctx.Interaction.Message != nil && ctx.Interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0
	acknowledgement := ui.DeferEphemeral()
	if privateView {
		acknowledgement = ui.DeferUpdate()
	}
	return r.ticketTaskWithResponse(ctx, acknowledgement, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		ticket, _, err := r.Tickets.Detail(taskCtx, actor, ticketID)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		pending, err := r.Tickets.ClosurePending(taskCtx, actor, ticketID)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		var transcript *tickets.Transcript
		if ticket.Status != tickets.StatusOpen {
			transcript, err = r.Tickets.Transcript(taskCtx, actor, ticketID)
			if err != nil && !errors.Is(err, tickets.ErrNotFound) {
				_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
				return nil
			}
		}
		edit := ui.EditMessage(ticketDetailMessage(ticket, nil, actor, pending, transcript, page))
		if privateView {
			_, err = responder.UpdateMessage(edit)
		} else {
			_, err = responder.EditOriginal(edit)
		}
		return err
	})
}

func (r *Runtime) repairTicketComponent(ctx ui.Context) ui.HandlerResult {
	ticketID, err := ticketComponentID(ctx)
	if err != nil {
		return ui.Immediate(ui.Error("That ticket is unavailable."))
	}
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		if err := r.TicketDiscord.RepairPermissions(taskCtx, actor, ticketID); err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		_, err := responder.EditOriginal(ui.EditMessage(ui.Signal("lock", "Ticket access and staff queue delivery are repaired. Access is limited to the member and staff.", true)))
		return err
	})
}

func ticketCloseComponents(ticketID string) []discordgo.MessageComponent {
	closeID := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "close", Version: "v1", Payload: ticketID})
	return []discordgo.MessageComponent{ui.Row(ui.Button(closeID, "Close", discordgo.DangerButton, false))}
}

func ticketControls(ticketID string, includeRepair bool) []discordgo.MessageComponent {
	components := ticketCloseComponents(ticketID)
	if !includeRepair || len(components) == 0 {
		return components
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		return components
	}
	repairID := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "repair", Version: "v1", Payload: ticketID})
	row.Components = append(row.Components, ui.Button(repairID, "Repair ticket", discordgo.SecondaryButton, false))
	components[0] = row
	return components
}

func (r *Runtime) closeTicketComponent(ctx ui.Context) ui.HandlerResult {
	ticketID, err := ticketComponentID(ctx)
	if err != nil {
		return ui.Immediate(ui.Error("That ticket is unavailable."))
	}
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		return closeTicketWithFeedback(taskCtx, responder, r.TicketDiscord, actor, ticketID, ctx.Interaction.ChannelID)
	})
}

// ticketComponentID extracts only the opaque identity; ticketTask checks live
// membership and the service verifies ownership before any content is read.
func ticketComponentID(ctx ui.Context) (string, error) {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return "", errors.New("ticket interactions require a guild")
	}
	customID, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil || customID.Payload == "" {
		return "", errors.New("ticket id is required")
	}
	return customID.Payload, nil
}

// ticketTask acknowledges before making fresh Discord authorization requests.
// Gateway cache and channel-level overrides never grant guild staff authority.
func (r *Runtime) ticketTask(ctx ui.Context, task func(context.Context, ui.Responder, tickets.Actor) error) ui.HandlerResult {
	return r.ticketTaskWithResponse(ctx, ui.DeferEphemeral(), task)
}

// ticketTaskWithResponse shares fresh authorization for private initial views and
// in-place history updates; callers must never update public messages with detail.
func (r *Runtime) ticketTaskWithResponse(ctx ui.Context, acknowledgement *discordgo.InteractionResponse, task func(context.Context, ui.Responder, tickets.Actor) error) ui.HandlerResult {
	return ui.Async(acknowledgement, func(taskCtx context.Context, responder ui.Responder) error {
		taskCtx = quack.ContextWithAuditSource(taskCtx, model.AuditSourceDiscord)
		current := ctx
		current.Context = taskCtx
		actor, err := r.ticketActor(current)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit("Quack could not verify your ticket access."))
			return nil
		}
		return task(taskCtx, responder, actor)
	})
}

func interactionUserID(interaction *discordgo.InteractionCreate) string {
	if interaction.Member != nil && interaction.Member.User != nil {
		return interaction.Member.User.ID
	}
	if interaction.User != nil {
		return interaction.User.ID
	}
	return ""
}

// modalText extracts one expected text input from Discord's component tree.
// discordgo decodes rows and inputs as either values or pointers, so both shapes
// have to be handled.
func modalText(components []discordgo.MessageComponent, customID string) string {
	for _, component := range components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			if value, valueOK := component.(discordgo.ActionsRow); valueOK {
				row = &value
			} else {
				continue
			}
		}
		for _, child := range row.Components {
			input, ok := child.(*discordgo.TextInput)
			if ok && input.CustomID == customID {
				return strings.TrimSpace(input.Value)
			}
			if value, valueOK := child.(discordgo.TextInput); valueOK && value.CustomID == customID {
				return strings.TrimSpace(value.Value)
			}
		}
	}
	return ""
}

func ticketErrorMessage(err error) string {
	switch {
	case errors.Is(err, tickets.ErrInvalidQueueReceipt):
		return "That message is not a Quack queue post for this ticket in its recorded staff queue. Check the message link and try again."
	case errors.Is(err, tickets.ErrQueueDeliveryUnknown):
		return "The staff queue post could not be confirmed. Another post was not sent because it could create a duplicate. Ask an administrator to open Recovery and check the queue post."
	case errors.Is(err, tickets.ErrJournalIncomplete):
		return "This ticket cannot close because some received messages could not be retained. Ask a server administrator to check transcript storage."
	case errors.Is(err, tickets.ErrDisabled):
		return "Tickets are not enabled for this server."
	case errors.Is(err, tickets.ErrPermissionDenied):
		return "You do not have permission to use that ticket."
	case errors.Is(err, tickets.ErrDuplicateOpen):
		return "You already have an open ticket."
	case errors.Is(err, tickets.ErrRateLimited):
		return "You have reached this server's ticket limit."
	case errors.Is(err, tickets.ErrNotFound):
		return "That ticket was not found."
	default:
		return "Something went wrong with this ticket. Try again in a moment."
	}
}

// registerTicketQueueRecovery keeps exceptional delivery repair on the existing
// private ticket detail rather than adding another ticket lifecycle. These custom
// IDs are also baked into already-posted messages: never change them.
func (r *Runtime) registerTicketQueueRecovery(registry *interactions.ComponentRegistry) error {
	for action, handler := range map[string]ui.Handler{
		"queuefix":     r.ticketQueueRecoveryComponent,
		"queueadopt":   r.ticketQueueAdoptComponent,
		"queueretry":   r.ticketQueueRetryComponent,
		"queueretryok": r.ticketQueueRetryConfirmed,
	} {
		if err := registry.RegisterComponent("ticket", action, handler); err != nil {
			return err
		}
	}
	return registry.RegisterModal("ticket", "queueadopt", r.ticketQueueAdoptSubmit)
}

// ticketQueueRecoveryComponent reads current manager authority and binds all
// subsequent controls to one durable uncertain delivery attempt.
func (r *Runtime) ticketQueueRecoveryComponent(ctx ui.Context) ui.HandlerResult {
	id, err := ticketComponentID(ctx)
	if err != nil {
		return ui.Immediate(ui.Error("That ticket is unavailable."))
	}
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		recovery, err := r.TicketDiscord.QueueRecovery(taskCtx, actor, id)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketQueueRecoveryError(err)))
			return nil
		}
		message := ui.Signal("ticket", "Check the staff queue in <#"+recovery.ChannelDiscordID+"> for this ticket's post. If it exists, use its message link. If no post was sent, you can allow one replacement after confirming that below.", true)
		payload := recovery.TicketID + "~" + recovery.AttemptID
		message.Components = []discordgo.MessageComponent{ui.Row(
			queueRecoveryButton("queueadopt", payload, "Use existing post", discordgo.SecondaryButton),
			queueRecoveryButton("queueretry", payload, "Post was not sent", discordgo.SecondaryButton),
		)}
		_, err = responder.EditOriginal(ui.EditMessage(message))
		return err
	})
}

func queueRecoveryButton(action, payload, label string, style discordgo.ButtonStyle) discordgo.Button {
	return ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: action, Version: "v1", Payload: payload}), label, style, false)
}

// queueRecoveryPayload rejects missing or ambiguous identifiers before handling
// a modal or confirmation; current authority and SQL state are checked on submit.
func queueRecoveryPayload(payload string) (string, string, bool) {
	id, attempt, ok := strings.Cut(payload, "~")
	return id, attempt, ok && id != "" && attempt != "" && !strings.Contains(attempt, "~")
}

// ticketQueueAdoptComponent opens a form with no private ticket content; the
// submission reauthorizes before fetching or adopting the referenced message.
func (r *Runtime) ticketQueueAdoptComponent(ctx ui.Context) ui.HandlerResult {
	payload, err := ticketComponentID(ctx)
	if _, _, ok := queueRecoveryPayload(payload); err != nil || !ok {
		return ui.Immediate(ui.Error("Open the ticket's recovery controls again."))
	}
	id := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "queueadopt", Version: "v1", Payload: payload})
	return ui.Immediate(ui.Modal("Use existing queue post", id, []discordgo.MessageComponent{
		ui.Row(discordgo.TextInput{CustomID: "message", Label: "Queue message link", Style: discordgo.TextInputShort, Required: true, MaxLength: 300}),
	}))
}

// ticketQueueAdoptSubmit accepts only a validated bot-owned post for this ticket.
func (r *Runtime) ticketQueueAdoptSubmit(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Type != discordgo.InteractionModalSubmit {
		return ui.Immediate(ui.Error("Open the ticket's recovery controls again."))
	}
	data := ctx.Interaction.ModalSubmitData()
	custom, err := ui.DecodeCustomID(data.CustomID)
	id, attempt, ok := queueRecoveryPayload(custom.Payload)
	if err != nil || !ok {
		return ui.Immediate(ui.Error("Open the ticket's recovery controls again."))
	}
	input := tickets.QueueRecoveryInput{TicketID: id, AttemptID: attempt, MessageURL: strings.TrimSpace(modalText(data.Components, "message"))}
	return r.reconcileTicketQueueTask(ctx, input)
}

// ticketQueueRetryComponent makes the administrator's nondelivery assertion
// explicit; an incomplete queue search is not automatic permission to resend.
func (r *Runtime) ticketQueueRetryComponent(ctx ui.Context) ui.HandlerResult {
	payload, err := ticketComponentID(ctx)
	if _, _, ok := queueRecoveryPayload(payload); err != nil || !ok {
		return ui.Immediate(ui.Error("Open the ticket's recovery controls again."))
	}
	message := ui.Signal("ticket", "Only continue after checking the staff queue and confirming that this ticket's post was not sent. If it did arrive, allowing a replacement may create a duplicate. Use its existing message link instead.", true)
	message.Components = []discordgo.MessageComponent{ui.Row(queueRecoveryButton("queueretryok", payload, "Confirm no post was sent", discordgo.DangerButton))}
	return ui.Immediate(ui.Ephemeral(message))
}

// ticketQueueRetryConfirmed resolves only the attempt the administrator inspected.
func (r *Runtime) ticketQueueRetryConfirmed(ctx ui.Context) ui.HandlerResult {
	payload, err := ticketComponentID(ctx)
	id, attempt, ok := queueRecoveryPayload(payload)
	if err != nil || !ok {
		return ui.Immediate(ui.Error("Open the ticket's recovery controls again."))
	}
	return r.reconcileTicketQueueTask(ctx, tickets.QueueRecoveryInput{TicketID: id, AttemptID: attempt, ConfirmNotDelivered: true})
}

// reconcileTicketQueueTask refreshes Discord authority for both recovery paths.
// It leaves publication and source cleanup to the ordinary repair/close controls.
func (r *Runtime) reconcileTicketQueueTask(ctx ui.Context, input tickets.QueueRecoveryInput) ui.HandlerResult {
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		ticket, err := r.TicketDiscord.ReconcileQueue(taskCtx, actor, input)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketQueueRecoveryError(err)))
			return nil
		}
		text := "The existing queue post is linked to this ticket."
		if input.ConfirmNotDelivered {
			text = "Your confirmation was saved. Quack can now send one replacement."
		}
		message := ui.Signal("ticket", text, true)
		if ticket.Status == tickets.StatusOpen {
			if input.ConfirmNotDelivered {
				message.Content += " Use Repair ticket to finish setup."
			}
			message.Components = ticketControls(ticket.ID, true)
		} else {
			message.Content += " Finish closing to save the transcript before deleting the thread."
			message.Components = []discordgo.MessageComponent{ui.Row(queueRecoveryButton("close", ticket.ID, "Finish closing", discordgo.SecondaryButton))}
		}
		_, err = responder.EditOriginal(ui.EditMessage(message))
		return err
	})
}

// ticketQueueRecoveryError directs stale controls back to current ticket state
// without implying that a newer delivery attempt was cleared or retried.
func ticketQueueRecoveryError(err error) string {
	if errors.Is(err, tickets.ErrInvalidTransition) || errors.Is(err, tickets.ErrQueueDeliveryUnknown) {
		return "This ticket's recovery state has changed. Open View ticket again to check its current status. No replacement was sent."
	}
	return ticketErrorMessage(err)
}
