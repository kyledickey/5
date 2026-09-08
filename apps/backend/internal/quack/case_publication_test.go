package quack

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// publicationUseCaseStore exposes only delivery bookkeeping in these tests;
// embedded unrelated operations panic if publication accidentally invokes them.
type publicationUseCaseStore struct {
	CaseRepository
	attempts int
	failures int
	readErr  error
}

// SaveCasePublication models transient storage failure without changing a case.
func (r *publicationUseCaseStore) SaveCasePublication(context.Context, model.CasePublication) error {
	r.attempts++
	if r.attempts <= r.failures {
		return errors.New("storage unavailable")
	}
	return nil
}

// ListCaseActionExecutions supplies private fields to verify the public projection.
func (r *publicationUseCaseStore) ListCaseActionExecutions(context.Context, string) ([]model.CaseActionExecution, error) {
	return []model.CaseActionExecution{{ULIDModel: model.ULIDModel{ID: "execution"}, Status: model.ActionExecutionSucceeded, ConfigSnapshotJSON: "private configuration"}}, r.readErr
}

// TestRecordPublicReceiptKeepsBoundedRetries verifies successful recovery,
// persistent outage, cancellation, and adapters without publication capability.
func TestRecordPublicReceiptKeepsBoundedRetries(t *testing.T) {
	for _, failures := range []int{0, 2, 3} {
		repository := &publicationUseCaseStore{failures: failures}
		err := NewCaseService(repository).RecordPublicReceipt(context.Background(), model.CasePublication{})
		want := failures + 1
		if want > 3 {
			want = 3
		}
		if repository.attempts != want || (err != nil) != (failures == 3) {
			t.Fatalf("%d failures: %d attempts %v", failures, repository.attempts, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repository := &publicationUseCaseStore{failures: 3}
	if err := NewCaseService(repository).RecordPublicReceipt(ctx, model.CasePublication{}); err == nil || repository.attempts != 1 {
		t.Fatalf("cancellation: %v, %d attempts", err, repository.attempts)
	}
	if err := NewCaseService(struct{ CaseRepository }{}).RecordPublicReceipt(context.Background(), model.CasePublication{}); err == nil {
		t.Fatal("missing capability accepted")
	}
}

// TestPublicReceiptStatusesExcludeExecutionDetails keeps sensitive execution
// payloads out of the command-facing read boundary and propagates read failures.
func TestPublicReceiptStatusesExcludeExecutionDetails(t *testing.T) {
	repository := &publicationUseCaseStore{}
	service := NewCaseService(repository)
	got, err := service.PublicReceiptActionStatuses(context.Background(), "case")
	want := []CaseActionResponse{{ID: "execution", Status: model.ActionExecutionSucceeded}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("projection: %+v %v", got, err)
	}
	repository.readErr = errors.New("unavailable")
	if got, err := service.PublicReceiptActionStatuses(context.Background(), "case"); !errors.Is(err, repository.readErr) || got != nil {
		t.Fatalf("read failure: %+v %v", got, err)
	}
}
