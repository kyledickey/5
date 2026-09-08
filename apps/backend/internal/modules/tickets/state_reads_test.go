package tickets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/gorm"
)

// TestTicketStateOperationsDoNotLoadTimeline makes discarded history queries fail
// while exercising both transcript ports and the existing access/cleanup lifecycle.
func TestTicketStateOperationsDoNotLoadTimeline(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "legacy"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			db, service, _ := journalSetup(t)
			ctx := context.Background()
			client := &discordFake{}
			var transport tickets.DiscordClient = client
			if native {
				transport = &journalDiscordFake{discordFake: client}
			}
			adapter := tickets.NewDiscordAdapter(service, transport)
			owner := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
			staff := tickets.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true, CanManage: true}
			ticket, err := adapter.Open(ctx, owner)
			if err != nil {
				t.Fatal(err)
			}
			historyError := errors.New("unexpected timeline query")
			historyReads := 0
			if err := db.Callback().Query().Before("gorm:query").Register("reject_unused_timeline", func(tx *gorm.DB) {
				if tx.Statement.Table == "ticket_events" {
					historyReads++
					tx.AddError(historyError)
				}
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ClosurePending(ctx, owner, ticket.ID); err != nil {
				t.Fatal(err)
			}
			if err := adapter.Reply(ctx, owner, ticket.ID, "retained reply"); err != nil {
				t.Fatal(err)
			}
			if err := adapter.Join(ctx, staff, ticket.ID); err != nil {
				t.Fatal(err)
			}
			if err := adapter.RepairPermissions(ctx, staff, ticket.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Close(ctx, owner, ticket.ID); err != nil {
				t.Fatal(err)
			}
			if pending, err := service.ClosurePending(ctx, staff, ticket.ID); err != nil || pending {
				t.Fatal("closure state", pending, err)
			}
			if historyReads != 0 {
				t.Fatal("state operation queried timeline", historyReads)
			}
			// Detail still intentionally loads history and preserves its query failure.
			if _, _, err := service.Detail(ctx, owner, ticket.ID); !errors.Is(err, historyError) {
				t.Fatal("detail hid timeline failure", err)
			}
			if historyReads != 1 {
				t.Fatal("detail did not read history")
			}
			if err := db.Callback().Query().Remove("reject_unused_timeline"); err != nil {
				t.Fatal(err)
			}
			_, events, err := service.Detail(ctx, owner, ticket.ID)
			if err != nil || len(events) < 3 {
				t.Fatal("detail lost events", events, err)
			}
			for i := 1; i < len(events); i++ {
				if events[i].CreatedAt.Before(events[i-1].CreatedAt) {
					t.Fatal("timeline order changed")
				}
			}
		})
	}
}

// TestTicketStateReadsRecheckAuthority verifies no authorization result is cached
// and wrong-guild/unknown-record errors are retained before any history lookup.
func TestTicketStateReadsRecheckAuthority(t *testing.T) {
	db, service, _ := journalSetup(t)
	ctx := context.Background()
	ticket, err := service.Open(ctx, tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner"}, "thread")
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	if err := db.Callback().Query().Before("gorm:query").Register("count_state_history", func(tx *gorm.DB) {
		if tx.Statement.Table == "ticket_events" {
			reads++
		}
	}); err != nil {
		t.Fatal(err)
	}
	staff := tickets.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true}
	if _, err := service.ClosurePending(ctx, staff, ticket.ID); err != nil {
		t.Fatal(err)
	}
	staff.CanModerate = false
	if _, err := service.ClosurePending(ctx, staff, ticket.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatal("revoked staff retained access", err)
	}
	for _, scenario := range []struct {
		actor tickets.Actor
		id    string
		want  error
	}{
		{tickets.Actor{GuildID: "guild-a", DiscordUserID: "other"}, ticket.ID, tickets.ErrPermissionDenied},
		{tickets.Actor{GuildID: "other-guild", DiscordUserID: "owner", CanModerate: true}, ticket.ID, tickets.ErrNotFound},
		{tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner"}, "missing", tickets.ErrNotFound},
	} {
		if _, err := service.ClosurePending(ctx, scenario.actor, scenario.id); !errors.Is(err, scenario.want) {
			t.Fatal(err, scenario.want)
		}
		if _, _, err := service.Detail(ctx, scenario.actor, scenario.id); !errors.Is(err, scenario.want) {
			t.Fatal(err, scenario.want)
		}
	}
	if reads != 0 {
		t.Fatal("authorization failure loaded private history")
	}
	lookupError := errors.New("ticket storage unavailable")
	if err := db.Callback().Query().Before("gorm:query").Register("fail_ticket_lookup", func(tx *gorm.DB) {
		if tx.Statement.Table == "tickets" {
			tx.AddError(lookupError)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClosurePending(ctx, tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner"}, ticket.ID); !errors.Is(err, lookupError) {
		t.Fatal("state lookup failure hidden", err)
	}
}
