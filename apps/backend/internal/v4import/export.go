package v4import

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Export reads one legacy guild in a read-only snapshot and emits import-ready
// JSONL. The caller supplies the explicit Discord guild ID to v5 guild ULID
// mapping; legacy case IDs remain stable and never become invented case numbers.
// Buffering the bounded result prevents validation failures from emitting a
// partial export. No Discord calls or source database writes are performed.
func Export(ctx context.Context, db *sql.DB, discordGuildID, targetGuildID string, output io.Writer) (int, error) {
	discordGuildID, targetGuildID = strings.TrimSpace(discordGuildID), strings.TrimSpace(targetGuildID)
	if db == nil || discordGuildID == "" || len(discordGuildID) > 32 || targetGuildID == "" || len(targetGuildID) > 26 {
		return 0, errors.New("source database, legacy Discord guild ID, and target v5 guild ULID are required")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return 0, fmt.Errorf("open read-only legacy snapshot: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id, user_id, moderator_id, reason, type, created_at, context_url FROM cases WHERE guild_id = ? ORDER BY created_at, id", discordGuildID)
	if err != nil {
		return 0, fmt.Errorf("read legacy cases: %w", err)
	}
	defer rows.Close()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	count := 0
	for rows.Next() {
		row := LegacyCase{Format: FormatVersion, GuildID: targetGuildID}
		var action int
		var contextURL sql.NullString
		if err := rows.Scan(&row.SourceID, &row.TargetDiscordUserID, &row.ModeratorDiscordUserID, &row.Reason, &action, &row.CreatedAt, &contextURL); err != nil {
			return 0, fmt.Errorf("decode legacy row %d: %w", count+1, err)
		}
		actions := [...]string{"warning", "ban", "kick", "unban", "timeout", "message_delete"}
		if action < 0 || action >= len(actions) {
			return 0, fmt.Errorf("legacy row %d: unsupported_action_type", count+1)
		}
		row.ActionType = actions[action]
		row.ContextURL = contextURL.String
		row.CreatedAt = row.CreatedAt.UTC()
		if code := validate(row, targetGuildID); code != "" {
			return 0, fmt.Errorf("legacy row %d: %s", count+1, code)
		}
		start := buffer.Len()
		if err := encoder.Encode(row); err != nil {
			return 0, err
		}
		if buffer.Len()-start >= 2<<20 {
			return 0, fmt.Errorf("legacy row %d: row_too_large", count+1)
		}
		if buffer.Len() > maxImportBytes {
			return 0, errors.New("export exceeds 64 MiB import limit; split source into bounded snapshots before importing")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read legacy snapshot: %w", err)
	}
	if count == 0 {
		return 0, errors.New("legacy guild has no cases; verify the source guild mapping")
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("finish read-only legacy snapshot: %w", err)
	}
	_, err = buffer.WriteTo(output)
	return count, err
}
