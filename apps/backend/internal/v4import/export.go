package v4import

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

// ExportOptions selects a deterministic page from an unchanged restored source.
// Limit zero preserves all-guild export and requires Offset zero.
type ExportOptions struct {
	Limit  int
	Offset int64
}

// Validate rejects unusable bounds before a source connection is accessed.
func (o ExportOptions) Validate() error {
	if o.Limit < 0 || o.Limit > 100000 || o.Offset < 0 || o.Offset > math.MaxInt64-int64(o.Limit) || (o.Limit == 0 && o.Offset != 0) {
		return errors.New("export requires --limit between 1 and 100000 with a nonnegative --offset (or both zero for all cases)")
	}
	return nil
}

// ExportResult describes the complete emitted page and its continuation.
// NextOffset is meaningful only when HasMore is true.
type ExportResult struct {
	Count      int
	HasMore    bool
	NextOffset int64
}

// Export reads one legacy guild in a read-only snapshot and emits import-ready
// JSONL. The caller supplies the explicit Discord guild ID to v5 guild ULID
// mapping; legacy case IDs remain stable and never become invented case numbers.
// Buffering the bounded result prevents validation failures from emitting a
// partial export. No Discord calls or source database writes are performed.
func Export(ctx context.Context, db *sql.DB, discordGuildID, targetGuildID string, output io.Writer) (int, error) {
	result, err := ExportPage(ctx, db, discordGuildID, targetGuildID, output, ExportOptions{})
	return result.Count, err
}

// ExportPage emits one validated page, ordered by timestamp and unique source ID.
// Pages must use the same unchanged restored database: separate read-only
// transactions cannot protect offsets against concurrent source changes.
func ExportPage(ctx context.Context, db *sql.DB, discordGuildID, targetGuildID string, output io.Writer, options ExportOptions) (ExportResult, error) {
	if err := options.Validate(); err != nil {
		return ExportResult{}, err
	}
	discordGuildID, targetGuildID = strings.TrimSpace(discordGuildID), strings.TrimSpace(targetGuildID)
	if db == nil || discordGuildID == "" || len(discordGuildID) > 32 || targetGuildID == "" || len(targetGuildID) > 26 {
		return ExportResult{}, errors.New("source database, legacy Discord guild ID, and target v5 guild ULID are required")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return ExportResult{}, fmt.Errorf("open read-only legacy snapshot: %w", err)
	}
	defer tx.Rollback()
	query := "SELECT id, user_id, moderator_id, reason, type, created_at, context_url FROM cases WHERE guild_id = ? ORDER BY created_at, id"
	args := []any{discordGuildID}
	if options.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, options.Limit+1, options.Offset)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return ExportResult{}, fmt.Errorf("read legacy cases: %w", err)
	}
	defer rows.Close()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	count := 0
	hasMore := false
	for rows.Next() {
		if options.Limit > 0 && count == options.Limit {
			hasMore = true
			break
		}
		row := LegacyCase{Format: FormatVersion, GuildID: targetGuildID}
		var action int
		var contextURL sql.NullString
		if err := rows.Scan(&row.SourceID, &row.TargetDiscordUserID, &row.ModeratorDiscordUserID, &row.Reason, &action, &row.CreatedAt, &contextURL); err != nil {
			return ExportResult{}, fmt.Errorf("decode legacy row %d: %w", count+1, err)
		}
		actions := [...]string{"warning", "ban", "kick", "unban", "timeout", "message_delete"}
		if action < 0 || action >= len(actions) {
			return ExportResult{}, fmt.Errorf("legacy row %d: unsupported_action_type", count+1)
		}
		row.ActionType = actions[action]
		row.ContextURL = contextURL.String
		row.CreatedAt = row.CreatedAt.UTC()
		if code := validate(row, targetGuildID); code != "" {
			return ExportResult{}, fmt.Errorf("legacy row %d: %s", count+1, code)
		}
		start := buffer.Len()
		if err := encoder.Encode(row); err != nil {
			return ExportResult{}, err
		}
		if buffer.Len()-start >= 2<<20 {
			return ExportResult{}, fmt.Errorf("legacy row %d: row_too_large", count+1)
		}
		if buffer.Len() > maxImportBytes {
			return ExportResult{}, errors.New("export exceeds 64 MiB import limit; retry with a smaller --limit at the same --offset")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return ExportResult{}, fmt.Errorf("read legacy snapshot: %w", err)
	}
	if count == 0 && options.Offset == 0 {
		return ExportResult{}, errors.New("legacy guild has no cases; verify the source guild mapping")
	}
	if err := rows.Close(); err != nil {
		return ExportResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExportResult{}, fmt.Errorf("finish read-only legacy snapshot: %w", err)
	}
	_, err = buffer.WriteTo(output)
	result := ExportResult{Count: count, HasMore: hasMore}
	if hasMore {
		result.NextOffset = options.Offset + int64(count)
	}
	return result, err
}
