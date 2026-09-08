package views

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

func TestAppealEntryOpensDiscordFormWithoutWebsite(t *testing.T) {
	for _, baseURL := range []string{"", "http://unused.example", "https://unused.example"} {
		message, err := AppealEntryMessage(baseURL, "guild", "case")
		if err != nil {
			t.Fatal(err)
		}
		row := message.Components[0].(discordgo.ActionsRow)
		button := row.Components[0].(discordgo.Button)
		if button.URL != "" || button.CustomID != "appeal:submit:v1:case" {
			t.Fatalf("not a Discord appeal button: %+v", button)
		}
	}
}

func TestAppealStaffMessageOffersOnlyExplicitReversalControls(t *testing.T) {
	message := AppealStaffMessage(&quack.AppealResponse{ID: "appeal", CaseID: "case", TargetDiscordUserID: "target", Status: model.AppealStatusAccepted, ReversalOffers: []quack.AppealReversalOffer{{OriginalExecutionID: "execution", ActionType: model.ActionUnbanUser}}})
	if len(message.Components) != 1 || len(message.Embeds) != 0 || !strings.Contains(message.Content, "<@target>") {
		t.Fatalf("expected one explicit reversal offer: %+v", message)
	}
	row := message.Components[0].(discordgo.ActionsRow)
	button := row.Components[0].(discordgo.Button)
	if button.Style != discordgo.SecondaryButton || !strings.HasPrefix(button.Label, "Confirm ") || !strings.Contains(button.CustomID, "appeal:reverse:v1") {
		t.Fatalf("reversal was not an explicit confirmation control: %+v", button)
	}
}

func TestAppealQueueContainsStatementAndDecisionControls(t *testing.T) {
	appeal := &quack.AppealResponse{ID: "appeal", CaseID: "case", CaseNumber: 12, TemplateName: "Spam", TargetDiscordUserID: "target", Status: model.AppealStatusPending, Answers: []model.AppealAnswer{{QuestionID: "reason", Value: "I am sorry for repeating messages."}}}
	message := AppealStaffMessage(appeal)
	if !strings.Contains(message.Content, "I am sorry") || !strings.Contains(message.Content, "Case #12") || !strings.Contains(message.Content, "Spam") {
		t.Fatalf("missing review context: %s", message.Content)
	}
	row := message.Components[0].(discordgo.ActionsRow)
	if len(row.Components) != 2 || row.Components[0].(discordgo.Button).CustomID != "appeal:accept:v1:appeal" || row.Components[1].(discordgo.Button).CustomID != "appeal:reject:v1:appeal" {
		t.Fatalf("decision controls: %+v", row)
	}
	appeal.Status = model.AppealStatusAccepted
	if decided := AppealStaffMessage(appeal); len(decided.Components) != 0 {
		t.Fatal("decided appeal still offers decision buttons")
	}
}
