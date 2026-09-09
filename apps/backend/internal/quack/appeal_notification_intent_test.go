package quack

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestAppealIntentValidationPreservesLegacyOnlyWhenAbsent keeps historical bodies
// exact while refusing unknown/corrupt payloads instead of hiding them via fallback.
func TestAppealIntentValidationPreservesLegacyOnlyWhenAbsent(t *testing.T) {
	legacy := "old **saved** body @everyone"
	notice, err := appealMemberNotification(model.AppealNotification{Body: legacy})
	if err != nil || notice.LegacyBody != legacy || notice.Intent != nil {
		t.Fatal(notice, err)
	}
	for _, payload := range []string{" ", "null", "{}", `{"version":2,"status":"accepted","reason":"reason"}`, `{"version":1,"status":"pending","reason":"reason"}`, `{"version":1,"status":"accepted","reason":""}`, `{"version":1,"status":"rejected","reason":"reason","rejoin_url":"https://discord.gg/pond"}`} {
		if _, err := appealMemberNotification(model.AppealNotification{Body: legacy, DecisionIntentJSON: payload}); !errors.Is(err, ErrAppealNotificationIntent) {
			t.Fatal("invalid payload fell back", payload, err)
		}
	}
	payload, _ := json.Marshal(model.AppealDecisionIntent{Version: 1, Status: model.AppealStatusAccepted, Reason: "saved reason", CaseNumber: 42, CaseID: "case", GuildName: "Pond", RejoinURL: "https://discord.gg/pond"})
	notice, err = appealMemberNotification(model.AppealNotification{Body: legacy, DecisionIntentJSON: string(payload)})
	if err != nil || notice.LegacyBody != "" || notice.Intent == nil || notice.Intent.Reason != "saved reason" || notice.Intent.CaseNumber != 42 || notice.Intent.CaseID != "case" || notice.Intent.GuildName != "Pond" {
		t.Fatal(notice, err)
	}
}
