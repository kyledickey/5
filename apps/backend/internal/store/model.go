package store

import (
	"time"

	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// prepareULIDModel assigns a fresh ULID when the row has no ID yet, backfills
// CreatedAt when unset, and always stamps UpdatedAt with now. Call it before
// every Create so identity and timestamps are decided by the store, not callers.
func prepareULIDModel(model *model.ULIDModel, now time.Time) error {
	if model.ID == "" {
		id, err := idutil.NewULID()
		if err != nil {
			return err
		}
		model.ID = id
	}

	if model.CreatedAt.IsZero() {
		model.CreatedAt = now
	}
	model.UpdatedAt = now

	return nil
}
