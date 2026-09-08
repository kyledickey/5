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

// TestHoneypotExemptionUsesGuildModerationAuthority keeps the baseline identical
// to cases and appeals, regardless of trap-channel permission overwrites.
func TestHoneypotExemptionUsesGuildModerationAuthority(t *testing.T) {
	for _, test := range []struct {
		name                     string
		permissions, allow, deny int64
		owner, want              bool
	}{
		{name: "moderator", permissions: discordgo.PermissionModerateMembers, want: true},
		{name: "administrator", permissions: discordgo.PermissionAdministrator, want: true},
		{name: "owner", owner: true, want: true},
		{name: "manage server only", permissions: discordgo.PermissionManageServer},
		{name: "ban only", permissions: discordgo.PermissionBanMembers},
		{name: "channel grant", allow: discordgo.PermissionModerateMembers},
		{name: "channel denial", permissions: discordgo.PermissionModerateMembers, deny: discordgo.PermissionModerateMembers, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{{ID: "guild"}, {ID: "role", Permissions: test.permissions}}}
			if test.owner {
				guild.OwnerID = "member"
			}
			channel := &discordgo.Channel{ID: "trap", GuildID: "guild", PermissionOverwrites: []*discordgo.PermissionOverwrite{{ID: "member", Type: discordgo.PermissionOverwriteTypeMember, Allow: test.allow, Deny: test.deny}}}
			member := &discordgo.Member{User: &discordgo.User{ID: "member"}, Roles: []string{"role"}}
			event := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "trap", Author: member.User}}
			projection, err := projectHoneypotMessage("internal", event, guild, channel, member, "bot")
			if err != nil || projection.AuthorCanModerate != test.want {
				t.Fatalf("exemption=%v want=%v err=%v", projection.AuthorCanModerate, test.want, err)
			}
		})
	}
}
