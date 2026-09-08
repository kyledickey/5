package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	storage "github.com/quackdiscord/bot/internal/store"
)

// TestAuditRejectsServiceEvents checks raw storage, so a display-only filter
// cannot accidentally satisfy the product's no-technical-history requirement.
func TestAuditRejectsServiceEvents(t *testing.T) {
	repository, guildID := templateTestStore(t)
	ctx := context.Background()
	for _, action := range []string{"case_template.read", "audit.read", "audit_mirror.skipped", "audit_mirror.failed", "case_action.attempt", "unknown.service"} {
		if err := repository.CreateAuditLogEntry(ctx, &model.AuditLogEntry{GuildID: guildID, Action: action}); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := repository.DB().Model(&model.AuditLogEntry{}).Where("guild_id = ?", guildID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("technical rows persisted: count=%d err=%v", count, err)
	}
	entry := model.AuditLogEntry{GuildID: guildID, Action: "case.create", Source: model.AuditSourceDiscord, Result: model.AuditResultSuccess}
	if err := repository.CreateAuditLogEntry(ctx, &entry); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveAuditMirrorDelivery(ctx, entry.ID, false, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	pending, err := repository.ListPendingAuditMirrorEntries(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("failed delivery ignored backoff: %+v err=%v", pending, err)
	}
	if err := repository.SaveAuditMirrorDelivery(ctx, entry.ID, false, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	pending, err = repository.ListPendingAuditMirrorEntries(ctx, 10)
	if err != nil || len(pending) != 1 || pending[0].ID != entry.ID {
		t.Fatalf("failed delivery did not become retryable: %+v err=%v", pending, err)
	}
	if err := repository.SaveAuditMirrorDelivery(ctx, entry.ID, true, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	pending, err = repository.ListPendingAuditMirrorEntries(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("completed delivery became retryable: %+v err=%v", pending, err)
	}
	if err := repository.DB().Model(&model.AuditLogEntry{}).Where("guild_id = ?", guildID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("delivery bookkeeping added staff events: count=%d err=%v", count, err)
	}
}

func TestAuditRowsAreAppendOnlyAndRedactedAtStorageBoundary(t *testing.T) {
	repository, guildID := templateTestStore(t)
	entry := model.AuditLogEntry{GuildID: guildID, Source: model.AuditSourceAPI, Action: string(model.AuditActionSettingsUpdate), ResourceType: "guild_settings", ResourceID: "settings", Result: model.AuditResultFailure, FailureReason: "token=top-secret", MetadataJSON: `{"authorization":"Bearer secret","safe":"value"}`}
	if err := repository.CreateAuditLogEntry(context.Background(), &entry); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.ListAuditLogEntriesFiltered(context.Background(), model.ListAuditLogEntriesParams{GuildID: guildID, Limit: 10})
	if err != nil || len(stored.Entries) != 1 {
		t.Fatalf("read stored audit: %+v err=%v", stored, err)
	}
	if stored.Entries[0].MetadataJSON != `{"authorization":"[REDACTED]","safe":"value"}` || stored.Entries[0].FailureReason != "sensitive failure detail redacted" {
		t.Fatalf("storage boundary did not redact audit: %+v", stored.Entries[0])
	}
	if err := repository.DB().Model(&model.AuditLogEntry{}).Where("id = ?", entry.ID).Update("result", model.AuditResultSuccess).Error; !errors.Is(err, storage.ErrAuditImmutable) {
		t.Fatalf("expected update rejection, got %v", err)
	}
	if err := repository.DB().Delete(&model.AuditLogEntry{}, "id = ?", entry.ID).Error; !errors.Is(err, storage.ErrAuditImmutable) {
		t.Fatalf("expected delete rejection, got %v", err)
	}
}
