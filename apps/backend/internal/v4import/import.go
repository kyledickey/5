// Package v4import defines Quack's bounded, historical-only v4 moderation import.
package v4import

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const FormatVersion = "quack-v4-case-jsonl/v1"

const maxImportBytes = 64 << 20

var (
	// ErrInvalidInput reports a JSONL row that cannot become historical moderation history.
	ErrInvalidInput = errors.New("invalid v4 import input")
	// ErrSourceCollision reports reuse of one source identity with different content.
	ErrSourceCollision = errors.New("v4 import source collision")
)

// LegacyCase is the final versioned JSONL contract accepted from Quack v4.
type LegacyCase struct {
	Format                 string     `json:"format"`
	SourceID               string     `json:"source_id"`
	CaseNumber             uint64     `json:"case_number"`
	GuildID                string     `json:"guild_id"`
	TargetDiscordUserID    string     `json:"target_discord_user_id"`
	ModeratorDiscordUserID string     `json:"moderator_discord_user_id,omitempty"`
	ModeratorDisplayName   string     `json:"moderator_display_name,omitempty"`
	Reason                 string     `json:"reason"`
	ActionType             string     `json:"action_type"`
	ContextURL             string     `json:"context_url,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	TargetDeparted         bool       `json:"target_departed,omitempty"`
	TargetMissing          bool       `json:"target_missing,omitempty"`
	ActionExpiresAt        *time.Time `json:"action_expires_at,omitempty"`
}

// PreparedCase is a validated row plus the content fingerprint used for durable idempotency.
type PreparedCase struct {
	Line        int
	Fingerprint string
	Case        LegacyCase
}

// Batch describes one source file without including member data in ledger or audit metadata.
type Batch struct {
	ID, GuildID, SourceName, Checksum, ActorDiscordUserID string
	RecordCount                                           int
}

// Decision describes one durable mapping or dry-run outcome.
type Decision struct {
	Line                                  int
	SourceCaseNumber                      uint64
	SourceID, TargetCaseID                string
	TargetCaseNumber                      uint64
	WouldCreate, Created, AlreadyImported bool
	Warnings                              []string
}

// Report is safe for operator output. Failures identify line and code without echoing member content.
type Report struct {
	BatchID, Checksum                      string
	DryRun                                 bool
	Total, Valid, Created, AlreadyImported int
	Warnings                               []Issue
	Failures                               []Issue
	Decisions                              []Decision
}

// Issue is a bounded diagnostic that never contains the imported reason or member identity.
type Issue struct {
	Line int    `json:"line"`
	Code string `json:"code"`
}

// Repository persists validated rows atomically and owns collision-safe case numbering.
type Repository interface {
	PreviewV4Import(context.Context, Batch, []PreparedCase) ([]Decision, error)
	ApplyV4Import(context.Context, Batch, []PreparedCase) ([]Decision, error)
	RollbackV4Import(context.Context, string, string, string) error
	RecordV4ImportFailure(context.Context, Batch, int, string) error
}

// Importer parses, validates, previews, and atomically imports one complete JSONL source.
type Importer struct{ repository Repository }

// New constructs a v4 historical-case importer.
func New(repository Repository) *Importer { return &Importer{repository: repository} }

// Import processes one complete source. Any malformed row prevents all writes.
func (i *Importer) Import(ctx context.Context, sourceName, guildID, actorID string, input io.Reader, dryRun bool) (*Report, error) {
	if i == nil || i.repository == nil {
		return nil, errors.New("v4 import repository is required")
	}
	if strings.TrimSpace(sourceName) == "" || strings.TrimSpace(guildID) == "" || strings.TrimSpace(actorID) == "" {
		return nil, fmt.Errorf("%w: source, guild, and actor are required", ErrInvalidInput)
	}
	if len(strings.TrimSpace(sourceName)) > 191 || len(strings.TrimSpace(guildID)) > 26 || len(strings.TrimSpace(actorID)) > 32 {
		return nil, fmt.Errorf("%w: source, guild, or actor is too long", ErrInvalidInput)
	}
	raw, err := io.ReadAll(io.LimitReader(input, maxImportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read v4 import: %w", err)
	}
	if len(raw) > maxImportBytes {
		return nil, fmt.Errorf("%w: source exceeds 64 MiB", ErrInvalidInput)
	}
	sum := sha256.Sum256(raw)
	checksum := hex.EncodeToString(sum[:])
	prepared, issues := parse(raw, guildID)
	identity := sha256.Sum256([]byte(strings.TrimSpace(guildID) + "\n" + strings.TrimSpace(sourceName) + "\n" + checksum))
	batch := Batch{ID: "v4-" + hex.EncodeToString(identity[:12]), GuildID: strings.TrimSpace(guildID), SourceName: strings.TrimSpace(sourceName), Checksum: checksum, ActorDiscordUserID: strings.TrimSpace(actorID), RecordCount: len(prepared) + len(issues)}
	report := &Report{BatchID: batch.ID, Checksum: checksum, DryRun: dryRun, Total: batch.RecordCount, Valid: len(prepared), Failures: issues}
	recordFailure := func(count int, code string) {
		if !dryRun {
			_ = i.repository.RecordV4ImportFailure(ctx, batch, count, code)
		}
	}
	if len(issues) != 0 {
		recordFailure(len(issues), "validation_failed")
		return report, fmt.Errorf("%w: %d row(s) failed validation", ErrInvalidInput, len(issues))
	}
	if len(prepared) == 0 {
		recordFailure(1, "empty_source")
		return report, fmt.Errorf("%w: source contains no records", ErrInvalidInput)
	}
	var decisions []Decision
	if dryRun {
		decisions, err = i.repository.PreviewV4Import(ctx, batch, prepared)
	} else {
		decisions, err = i.repository.ApplyV4Import(ctx, batch, prepared)
	}
	if err != nil {
		code := "storage_failed"
		if errors.Is(err, ErrSourceCollision) {
			code = "source_collision"
		}
		recordFailure(1, code)
		return report, err
	}
	report.Decisions = decisions
	for _, decision := range decisions {
		if decision.Created {
			report.Created++
		}
		if decision.AlreadyImported {
			report.AlreadyImported++
		}
		for _, code := range decision.Warnings {
			report.Warnings = append(report.Warnings, Issue{Line: decision.Line, Code: code})
		}
	}
	return report, nil
}

// Rollback removes one batch only when its historical cases have no dependent v5 state.
func (i *Importer) Rollback(ctx context.Context, guildID, batchID, actorID string) error {
	if i == nil || i.repository == nil {
		return errors.New("v4 import repository is required")
	}
	return i.repository.RollbackV4Import(ctx, strings.TrimSpace(guildID), strings.TrimSpace(batchID), strings.TrimSpace(actorID))
}

func parse(raw []byte, guildID string) ([]PreparedCase, []Issue) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	seen := make(map[string]struct{})
	var rows []PreparedCase
	var issues []Issue
	for line := 1; scanner.Scan(); line++ {
		body := bytes.TrimSpace(scanner.Bytes())
		if len(body) == 0 {
			continue
		}
		var row LegacyCase
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&row); err != nil {
			issues = append(issues, Issue{Line: line, Code: "malformed_json"})
			continue
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			issues = append(issues, Issue{Line: line, Code: "malformed_json"})
			continue
		}
		if code := validate(row, guildID); code != "" {
			issues = append(issues, Issue{Line: line, Code: code})
			continue
		}
		if _, duplicate := seen[row.SourceID]; duplicate {
			issues = append(issues, Issue{Line: line, Code: "duplicate_source_id"})
			continue
		}
		seen[row.SourceID] = struct{}{}
		canonical, _ := json.Marshal(row)
		digest := sha256.Sum256(canonical)
		rows = append(rows, PreparedCase{Line: line, Fingerprint: hex.EncodeToString(digest[:]), Case: row})
	}
	if scanner.Err() != nil {
		issues = append(issues, Issue{Line: len(rows) + len(issues) + 1, Code: "row_too_large"})
	}
	return rows, issues
}

func validate(row LegacyCase, guildID string) string {
	if row.Format != FormatVersion {
		return "unsupported_format"
	}
	if strings.TrimSpace(row.SourceID) == "" {
		return "missing_source_id"
	}
	if len(row.SourceID) > 191 {
		return "source_id_too_long"
	}
	if strings.TrimSpace(row.GuildID) != strings.TrimSpace(guildID) {
		return "guild_mismatch"
	}
	if strings.TrimSpace(row.TargetDiscordUserID) == "" {
		return "missing_target"
	}
	if len(row.TargetDiscordUserID) > 32 || len(row.ModeratorDiscordUserID) > 32 {
		return "discord_identity_too_long"
	}
	if strings.TrimSpace(row.Reason) == "" {
		return "missing_reason"
	}
	switch row.ActionType {
	case "warning", "timeout", "kick", "ban", "unban", "message_delete":
	default:
		return "unsupported_action_type"
	}
	if row.CreatedAt.IsZero() {
		return "missing_created_at"
	}
	return ""
}

// ValidateCommandScopes proves v4 and v5 command names cannot coexist and requires direct v4 moderation removal after cutover.
func ValidateCommandScopes(v4Names, v5Names []string, afterMigration bool) error {
	v5 := map[string]struct{}{}
	for _, name := range v5Names {
		v5[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	for _, name := range v4Names {
		name = strings.ToLower(strings.TrimSpace(name))
		if _, collision := v5[name]; collision {
			return fmt.Errorf("command scope collision: %s", name)
		}
		if afterMigration && (name == "warn" || name == "timeout" || name == "kick" || name == "ban") {
			return fmt.Errorf("legacy direct moderation command remains after migration: %s", name)
		}
	}
	return nil
}
