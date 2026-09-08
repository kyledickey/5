package moduleintegration

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestDeletedTicketChannelGatewayLookup verifies the service lookup still forwards
// resolved threads to the adapter and ignores unrelated channels and guilds.
func TestDeletedTicketChannelGatewayLookup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "gateway.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Guild{}); err != nil {
		t.Fatal(err)
	}
	if err := modules.RegistryMigration().Apply(db); err != nil {
		t.Fatal(err)
	}
	if err := tickets.Migration().Apply(db); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: id}, DiscordGuildID: "discord-" + id, Name: id, IsActive: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), tickets.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	service := tickets.NewService(registry, tickets.NewStore(db), nil)
	ctx := context.Background()
	actor := tickets.Actor{GuildID: "one", DiscordUserID: "owner", CanManage: true}
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID, settings.QueueChannelDiscordID = "entry", "queue"
	if _, err := service.UpdateSettings(ctx, actor, true, settings); err != nil {
		t.Fatal(err)
	}
	ticket, err := service.Open(ctx, actor, "thread")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(ctx, actor, ticket.ID, "transcript"); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{db: db, Tickets: service, TicketDiscord: tickets.NewDiscordAdapter(service, nil)}
	for _, event := range []struct{ guild, channel string }{
		{"discord-two", "thread"}, {"discord-one", "unrelated"}, {"discord-one", "thread"},
	} {
		runtime.onChannelDelete(nil, &discordgo.ChannelDelete{Channel: &discordgo.Channel{GuildID: event.guild, ID: event.channel}})
	}
	current, events, err := service.Detail(ctx, actor, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	missing := 0
	for _, event := range events {
		if event.Type == tickets.EventChannelMissing {
			missing++
		}
	}
	if missing != 1 || current.Status != tickets.StatusResolved {
		t.Fatalf("deleted-thread repair changed: missing events=%d status=%s", missing, current.Status)
	}
}
