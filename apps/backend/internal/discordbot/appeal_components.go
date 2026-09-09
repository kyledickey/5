package discordbot

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// RegisterAppealComponents installs member form entry/submission and staff recovery controls.
func RegisterAppealComponents(registry *interactions.ComponentRegistry, services *quack.Services, appeals *quack.AppealService) error {
	if registry == nil || services == nil || services.Guilds == nil || services.Actions == nil || appeals == nil {
		return errors.New("appeal component dependencies are not configured")
	}
	if err := registry.RegisterComponent("appeal", "submit", appealSubmissionHandler(appeals)); err != nil {
		return err
	}
	if err := registry.RegisterModal("appeal", "submit", appealSubmissionModal(appeals)); err != nil {
		return err
	}
	for _, action := range []string{"accept", "reject"} {
		if err := registry.RegisterComponent("appeal", action, appealDecisionHandler(services, appeals, action)); err != nil {
			return err
		}
	}
	for action, delta := range map[string]int{"statement_prev": -1, "statement_next": 1} {
		if err := registry.RegisterComponent("appeal", action, appealStatementPage(services, appeals, delta)); err != nil {
			return err
		}
	}
	return registry.RegisterComponent("appeal", "reverse", appealReversalHandler(services, appeals))
}

// appealReversalHandler checks current review authority before queuing a ban or
// timeout removal linked to the accepted appeal.
func appealReversalHandler(services *quack.Services, appeals *quack.AppealService) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil {
			return ui.Immediate(ui.Error("Open this appeal in the server’s review queue to remove the punishment."))
		}
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That punishment button is broken. Open the case to try again."))
		}
		parts := strings.Split(parsed.Payload, ",")
		if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return ui.Immediate(ui.Error("That punishment button is broken. Open the case to try again."))
		}
		actionType := model.ActionType(parts[2])
		if actionType != model.ActionRemoveTimeout && actionType != model.ActionUnbanUser {
			return ui.Immediate(ui.Error("Only bans and timeouts can be removed here."))
		}
		appealID, executionID := parts[0], parts[1]
		guildID := ctx.Interaction.GuildID
		actor := ctx.Interaction.Member.User
		displayName := ctx.Interaction.Member.Nick
		if displayName == "" {
			displayName = actor.GlobalName
		}
		return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
			guildContext, err := services.Guilds.ResolveDiscordStaffContext(taskCtx, quack.DiscordStaffContextInput{DiscordGuildID: guildID, DiscordUserID: actor.ID, DisplayName: displayName, LastActiveAt: time.Now().UTC()})
			if err != nil {
				_, _ = responder.EditOriginal(ui.ErrorEdit("I couldn’t check your Discord permissions. Try again in a moment."))
				return nil
			}
			appeal, err := appeals.GetStaff(taskCtx, guildContext, appealID)
			if err != nil || appeal.Status != model.AppealStatusAccepted {
				_, _ = responder.EditOriginal(ui.ErrorEdit("Accept the appeal before removing its punishment."))
				return nil
			}
			linkedAppealID := appeal.ID
			if _, err := services.Actions.ReverseForAppeal(taskCtx, guildContext, appeal.CaseID, executionID, actionType, &linkedAppealID); err != nil {
				_, _ = responder.EditOriginal(ui.ErrorEdit("I couldn’t queue the punishment removal. Check your moderation permissions and try again."))
				return nil
			}
			message := ui.Signal("retry", "Punishment removal queued. Check the case for the result.", false)
			_, err = ui.Publish(responder, message)
			return err
		})
	}
}
