package moduleintegration

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"testing"
)

// TestExternalBanLoggingSeparatesActorAndTarget prevents duplicate Quack entries
// without suppressing another moderator's action against the same member.
func TestExternalBanLoggingSeparatesActorAndTarget(t *testing.T) {
	for _, kind := range []discordgo.AuditLogAction{discordgo.AuditLogActionMemberBanAdd, discordgo.AuditLogActionMemberBanRemove} {
		entry := &discordgo.GuildAuditLogEntryCreate{GuildID: "guild", AuditLogEntry: &discordgo.AuditLogEntry{ID: "audit", TargetID: "member", UserID: "quack", ActionType: &kind, Reason: "rule"}}
		if _, ok := externalBanEvent(entry, "quack"); ok {
			t.Fatal("Quack action duplicated")
		}
		entry.UserID = "moderator"
		event, ok := externalBanEvent(entry, "quack")
		if !ok || event.ActorDiscordUserID != "moderator" || event.Metadata["target_id"] != "member" || event.Metadata["reason"] != "rule" {
			t.Fatalf("external action lost attribution: %+v", event)
		}
		if kind == discordgo.AuditLogActionMemberBanAdd && event.Type != generallogging.DiscordBan || kind == discordgo.AuditLogActionMemberBanRemove && event.Type != generallogging.DiscordUnban {
			t.Fatal("wrong event kind")
		}
	}
	if _, ok := externalBanEvent(nil, "quack"); ok {
		t.Fatal("nil audit event accepted")
	}
}
