package store

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// TestEvidenceChannelReceiptPreservesSettings verifies the narrow receipt write
// and expected-old comparison on both supported database engines.
func TestEvidenceChannelReceiptPreservesSettings(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			ctx := context.Background()
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			bootstrap, err := repository.BootstrapGuild(ctx, model.BootstrapGuildParams{DiscordGuildID: "guild", Name: "Guild", OwnerDiscordUserID: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			settings := bootstrap.Settings
			settings.NotificationFooter = "keep footer"
			settings.AppealRejoinURL = "https://discord.gg/keep"
			if _, err := repository.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{Settings: settings}); err != nil {
				t.Fatal(err)
			}
			winner, err := repository.CompareAndSetEvidenceChannel(ctx, bootstrap.Guild.ID, "", "first")
			if err != nil || winner != "first" {
				t.Fatal(winner, err)
			}
			winner, err = repository.CompareAndSetEvidenceChannel(ctx, bootstrap.Guild.ID, "", "loser")
			if err != nil || winner != "first" {
				t.Fatal(winner, err)
			}
			current, err := repository.GetGuildSettings(ctx, bootstrap.Guild.ID)
			if err != nil || current.NotificationFooter != "keep footer" || current.AppealRejoinURL != "https://discord.gg/keep" || current.ManagedEvidenceChannelDiscordID != "first" {
				t.Fatal(current, err)
			}
		})
	}
}
