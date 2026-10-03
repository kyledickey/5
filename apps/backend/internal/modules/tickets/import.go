package tickets

import (
	"context"
	"errors"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
)

type LegacyTicket struct {
	SourceID, GuildID, OwnerDiscordUserID, ThreadDiscordChannelID string
	Status                                                        Status
	CreatedAt                                                     time.Time
}

type ImportResult struct {
	SourceID, TargetID   string
	WouldCreate, Created bool
}

// Importer migrates ticket records separately from historical moderation cases.
type Importer struct {
	store   *Store
	auditor modules.Auditor
	now     func() time.Time
}

func NewImporter(store *Store, auditor modules.Auditor) *Importer {
	return &Importer{store: store, auditor: auditor, now: func() time.Time { return time.Now().UTC() }}
}

// Import validates every row first, supports side-effect-free dry runs, and
// deduplicates against the durable modules.ImportRecord ledger.
func (i *Importer) Import(ctx context.Context, actor Actor, rows []LegacyTicket, dryRun bool) ([]ImportResult, error) {
	if !actor.CanManage {
		return nil, ErrPermissionDenied
	}
	results := make([]ImportResult, 0, len(rows))
	for _, row := range rows {
		if row.SourceID == "" || row.GuildID != actor.GuildID || row.OwnerDiscordUserID == "" {
			return nil, errors.New("invalid v4 ticket import row")
		}
		if row.Status != StatusOpen && row.Status != StatusResolved && row.Status != StatusCancelled {
			return nil, errors.New("invalid v4 ticket status")
		}
		if dryRun {
			targetID, exists, err := i.store.importTarget(ctx, row.GuildID, row.SourceID)
			if err != nil {
				return nil, err
			}
			results = append(results, ImportResult{SourceID: row.SourceID, TargetID: targetID, WouldCreate: !exists})
			continue
		}
		targetID, created, err := i.store.importTicket(ctx, row, i.now())
		if err != nil {
			return nil, err
		}
		results = append(results, ImportResult{SourceID: row.SourceID, TargetID: targetID, Created: created})
	}
	if !dryRun && i.auditor != nil {
		_ = i.auditor.RecordModuleAudit(ctx, modules.AuditEvent{GuildID: actor.GuildID, ActorDiscordUserID: actor.DiscordUserID, Action: "ticket.v4_import", ResourceType: "ticket_import", Result: "success", MetadataJSON: "{}"})
	}
	return results, nil
}
