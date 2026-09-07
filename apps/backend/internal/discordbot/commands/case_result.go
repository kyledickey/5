package commands

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// updatePublicCaseResult follows enforcement for at most 30 seconds. It edits
// the original public result once a terminal outcome is known, without mutating
// the caller's case snapshot or polling an idle case indefinitely.
func updatePublicCaseResult(ctx context.Context, responder ui.Responder, services *quack.Services, created *quack.CaseResponse, messageID string, template *quack.TemplateResponse) {
	if services == nil || services.Store == nil || responder == nil || created == nil || created.ID == "" || messageID == "" || len(created.Actions) == 0 {
		return
	}
	snapshot := *created
	snapshot.Actions = slices.Clone(created.Actions)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			actions, err := services.Store.ListCaseActionExecutions(ctx, snapshot.ID)
			if err != nil {
				if ctx.Err() == nil {
					slog.ErrorContext(ctx, "Could not refresh public case result", "case_id", snapshot.ID, "error", err)
				}
				return
			}
			byID := make(map[string]model.ActionExecutionStatus, len(actions))
			for _, action := range actions {
				byID[action.ID] = action.Status
			}
			terminal := true
			for i := range snapshot.Actions {
				if status, ok := byID[snapshot.Actions[i].ID]; ok {
					snapshot.Actions[i].Status = status
				}
				switch snapshot.Actions[i].Status {
				case model.ActionExecutionPending, model.ActionExecutionRunning, model.ActionExecutionRetrying:
					terminal = false
				}
			}
			if terminal {
				_, err := responder.EditOriginal(ui.EditMessage(views.CaseCreatedMessage(views.CaseCreated{Case: &snapshot, Template: template})))
				if err != nil {
					slog.WarnContext(ctx, "Could not update public case result", "case_id", snapshot.ID, "error_type", "discord_response")
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
