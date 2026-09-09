package views

import (
	"fmt"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// PublicCaseDetail shows the rule and outcome in a normal command response.
// Staff context, evidence, and delivery diagnostics remain behind private controls.
func PublicCaseDetail(detail *quack.CaseDetailResponse) ui.Message {
	if detail == nil {
		return ui.Signal("error", "I can’t find that case. Check its number.", true)
	}
	item := detail.CaseResponse
	item.Actions = nil
	for _, action := range detail.Actions {
		item.Actions = append(item.Actions, action.CaseActionResponse)
	}
	var rule *quack.TemplateResponse
	reason := ""
	if detail.TemplateSnapshot != nil {
		rule = &quack.TemplateResponse{Name: detail.TemplateSnapshot.Template.Name}
		reason = detail.TemplateSnapshot.Template.ReasonTemplate
	}
	return CaseCreatedMessage(CaseCreated{Case: &item, Template: rule, MemberReason: reason})
}

// caseActionSentence adds the recorded expiry to completed timeouts so Discord
// localizes it for each reader. Other actions retain their accurate progress.
func caseActionSentence(action quack.CaseActionResponse) string {
	if action.ActionType == model.ActionTimeoutUser && action.Status == model.ActionExecutionSucceeded && action.TimeoutUntil != nil {
		return fmt.Sprintf("Timed out until <t:%d:f> (<t:%d:R>).", action.TimeoutUntil.Unix(), action.TimeoutUntil.Unix())
	}
	return ui.ActionSentence(action.ActionType, action.Status)
}
