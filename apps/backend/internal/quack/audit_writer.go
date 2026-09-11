package quack

import (
	"context"
	"log/slog"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// auditWriter is the append-only capability used by audited service operations.
type auditWriter interface {
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
}

// recordAudit persists entry when its action is an important audit event and
// otherwise only logs it at debug level, so routine reads never enter staff
// history. A nil entry is a no-op. Storage errors are returned to the caller
// and logged with identifiers only: never the entry body, actor input, or raw
// driver error.
func recordAudit(ctx context.Context, writer auditWriter, entry *model.AuditLogEntry) error {
	if entry == nil {
		return nil
	}
	if !model.IsAuditEvent(entry.Action) {
		slog.DebugContext(ctx, "Service operation", "guild_id", entry.GuildID, "action", entry.Action, "result", entry.Result)
		return nil
	}
	err := writer.CreateAuditLogEntry(ctx, entry)
	if err != nil {
		slog.Default().
			With("guild_id", entry.GuildID, "action", entry.Action, "resource_id", entry.ResourceID).
			ErrorContext(ctx, "Audit entry could not be recorded")
	}
	return err
}
