package moduleintegration

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestHoneypotFiltersBeforeDiscordLookup verifies unrelated traffic stays cheap
// while a channel configuration change is honored by the very next message.
func TestHoneypotFiltersBeforeDiscordLookup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Guild{}, &modules.Configuration{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), honeypot.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	configure := func(raw string) {
		t.Helper()
		if _, err := registry.SetConfiguration(context.Background(), modules.Configuration{GuildID: "internal", ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: raw}); err != nil {
			t.Fatal(err)
		}
	}
	configure(`{"channel_discord_id":"trap","template_id":"template"}`)
	session, _ := discordgo.New("Bot test")
	reads := 0
	session.Client = &http.Client{Transport: ticketRoundTripper(func(*http.Request) (*http.Response, error) {
		reads++
		return nil, errors.New("stop after first lookup")
	})}
	runtime := &Runtime{db: db, registry: registry, session: session, HoneypotRuntime: &honeypot.Runtime{}}
	event := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "ordinary", Author: &discordgo.User{ID: "member"}}}
	runtime.submitHoneypotMessage(event)
	event.ChannelID = "trap"
	event.Author.Bot = true
	runtime.submitHoneypotMessage(event)
	if reads != 0 {
		t.Fatalf("unrelated traffic made %d REST calls", reads)
	}
	event.Author.Bot = false
	event.ChannelID = "ordinary"
	configure(`{"channel_discord_id":"ordinary","template_id":"template"}`)
	runtime.submitHoneypotMessage(event)
	if reads != 1 {
		t.Fatalf("new trap configuration not observed: %d reads", reads)
	}
}
