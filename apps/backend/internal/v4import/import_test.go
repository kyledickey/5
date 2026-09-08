package v4import

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRepository struct {
	preview, applied []Decision
	failureCode      string
	failureCalls     int
}

func (f *fakeRepository) PreviewV4Import(_ context.Context, _ Batch, _ []PreparedCase) ([]Decision, error) {
	return f.preview, nil
}
func (f *fakeRepository) ApplyV4Import(_ context.Context, _ Batch, _ []PreparedCase) ([]Decision, error) {
	return f.applied, nil
}
func (f *fakeRepository) RollbackV4Import(context.Context, string, string, string) error { return nil }
func (f *fakeRepository) RecordV4ImportFailure(_ context.Context, _ Batch, _ int, code string) error {
	f.failureCode = code
	f.failureCalls++
	return nil
}

func TestImportRejectsTrailingDataAfterJSONLObject(t *testing.T) {
	valid := `{"format":"quack-v4-case-jsonl/v1","source_id":"source","guild_id":"guild","target_discord_user_id":"member","reason":"history","action_type":"warning","created_at":"2024-01-02T03:04:05Z"}`
	for _, trailing := range []string{valid, `garbage`} {
		repository := &fakeRepository{}
		report, err := New(repository).Import(context.Background(), "export", "guild", "actor", bytes.NewBufferString(valid+trailing+"\n"), false)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("trailing %q: expected invalid input, got %v", trailing, err)
		}
		if report == nil || report.Valid != 0 || len(report.Failures) != 1 || report.Failures[0].Code != "malformed_json" {
			t.Fatalf("trailing %q: unexpected report %+v", trailing, report)
		}
	}
}

func TestImportFailedDryRunHasNoRepositorySideEffects(t *testing.T) {
	for name, input := range map[string]string{"malformed": "{bad json}\n", "empty": "\n"} {
		t.Run(name, func(t *testing.T) {
			repository := &fakeRepository{}
			if _, err := New(repository).Import(context.Background(), "export", "guild", "actor", bytes.NewBufferString(input), true); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected invalid input, got %v", err)
			}
			if repository.failureCalls != 0 || repository.failureCode != "" {
				t.Fatalf("dry-run recorded failure side effects: %+v", repository)
			}
		})
	}
}

func TestImportRejectsWholeMalformedSourceAndAuditsClassification(t *testing.T) {
	repository := &fakeRepository{}
	report, err := New(repository).Import(context.Background(), "export", "guild", "actor", bytes.NewBufferString("{bad json}\n"), false)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
	if report == nil || len(report.Failures) != 1 || report.Failures[0].Code != "malformed_json" {
		t.Fatalf("unexpected safe report: %+v", report)
	}
	if repository.failureCode != "validation_failed" {
		t.Fatalf("unexpected audit classification %q", repository.failureCode)
	}
}

func TestImportDryRunReturnsRepositoryPlanWithoutApplying(t *testing.T) {
	repository := &fakeRepository{preview: []Decision{{Line: 1, SourceID: "source", WouldCreate: true, Warnings: []string{"target_departed"}}}}
	input := `{"format":"quack-v4-case-jsonl/v1","source_id":"source","guild_id":"guild","target_discord_user_id":"member","reason":"history","action_type":"warning","created_at":"2024-01-02T03:04:05Z"}`
	report, err := New(repository).Import(context.Background(), "export", "guild", "actor", bytes.NewBufferString(input), true)
	if err != nil {
		t.Fatalf("dry-run import: %v", err)
	}
	if !report.DryRun || len(report.Decisions) != 1 || report.Decisions[0].Created {
		t.Fatalf("unexpected dry-run report: %+v", report)
	}
}

func TestValidateCommandScopes(t *testing.T) {
	if err := ValidateCommandScopes([]string{"warn"}, []string{"case"}, true); err == nil {
		t.Fatal("expected post-migration direct-command failure")
	}
	if err := ValidateCommandScopes([]string{"ticket"}, []string{"case"}, false); err != nil {
		t.Fatalf("unexpected isolated command scopes: %v", err)
	}
}

func FuzzLegacyImportRows(f *testing.F) {
	f.Add(`{"format":"quack-v4-case-jsonl/v1","source_id":"source","guild_id":"guild","target_discord_user_id":"member","reason":"history","action_type":"warning","created_at":"2024-01-02T03:04:05Z"}`)
	f.Add(`{bad json}`)
	f.Add("")
	f.Fuzz(func(t *testing.T, row string) {
		repository := &fakeRepository{}
		report, err := New(repository).Import(context.Background(), "fuzz", "guild", "actor", bytes.NewBufferString(row), true)
		if err == nil && (report == nil || report.Total < 1 || report.Valid < 1) {
			t.Fatalf("successful import returned an invalid report: %+v", report)
		}
		if report != nil {
			for _, issue := range append(report.Warnings, report.Failures...) {
				if issue.Code == "" || issue.Line < 1 {
					t.Fatalf("unsafe issue classification: %+v", issue)
				}
			}
		}
	})
}

// TestImportDuplicateSourceIDsFailPreview proves both identical and changed
// duplicate identities fail validation before preview or application can succeed.
func TestImportDuplicateSourceIDsFailPreview(t *testing.T) {
	row := `{"format":"quack-v4-case-jsonl/v1","source_id":"source","guild_id":"guild","target_discord_user_id":"member","reason":"history","action_type":"unban","created_at":"2024-01-02T03:04:05Z"}`
	for _, dryRun := range []bool{true, false} {
		for _, next := range []string{row, strings.Replace(row, "history", "changed", 1)} {
			report, err := New(&fakeRepository{}).Import(context.Background(), "export", "guild", "actor", strings.NewReader(row+"\n"+next), dryRun)
			if !errors.Is(err, ErrInvalidInput) || len(report.Failures) != 1 || report.Failures[0].Code != "duplicate_source_id" || report.Failures[0].Line != 2 {
				t.Fatalf("duplicate identity accepted: report=%+v err=%v", report, err)
			}
		}
	}
}
