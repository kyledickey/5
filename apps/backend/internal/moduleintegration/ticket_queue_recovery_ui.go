package moduleintegration

import (
	"context"
	"errors"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// registerTicketQueueRecovery keeps exceptional delivery repair on the existing
// private ticket detail rather than adding another ticket lifecycle.
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

// queueRecoveryButton preserves the expected attempt in every recovery control.
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
