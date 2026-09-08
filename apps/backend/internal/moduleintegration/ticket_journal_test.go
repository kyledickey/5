package moduleintegration

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestTicketJournalCaptureWithoutLogging proves gateway text retention needs no
// logging service or integration guild lookup, including ordinary bot replies.
func TestTicketJournalCaptureWithoutLogging(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tickets.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := modules.RegistryMigration().Apply(db); err != nil {
		t.Fatal(err)
	}
	if err := tickets.Migration().Apply(db); err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), tickets.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	service := tickets.NewService(registry, tickets.NewStore(db), nil)
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID = "entry"
	settings.QueueChannelDiscordID = "queue"
	actor := tickets.Actor{GuildID: "guild", DiscordUserID: "owner", CanManage: true}
	if _, err := service.UpdateSettings(context.Background(), actor, true, settings); err != nil {
		t.Fatal(err)
	}
	ticket, err := service.Open(context.Background(), actor, "thread")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{Tickets: service}
	runtime.recordTicketMessage(&discordgo.MessageCreate{Message: &discordgo.Message{ID: "original", GuildID: "discord-guild", ChannelID: "thread", Content: "deleted bot reply", Author: &discordgo.User{ID: "bot", Bot: true}}})
	runtime.recordTicketMessage(&discordgo.MessageCreate{Message: &discordgo.Message{ID: "unrelated", GuildID: "discord-guild", ChannelID: "other", Content: "private unrelated", Author: &discordgo.User{ID: "member"}}})
	if _, err := service.ResolveNativeTranscript(context.Background(), actor, ticket.ID, nil); err != nil {
		t.Fatal(err)
	}
	transcript, err := service.Transcript(context.Background(), actor, ticket.ID)
	if err != nil || !strings.Contains(transcript.Content, "deleted bot reply") || strings.Contains(transcript.Content, "private unrelated") {
		t.Fatal(transcript, err)
	}
}
