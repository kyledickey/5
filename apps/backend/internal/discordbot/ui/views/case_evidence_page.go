package views

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
)

// CaseEvidenceSnapshotPages splits only the selected evidence after resolving
// emoji, preserving native text navigation and all attachment warnings/links.
func CaseEvidenceSnapshotPages(detail *quack.CaseEvidencePageResponse, applicationID string) []string {
	body := evidenceSummary(detail.Evidence)
	if body == "" {
		body = "No evidence has been added yet."
	}
	return ui.TextPages(discordtext.Resolve(body, applicationID), 1600)
}

// CaseEvidenceSnapshotPage combines bounded snapshot navigation with text subpages.
// Previous/Next cross snapshot boundaries without introducing another workflow.
func CaseEvidenceSnapshotPage(detail *quack.CaseEvidencePageResponse, page int, applicationID string) ui.Message {
	if detail == nil {
		return ui.Signal("error", "That case could not be found.", true)
	}
	pages := CaseEvidenceSnapshotPages(detail, applicationID)
	if page < 1 {
		page = 1
	}
	if page > len(pages) {
		page = len(pages)
	}
	body := pages[page-1] + fmt.Sprintf("\n\nAdd a screenshot with `/case evidence case:%d file:` or use its `message_link` option.", detail.CaseNumber)
	footer := ""
	if detail.Total > 0 {
		footer = fmt.Sprintf("Evidence %d of %d", detail.Position, detail.Total)
	}
	if len(pages) > 1 {
		footer += fmt.Sprintf(" · page %d of %d", page, len(pages))
	}
	message := ui.Conversation("evidence", fmt.Sprintf("Evidence for case #%d", detail.CaseNumber), "", body, footer, true)
	previous := detail.Position > 1 || page > 1
	next := int64(detail.Position) < detail.Total || page < len(pages)
	if previous || next {
		payload := fmt.Sprintf("%d:%d|%s", detail.Position, page, detail.ID)
		message.Components = []discordgo.MessageComponent{ui.Row(
			ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "evidence_prev", Version: "v1", Payload: payload}), "Previous", discordgo.SecondaryButton, !previous),
			ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "evidence_next", Version: "v1", Payload: payload}), "Next", discordgo.SecondaryButton, !next),
		)}
	}
	return message
}
