package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"gorm.io/gorm"
)

// TestCurrentSchemaAddsHoneypotPresentation verifies marker-one startup adds the
// delivery table and recovery preserves both pending work and uncertain sends.
func TestCurrentSchemaAddsHoneypotPresentation(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			if err := db.Migrator().DropTable(&honeypot.WarningRefresh{}); err != nil {
				t.Fatal(err)
			}
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			row := honeypot.WarningRefresh{GuildID: "guild", Revision: "request", Pending: true, NextAttemptAt: time.Now().UTC(), SendIdentity: "uncertain-send"}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			manifest, err := repository.BuildRecoveryManifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Tables["honeypot_warning_refreshes"].Count != 1 {
				t.Fatal("presentation missing from manifest")
			}
			if err := repository.VerifyRecoveryManifest(context.Background(), *manifest); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&row).Update("send_identity", "").Error; err != nil {
				t.Fatal(err)
			}
			if err := repository.VerifyRecoveryManifest(context.Background(), *manifest); err == nil {
				t.Fatal("unknown-send fence mutation undetected")
			}
			service := honeypot.NewService(nil, honeypot.NewStore(db), nil, nil, nil, nil)
			ctx := context.Background()
			settings := honeypot.Settings{ChannelDiscordID: "trap", WarningMessageID: "old"}
			if err := service.ReserveWarningSend(ctx, "guild", settings); err != nil {
				t.Fatal(err)
			}
			if err := service.ReserveWarningSend(ctx, "guild", settings); !errors.Is(err, honeypot.ErrWarningDeliveryUnknown) {
				t.Fatal(err)
			}
			if err := service.ReleaseWarningSend(ctx, "guild", settings); err != nil {
				t.Fatal(err)
			}
			if err := service.RequestWarningRefresh(ctx, "guild"); err != nil {
				t.Fatal(err)
			}
			if err := service.CompleteWarningRefresh(ctx, row, false); err != nil {
				t.Fatal(err)
			}
			rows, err := service.WarningRefreshes(ctx, time.Now().Add(time.Hour))
			if err != nil || len(rows) != 1 || rows[0].Revision == row.Revision {
				t.Fatal(rows, err)
			}

		})
	}
}
