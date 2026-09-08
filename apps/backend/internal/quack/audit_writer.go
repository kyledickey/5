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

// recordAudit preserves the storage error while making lost audit evidence
// observable. It never emits the entry body, actor input, or raw driver error.
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
		logger := slog.Default()
		if entry != nil {
			logger = logger.With("guild_id", entry.GuildID, "action", entry.Action, "resource_id", entry.ResourceID)
		}
		logger.ErrorContext(ctx, "Audit entry could not be recorded")
	}
	return err
}
