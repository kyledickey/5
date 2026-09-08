package honeypot_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"gorm.io/gorm"
)

// TestWarningRefreshCoalescing preserves newer requests and uncertain replacement
// identity while using one durable row for any number of incident notifications.
func TestWarningRefreshCoalescing(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	enable(t, f, "guild-a")
	for range 10 {
		if err := f.service.RequestWarningRefresh(ctx, "guild-a"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := f.service.WarningRefreshes(ctx, time.Now().Add(time.Second))
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	old := rows[0]
	if err := f.service.RequestWarningRefresh(ctx, "guild-a"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CompleteWarningRefresh(ctx, old, false); err != nil {
		t.Fatal(err)
	}
	rows, err = f.service.WarningRefreshes(ctx, time.Now().Add(time.Second))
	if err != nil || len(rows) != 1 || rows[0].Revision == old.Revision {
		t.Fatal(rows, err)
	}
	settings := honeypot.Settings{ChannelDiscordID: "trap", WarningMessageID: "old"}
	if err := f.service.ReserveWarningSend(ctx, "guild-a", settings); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ReserveWarningSend(ctx, "guild-a", settings); !errors.Is(err, honeypot.ErrWarningDeliveryUnknown) {
		t.Fatal(err)
	}
	if err := f.service.ReleaseWarningSend(ctx, "guild-a", settings); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ReserveWarningSend(ctx, "guild-a", settings); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CompleteWarningRefresh(ctx, rows[0], false); err != nil {
		t.Fatal(err)
	}
	rows, err = f.service.WarningRefreshes(ctx, time.Now().Add(time.Second))
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

// queuedWarningObserver models the production observer's persistence-only path.
type queuedWarningObserver struct{ service *honeypot.Service }

// IncidentCreated schedules presentation without another moderation operation.
func (o queuedWarningObserver) IncidentCreated(ctx context.Context, guildID string) error {
	return o.service.RequestWarningRefresh(ctx, guildID)
}

// TestPresentationRequestsDoNotRepeatModeration keeps burst duplicates at one
// case while the derived warning remains independently retryable.
func TestPresentationRequestsDoNotRepeatModeration(t *testing.T) {
	f := setup(t)
	enable(t, f, "guild-a")
	runtime := honeypot.NewRuntime(context.Background(), honeypot.NewDiscordAdapter(f.service), 8, 1, queuedWarningObserver{f.service})
	for range 2 {
		if err := runtime.Submit(message("same")); err != nil {
			t.Fatal(err)
		}
	}
	runtime.Close()
	if f.applier.count() != 1 {
		t.Fatal("repeated moderation", f.applier.count())
	}
	rows, err := f.service.WarningRefreshes(context.Background(), time.Now().Add(time.Second))
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	for range 3 {
		if err := f.service.CompleteWarningRefresh(context.Background(), rows[0], true); err != nil {
			t.Fatal(err)
		}
	}
	if f.applier.count() != 1 {
		t.Fatal("presentation retried moderation")
	}
}

// TestIncidentCompletionAndWarningRequestAreAtomic injects a refresh-store error
// after the outcome update and proves recovery still owns a pending incident.
func TestIncidentCompletionAndWarningRequestAreAtomic(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	store := honeypot.NewStore(f.db)
	trigger, _, err := store.Claim(ctx, message("atomic"), "template", honeypot.OutcomePending)
	if err != nil {
		t.Fatal(err)
	}
	const callback = "test:fail_warning_request"
	if err := f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "honeypot_warning_refreshes" {
			tx.AddError(errors.New("warning storage unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, trigger.ID, honeypot.OutcomeCreated, "case", ""); err == nil {
		t.Fatal("partial completion succeeded")
	}
	if err := f.db.Callback().Create().Remove(callback); err != nil {
		t.Fatal(err)
	}
	var saved honeypot.Trigger
	if err := f.db.First(&saved, "id = ?", trigger.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Outcome != honeypot.OutcomePending || saved.CaseID != "" {
		t.Fatal("completion was not rolled back", saved)
	}
	var count int64
	if err := f.db.Model(&honeypot.WarningRefresh{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := store.Complete(ctx, trigger.ID, honeypot.OutcomeCreated, "case", ""); err != nil {
		t.Fatal(err)
	}
	rows, err := f.service.WarningRefreshes(ctx, time.Now().Add(time.Second))
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if err := store.Complete(ctx, trigger.ID, honeypot.OutcomeCreated, "case", ""); !errors.Is(err, honeypot.ErrDuplicate) {
		t.Fatal(err)
	}
	next, err := f.service.WarningRefreshes(ctx, time.Now().Add(time.Second))
	if err != nil || len(next) != 1 || next[0].Revision != rows[0].Revision {
		t.Fatal("duplicate completion enqueued", next, err)
	}
}
