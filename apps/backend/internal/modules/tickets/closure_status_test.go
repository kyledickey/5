package tickets_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// TestClosurePendingUsesAuthorizedOwnerReservation covers failed deletion and
// successful cleanup for owner/staff without leaking another member's ticket.
func TestClosurePendingUsesAuthorizedOwnerReservation(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	owner := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	staff := tickets.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true}
	ticket, err := adapter.Open(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := service.ClosurePending(ctx, owner, ticket.ID); err != nil || pending {
		t.Fatal("open ticket closing", pending, err)
	}
	client.failArchive = 1
	if _, err := adapter.Close(ctx, owner, ticket.ID); err == nil {
		t.Fatal("expected deletion failure")
	}
	for _, actor := range []tickets.Actor{owner, staff} {
		if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || !pending {
			t.Fatal("lost cleanup state", pending, err)
		}
	}
	for _, actor := range []tickets.Actor{{GuildID: "guild-a", DiscordUserID: "other"}, {GuildID: "other-guild", DiscordUserID: "member", CanModerate: true}} {
		if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err == nil || pending {
			t.Fatal("leaked cleanup state", pending, err)
		}
	}
	if _, err := adapter.Close(ctx, owner, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Open(ctx, owner); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []tickets.Actor{owner, staff} {
		if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || pending {
			t.Fatal("old ticket uses newer reservation", pending, err)
		}
	}
}
