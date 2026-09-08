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
	return registry.RegisterComponent("appeal", "reverse", appealReversalHandler(services, appeals))
}

func appealReversalHandler(services *quack.Services, appeals *quack.AppealService) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil {
			return ui.Immediate(ui.Error("This reversal control is unavailable."))
		}
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("This reversal control is invalid."))
		}
		parts := strings.Split(parsed.Payload, ",")
		if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return ui.Immediate(ui.Error("This reversal control is invalid."))
		}
		actionType := model.ActionType(parts[2])
		if actionType != model.ActionRemoveTimeout && actionType != model.ActionUnbanUser {
			return ui.Immediate(ui.Error("This reversal type is invalid."))
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
				_, _ = responder.EditOriginal(ui.ErrorEdit("Live Discord authorization failed."))
				return nil
			}
			appeal, err := appeals.GetStaff(taskCtx, guildContext, appealID)
			if err != nil || appeal.Status != model.AppealStatusAccepted {
				_, _ = responder.EditOriginal(ui.ErrorEdit("This appeal is not eligible for reversal."))
				return nil
			}
			linkedAppealID := appeal.ID
			if _, err := services.Actions.ReverseForAppeal(taskCtx, guildContext, appeal.CaseID, executionID, actionType, &linkedAppealID); err != nil {
				_, _ = responder.EditOriginal(ui.ErrorEdit("The reversal could not be authorized or queued."))
				return nil
			}
			message := ui.Signal("retry", "The reversal is queued. You can follow its progress in the case history.", false)
			_, err = ui.Publish(responder, message)
			return err
		})
	}
}
