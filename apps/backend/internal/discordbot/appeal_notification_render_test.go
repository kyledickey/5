package discordbot

import (
	"testing"

	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestAppealDecisionCopyPreservesSnapshots keeps version-one wording and literal
// reviewer-supplied reason text equivalent to the former stored rendered bodies.
func TestAppealDecisionCopyPreservesSnapshots(t *testing.T) {
	for _, item := range []struct {
		status           model.AppealStatus
		icon, lead, next string
	}{
		{model.AppealStatusAccepted, "accept", "Your appeal was accepted.", "Your case was voided. Quack will try to remove any ban or timeout from it."},
		{model.AppealStatusRejected, "decline", "Your appeal was declined.", ""},
		{model.AppealStatusNeedsInformation, "reply", "Staff need a little more information to review your appeal.", "You can reply from your Quack dashboard."},
	} {
		intent := &model.AppealDecisionIntent{Version: 1, Status: item.status, Reason: "**Reason** @everyone"}
		want := discordtext.Conversation(item.icon, item.lead, discordtext.Plain(intent.Reason), item.next, "")
		if item.status == model.AppealStatusAccepted {
			intent.RejoinURL = "https://discord.gg/original"
			want += "\n\nIf you left or were banned, you can rejoin once any ban has been removed: " + intent.RejoinURL
		}
		if got := appealMemberNotificationBody(quack.AppealMemberNotification{Intent: intent}); got != want {
			t.Fatal(got, want)
		}
	}
	legacy := "legacy preserved copy"
	if got := appealMemberNotificationBody(quack.AppealMemberNotification{LegacyBody: legacy}); got != legacy {
		t.Fatal("legacy body changed", got)
	}
}
