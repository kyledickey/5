package store

import (
	"time"

	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// prepareULIDModel assigns a fresh ULID when the row has no ID yet, backfills
// CreatedAt when unset, and always stamps UpdatedAt with now. Call it before
// every Create so identity and timestamps are decided by the store, not callers.
func prepareULIDModel(row *model.ULIDModel, now time.Time) {
	if row.ID == "" {
		row.ID = idutil.NewULID()
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
}
