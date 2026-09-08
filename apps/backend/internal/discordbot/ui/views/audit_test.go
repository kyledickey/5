package views

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

func TestAuditFailureShowsCaseAndPermissionCheckedRetry(t *testing.T) {
	notice := AuditMirrorMessage(quack.AuditMirrorMessage{ActorDiscordUserID: "quack-system", Action: "case_action.failed", Result: model.AuditResultFailure, CaseID: "case", CaseNumber: 42, TargetDiscordUserID: "123", TemplateName: "Spam", ActionType: model.ActionBanUser, RetryExecutionID: "execution", CorrelationID: "internal-correlation", MetadataJSON: `{"private":"not-for-display"}`})
	for _, want := range []string{"Quack", "Case #42", "<@123>", "Spam", "Ban"} {
		if !strings.Contains(notice.Content, want) {
			t.Fatalf("missing %s: %s", want, notice.Content)
		}
	}
	for _, hidden := range []string{"<@quack-system>", "internal-correlation", "not-for-display"} {
		if strings.Contains(notice.Content, hidden) {
			t.Fatalf("leaked %s", hidden)
		}
	}
	if len(notice.Components) != 1 {
		t.Fatal("missing retry button")
	}
	button := notice.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	id, err := ui.DecodeCustomID(button.CustomID)
	if err != nil || id.Namespace != "case" || id.Action != "retry" || id.Payload != "execution" {
		t.Fatalf("wrong recovery route: %+v %v", id, err)
	}
}
