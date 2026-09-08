package moduleintegration

import (
	"context"
	"log/slog"
	"time"
)

// repairDeletedHoneypotWarning handles gateway deletions with bounded work. It is
// independent of general logging enablement and never changes moderation outcomes.
func (r *Runtime) repairDeletedHoneypotWarning(guildID, channelID string, messageIDs []string) {
	if r.honeypotCounter == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.honeypotCounter.WarningDeleted(ctx, guildID, channelID, messageIDs); err != nil {
		slog.WarnContext(ctx, "Could not restore deleted honeypot warning", "guild_id", guildID, "error", err)
	}
}
