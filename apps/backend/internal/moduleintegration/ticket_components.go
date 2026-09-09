package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// ticketActor resolves the current Discord member into module authority.
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
		setupIncomplete := err != nil
		message := ui.Signal("ticket", "Your ticket is ready: <#"+ticket.ThreadDiscordChannelID+">. Type there whenever you’re ready; a moderator will join you.", true)
		if setupIncomplete {
			message = ui.Signal("ticket", "Your ticket is saved: <#"+ticket.ThreadDiscordChannelID+">, but setup did not finish. If access or the staff queue post is missing, ask a server administrator to use Repair ticket on this ticket. Opening again will return this same ticket.", true)
		}
		message.Components = ticketControls(ticket.ID, actor.CanManage)
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

// viewTicketComponent returns authorized lifecycle state and a bounded history page.
func (r *Runtime) viewTicketComponent(ctx ui.Context) ui.HandlerResult {
	payload, err := ticketComponentID(ctx)
	if err != nil {
		return ui.Immediate(ui.Error("That ticket is unavailable."))
	}
	ticketID, page := ticketDetailPayload(payload)
	// Refresh private entry receipts in place, replacing stale thread mentions
	// after closure. Public queue and thread controls always get a private reply.
	privateView := ctx.Interaction.Message != nil && ctx.Interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0
	pagination := strings.Contains(payload, "~") && privateView
	acknowledgement := ui.DeferEphemeral()
	if privateView {
		acknowledgement = ui.DeferUpdate()
	}
	return r.ticketTaskWithResponse(ctx, acknowledgement, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		ticket, events, err := r.Tickets.Detail(taskCtx, actor, ticketID)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		pending, err := r.Tickets.ClosurePending(taskCtx, actor, ticketID)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		if ticket.Status == tickets.StatusOpen && actor.CanModerate && !pagination {
			if err := r.TicketDiscord.Join(taskCtx, actor, ticket.ID); err != nil {
				_, _ = responder.EditOriginal(ui.ErrorEdit("Quack could not add you to the ticket thread. Check the bot's thread permissions and try again."))
				return nil
			}
		}
		var transcript *tickets.Transcript
		if ticket.Status != tickets.StatusOpen {
			transcript, err = r.Tickets.Transcript(taskCtx, actor, ticketID)
			if err != nil && !errors.Is(err, tickets.ErrNotFound) {
				_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
				return nil
			}
		}
		edit := ui.EditMessage(ticketDetailMessage(ticket, events, actor, pending, transcript, page))
		if privateView {
			_, err = responder.UpdateMessage(edit)
		} else {
			_, err = responder.EditOriginal(edit)
		}
		return err
	})
}

// repairTicketComponent restores the configured private ACL for current managers.
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

// ticketControls adds manager-only repair behavior to the module-owned controls.
func ticketControls(ticketID string, includeRepair bool) []discordgo.MessageComponent {
	components := tickets.TicketComponents(ticketID)
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

// closeTicketComponent captures the transcript and resolves the ticket.
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

// interactionUserID normalizes guild and direct interaction identity fields.
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

// ticketErrorMessage maps internal classifications to safe Discord copy.
func ticketErrorMessage(err error) string {
	switch {
	case errors.Is(err, tickets.ErrInvalidQueueReceipt):
		return "That message is not a Quack queue post for this ticket in its recorded staff queue. Check the message link and try again."
	case errors.Is(err, tickets.ErrQueueDeliveryUnknown):
		return "The staff queue post could not be confirmed. Another post was not sent because it could create a duplicate. Ask an administrator to open View ticket and use Recover queue post."
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
		return "Quack could not complete that ticket operation."
	}
}
