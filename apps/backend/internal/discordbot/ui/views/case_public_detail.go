package views

import (
	"fmt"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// PublicCaseDetail renders the complete staff case in the invoking channel.
// Moderators choose an appropriate channel; member DMs use separate renderers.
func PublicCaseDetail(detail *quack.CaseDetailResponse) ui.Message {
	return CaseDetailPage(detail, 1, "")
}

// caseActionSentence adds the recorded expiry to completed timeouts so Discord
// localizes it for each reader. Other actions retain their accurate progress.
func caseActionSentence(action quack.CaseActionResponse) string {
	if action.ActionType == model.ActionTimeoutUser && action.Status == model.ActionExecutionSucceeded && action.TimeoutUntil != nil {
		return fmt.Sprintf("Timed out until <t:%d:f> (<t:%d:R>).", action.TimeoutUntil.Unix(), action.TimeoutUntil.Unix())
	}
	return ui.ActionSentence(action.ActionType, action.Status)
}
