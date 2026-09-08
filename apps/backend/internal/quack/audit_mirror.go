package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// ErrAuditMirrorChannelUnavailable classifies a removed or inaccessible configured staff channel.
var ErrAuditMirrorChannelUnavailable = errors.New("audit mirror channel unavailable")

// AuditMirrorMessage is the redacted transport-neutral event sent to the configured staff channel.
type AuditMirrorMessage struct {
	AuditEntryID       string
	DiscordGuildID     string
	ChannelDiscordID   string
	OccurredAt         time.Time
	ActorDiscordUserID string
	Action             string
	ResourceType       string
	ResourceID         string
	Result             model.AuditResult
	FailureReason      string
	RequestID          string
	CorrelationID      string
	MetadataJSON       string
}

// AuditMirrorSender delivers one already-redacted important event to Discord.
// It is deliberately separate from the optional general-logging adapter.
type AuditMirrorSender interface {
	SendAuditMirror(context.Context, AuditMirrorMessage) error
}

// AuditMirrorRepository supplies immutable events and the managed destination.
type AuditMirrorRepository interface {
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	GetGuildByID(context.Context, string) (*model.Guild, error)
	SaveAuditMirrorDelivery(context.Context, string, bool, time.Time) error
	ClearGuildChannelReferences(context.Context, string, string, *model.AuditLogEntry) (*model.GuildSettings, error)
	ListPendingAuditMirrorEntries(context.Context, int) ([]model.AuditLogEntry, error)
}

// AuditMirrorWorker polls immutable audit history and mirrors important events
// out of band so Discord availability never blocks the originating operation.
type AuditMirrorWorker struct {
	store    AuditMirrorRepository
	sender   AuditMirrorSender
	interval time.Duration
	batch    int
	pollMu   sync.Mutex
}

// NewAuditMirrorWorker constructs the optional audit mirror worker.
func NewAuditMirrorWorker(store AuditMirrorRepository, sender AuditMirrorSender, interval time.Duration) *AuditMirrorWorker {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &AuditMirrorWorker{store: store, sender: sender, interval: interval, batch: 50}
}

// Run polls until cancellation. A failed poll is retried later and does not stop moderation work.
func (w *AuditMirrorWorker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.PollOnce(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "Audit mirror poll failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// PollOnce processes one bounded batch and serializes concurrent poll triggers.
func (w *AuditMirrorWorker) PollOnce(ctx context.Context) error {
	if w == nil || w.store == nil {
		return errors.New("audit mirror worker is not configured")
	}
	w.pollMu.Lock()
	defer w.pollMu.Unlock()
	entries, err := w.store.ListPendingAuditMirrorEntries(ctx, w.batch)
	if err != nil {
		return err
	}
	var failures []error
	for i := range entries {
		if err := w.process(ctx, entries[i]); err != nil {
			failures = append(failures, err)
			if ctx.Err() != nil {
				break
			}
		}
	}
	return errors.Join(failures...)
}

func (w *AuditMirrorWorker) process(ctx context.Context, entry model.AuditLogEntry) error {
	settings, err := w.store.GetGuildSettings(ctx, entry.GuildID)
	if err != nil {
		return w.recordDelivery(ctx, entry, false, "settings_unavailable")
	}
	if settings == nil || strings.TrimSpace(settings.AuditMirrorChannelDiscordID) == "" {
		return w.recordDelivery(ctx, entry, true, "not_configured")
	}
	guild, err := w.store.GetGuildByID(ctx, entry.GuildID)
	if err != nil || guild == nil {
		return w.recordDelivery(ctx, entry, false, "guild_unavailable")
	}
	if w.sender == nil {
		return w.recordDelivery(ctx, entry, false, "sender_unavailable")
	}
	message := AuditMirrorMessage{AuditEntryID: entry.ID, DiscordGuildID: guild.DiscordGuildID, ChannelDiscordID: settings.AuditMirrorChannelDiscordID, OccurredAt: entry.CreatedAt, ActorDiscordUserID: entry.ActorDiscordUserID, Action: entry.Action, ResourceType: entry.ResourceType, ResourceID: entry.ResourceID, Result: entry.Result, FailureReason: entry.FailureReason, RequestID: entry.RequestID, CorrelationID: entry.CorrelationID, MetadataJSON: model.RedactAuditMetadata(entry.MetadataJSON)}
	if err := w.sender.SendAuditMirror(ctx, message); err != nil {
		if errors.Is(err, ErrAuditMirrorChannelUnavailable) {
			if recordErr := w.recordDelivery(ctx, entry, false, "channel_unavailable"); recordErr != nil {
				return recordErr
			}
			repair := &model.AuditLogEntry{GuildID: entry.GuildID, ActorDiscordUserID: "quack-system", Source: model.AuditSourceSystem, Action: string(model.AuditActionMirrorRepaired), ResourceType: "guild_settings", ResourceID: settings.ID, Result: model.AuditResultSuccess, CorrelationID: entry.CorrelationID, MetadataJSON: auditMirrorMetadata(entry.ID, map[string]any{"cleared_channel_reference": true})}
			_, clearErr := w.store.ClearGuildChannelReferences(ctx, entry.GuildID, settings.AuditMirrorChannelDiscordID, repair)
			return clearErr
		}
		return w.recordDelivery(ctx, entry, false, "delivery_failed")
	}
	return w.recordDelivery(ctx, entry, true, "")
}

// recordDelivery keeps delivery progress separate from the moderation events it transports.
func (w *AuditMirrorWorker) recordDelivery(ctx context.Context, original model.AuditLogEntry, finished bool, failure string) error {
	retryAt := time.Now().UTC().Add(time.Minute)
	if !finished {
		slog.WarnContext(ctx, "Audit mirror delivery failed", "audit_entry_id", original.ID, "guild_id", original.GuildID, "reason", failure)
	}
	return w.store.SaveAuditMirrorDelivery(ctx, original.ID, finished, retryAt)
}

func auditMirrorMetadata(originalID string, extra map[string]any) string {
	metadata := map[string]any{"audit_entry_id": originalID}
	for key, value := range extra {
		metadata[key] = value
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return "{}"
	}
	return string(body)
}

// FormatAuditMirrorLine returns a bounded fallback description for adapters without embeds.
func FormatAuditMirrorLine(message AuditMirrorMessage) string {
	return fmt.Sprintf("%s · %s · %s/%s · %s", message.Action, message.Result, message.ResourceType, message.ResourceID, message.CorrelationID)
}
