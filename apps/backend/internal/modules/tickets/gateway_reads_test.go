package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/gorm"
)

// TestGatewayTicketReads preserves guild isolation, all-status deletion lookup,
// and bounded open-only repair traversal independently of ticket enablement.
func TestGatewayTicketReads(t *testing.T) {
	db, service, _ := journalSetup(t)
	ctx := context.Background()
	for i := 102; i >= 0; i-- {
		if err := db.Table("tickets").Create(map[string]any{
			"id": fmt.Sprintf("ticket-%03d", i), "guild_id": "repair-guild",
			"thread_discord_channel_id": fmt.Sprintf("thread-%03d", i),
			"owner_discord_user_id":     fmt.Sprintf("owner-%03d", i),
			"status":                    tickets.StatusOpen, "metadata_json": "{}",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Interleave excluded records so a broad query changes both pages' contents.
	for _, record := range []map[string]any{
		{"id": "ticket-050-other", "guild_id": "other-guild", "thread_discord_channel_id": "other-thread", "owner_discord_user_id": "other", "status": tickets.StatusOpen, "metadata_json": "{}"},
		{"id": "ticket-050-resolved", "guild_id": "repair-guild", "thread_discord_channel_id": "resolved-thread", "owner_discord_user_id": "resolved", "status": tickets.StatusResolved, "metadata_json": "{}"},
	} {
		if err := db.Table("tickets").Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		guild, channel, want string
	}{
		{"repair-guild", "thread-000", "ticket-000"},
		{"repair-guild", "resolved-thread", "ticket-050-resolved"},
		{"other-guild", "thread-000", ""},
		{"repair-guild", "unknown", ""},
	} {
		id, err := service.DeletedChannelTicketID(ctx, test.guild, test.channel)
		if test.want == "" {
			if !errors.Is(err, tickets.ErrNotFound) || id != "" {
				t.Fatalf("missing lookup = %q, %v", id, err)
			}
		} else if err != nil || id != test.want {
			t.Fatalf("lookup = %q, %v; want %q", id, err, test.want)
		}
	}
	first, err := service.OpenThreadRepairPage(ctx, "repair-guild", "")
	if err != nil || len(first) != 100 {
		t.Fatalf("first page length = %d, err = %v", len(first), err)
	}
	second, err := service.OpenThreadRepairPage(ctx, "repair-guild", first[len(first)-1].ID)
	if err != nil || len(second) != 3 {
		t.Fatalf("second page length = %d, err = %v", len(second), err)
	}
	for i, target := range append(first, second...) {
		if target.ID != fmt.Sprintf("ticket-%03d", i) || target.ThreadDiscordChannelID != fmt.Sprintf("thread-%03d", i) || target.OwnerDiscordUserID != fmt.Sprintf("owner-%03d", i) {
			t.Fatalf("repair target %d = %+v", i, target)
		}
	}
	last, err := service.OpenThreadRepairPage(ctx, "repair-guild", second[len(second)-1].ID)
	if err != nil || len(last) != 0 {
		t.Fatalf("terminal page = %+v, %v", last, err)
	}
}

// TestGatewayTicketReadFailures ensures a failed lookup cannot be mistaken for
// a missing ticket or an exhausted repair page by gateway callers.
func TestGatewayTicketReadFailures(t *testing.T) {
	db, service, _ := journalSetup(t)
	want := errors.New("ticket lookup failed")
	if err := db.Callback().Query().Before("gorm:query").Register("fail_gateway_reads", func(tx *gorm.DB) {
		if tx.Statement.Table == "tickets" {
			tx.AddError(want)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeletedChannelTicketID(context.Background(), "guild", "thread"); !errors.Is(err, want) {
		t.Fatal("deleted channel lookup error", err)
	}
	if _, err := service.OpenThreadRepairPage(context.Background(), "guild", ""); !errors.Is(err, want) {
		t.Fatal("repair page error", err)
	}
}
