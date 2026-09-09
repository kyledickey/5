package tickets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestMySQLQueueSendAdmission verifies conditional send fencing against MySQL,
// including legacy NULL destinations and a reconstructed store after uncertainty.
func TestMySQLQueueSendAdmission(t *testing.T) {
	dsn := os.Getenv("QUACK_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("QUACK_TEST_MYSQL_DSN is not configured")
	}
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ParseTime = true
	cfg.DBName = ""
	admin, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer adminSQL.Close()
	databaseName := fmt.Sprintf("quack_ticket_queue_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE DATABASE `" + databaseName + "`").Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := admin.Exec("DROP DATABASE `" + databaseName + "`").Error; err != nil {
			t.Error(err)
		}
	}()
	cfg.DBName = databaseName
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := Migration().Apply(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ticketRecord{ID: "ticket", GuildID: "guild", OwnerDiscordUserID: "owner", ThreadDiscordChannelID: "thread", Status: StatusOpen, MetadataJSON: "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ticketRecord{}).Where("id = ?", "ticket").Update("log_channel_discord_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := NewStore(db)
	ticket := &Ticket{ID: "ticket", GuildID: "guild"}
	if err := store.reserveQueueSend(ctx, ticket, "queue"); err != nil {
		t.Fatal(err)
	}
	restarted := NewStore(db)
	if err := restarted.reserveQueueSend(ctx, &Ticket{ID: "ticket", GuildID: "guild"}, "queue"); !errors.Is(err, ErrQueueDeliveryUnknown) {
		t.Fatal(err)
	}
	oldAttempt := *ticket
	if err := store.releaseQueueSend(ctx, ticket, "queue"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.reserveQueueSend(ctx, ticket, "queue"); err != nil {
		t.Fatal(err)
	}
	if oldAttempt.QueueDeliveryAttemptID == "" || ticket.QueueDeliveryAttemptID == oldAttempt.QueueDeliveryAttemptID {
		t.Fatal("new send reused its predecessor's token")
	}
	if err := store.releaseQueueSend(ctx, &oldAttempt, "queue"); !errors.Is(err, ErrQueueDeliveryUnknown) {
		t.Fatal("old failure released newer send", err)
	}
	if err := store.saveQueueReceipt(ctx, &oldAttempt, "queue", "late-message", ""); !errors.Is(err, ErrQueueDeliveryUnknown) {
		t.Fatal("late receipt overwrote newer send", err)
	}
	if err := restarted.saveQueueReceipt(ctx, ticket, "queue", "message", ""); err != nil {
		t.Fatal(err)
	}
	if ticket.QueueDeliveryAttemptID != "" {
		t.Fatal("confirmed receipt retained uncertain token")
	}
	if err := store.releaseQueueSend(ctx, ticket, "queue"); !errors.Is(err, ErrQueueDeliveryUnknown) {
		t.Fatal("released confirmed receipt", err)
	}
	if err := store.reserveQueueSend(ctx, ticket, "queue"); !errors.Is(err, ErrQueueDeliveryUnknown) {
		t.Fatal("repeated confirmed send", err)
	}
}
