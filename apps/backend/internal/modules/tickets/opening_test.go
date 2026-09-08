package tickets

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestOpeningReservationFencesExpiredWorkers proves a lost provisional worker
// cannot create a ticket after another request has reclaimed the member slot.
func TestOpeningReservationFencesExpiredWorkers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migration().Apply(db); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db)
	actor := Actor{GuildID: "guild", DiscordUserID: "member"}
	now := time.Now().UTC()
	first, err := s.reserveOpening(context.Background(), actor, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.reserveOpening(context.Background(), actor, now); !errors.Is(err, ErrDuplicateOpen) {
		t.Fatalf("concurrent opening was not denied: %v", err)
	}
	later := now.Add(ticketOpeningTTL + time.Second)
	second, err := s.reserveOpening(context.Background(), actor, later)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishOpening(context.Background(), actor, first, "old-channel", later); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("stale worker completed: %v", err)
	}
	if err := s.releaseOpening(context.Background(), actor, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishOpening(context.Background(), actor, second, "new-channel", later); err != nil {
		t.Fatal(err)
	}
}
