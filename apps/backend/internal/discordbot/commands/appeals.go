package commands

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack/model"
	"strconv"
)

// AppealsCommandSpec exposes pending submissions even when queue delivery failed.
func AppealsCommandSpec() CommandSpec {
	permissions := int64(discordgo.PermissionModerateMembers)
	dm := false
	return CommandSpec{Definition: &discordgo.ApplicationCommand{Name: "appeals", Description: "Review pending appeals", DefaultMemberPermissions: &permissions, DMPermission: &dm}, Handler: func(ctx ui.Context) ui.HandlerResult { return appealQueuePage(ctx, 1, false) }}
}

// RegisterAppealQueueComponents installs stateless, permission-checked pagination.
func RegisterAppealQueueComponents(registry *interactions.ComponentRegistry) error {
	return registry.RegisterComponent("appeal", "page", func(ctx ui.Context) ui.HandlerResult {
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		page, parseErr := strconv.Atoi(parsed.Payload)
		if err != nil || parseErr != nil || page < 1 || page > 1000000 {
			return ui.Immediate(ui.Error("I couldn’t open that page. Run /appeals to start again."))
		}
		return appealQueuePage(ctx, page, true)
	})
}

// appealQueuePage reads one pending statement per page directly from storage.
// Every navigation refreshes authorization, independent of notification receipts.
func appealQueuePage(ctx ui.Context, page int, update bool) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Services == nil || ctx.Services.Appeals == nil {
		return ui.Immediate(ui.Error("Use /appeals in your server."))
	}
	ack := ui.DeferPublic()
	if update {
		ack = ui.DeferUpdate()
	}
	return ui.Async(ack, func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("I couldn’t check your Discord permissions. Try again in a moment."))
			return err
		}
		list, err := ctx.Services.Appeals.ListStaff(taskCtx, guild, model.AppealStatusPending, 1, page-1)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("You need Moderate Members permission to review this server's appeals."))
			return err
		}
		if len(list.Appeals) == 0 && page > 1 {
			page = 1
			list, err = ctx.Services.Appeals.ListStaff(taskCtx, guild, model.AppealStatusPending, 1, 0)
			if err != nil {
				return err
			}
		}
		message := ui.Signal("appeal", "No appeals are waiting for review.", false)
		if len(list.Appeals) > 0 {
			message = views.AppealStaffPage(&list.Appeals[0], 1, ui.SessionApplicationID(ctx.Session))
			message.Ephemeral = false
			message.Content += fmt.Sprintf("\nPending appeal %d of %d", page, list.Total)
			message.Components = append(message.Components, ui.Row(
				ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "page", Version: "v1", Payload: strconv.Itoa(max(1, page-1))}), "Previous", discordgo.SecondaryButton, page <= 1),
				ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "page", Version: "v1", Payload: strconv.Itoa(page + 1)}), "Next", discordgo.SecondaryButton, int64(page) >= list.Total),
			))
		}
		_, err = ui.Publish(responder, message)
		return err
	})
}
