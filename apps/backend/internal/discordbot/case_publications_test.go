package discordbot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// publicationRepositoryStub retains transport state between independent worker
// passes and injects read failures without depending on a live Discord session.
type publicationRepositoryStub struct {
	receipt    model.CasePublication
	item       *model.Case
	actions    []model.CaseActionExecution
	failRead   bool
	incomplete bool
	deleted    bool
}

// ListDueCasePublications returns only receipts whose retry deadline has elapsed.
func (r *publicationRepositoryStub) ListDueCasePublications(_ context.Context, now time.Time, _ int) ([]model.CasePublication, error) {
	if r.deleted || !r.receipt.RefreshRequested || now.Before(r.receipt.RetryAt) {
		return nil, nil
	}
	return []model.CasePublication{r.receipt}, nil
}

// CompleteCasePublicationRefresh saves the acknowledged digest and retry deadline.
func (r *publicationRepositoryStub) CompleteCasePublicationRefresh(_ context.Context, _ string, revision uint64, digest string, retryAt time.Time, requested bool) error {
	if revision == r.receipt.Revision {
		r.receipt.LastDigest, r.receipt.RetryAt, r.receipt.RefreshRequested = digest, retryAt, requested
	} else {
		r.receipt.Revision++
		r.receipt.LastDigest = ""
		r.receipt.RefreshRequested = true
		r.receipt.RetryAt = time.Time{}
	}
	return nil
}

// DeleteCasePublication records explicit retirement.
func (r *publicationRepositoryStub) DeleteCasePublication(context.Context, string) error {
	r.deleted = true
	return nil
}

// GetCaseByID returns a system-owned case record containing deliberately private fields.
func (r *publicationRepositoryStub) GetCaseByID(context.Context, string) (*model.Case, error) {
	if r.failRead {
		return nil, errors.New("database unavailable")
	}
	return r.item, nil
}

// ListCaseActionExecutions supplies current persisted enforcement state.
func (r *publicationRepositoryStub) ListCaseActionExecutions(context.Context, string) ([]model.CaseActionExecution, error) {
	return r.actions, nil
}

// publicationFixture constructs a public snapshot independent of mutable rules.
func publicationFixture(t *testing.T) *publicationRepositoryStub {
	t.Helper()
	raw, err := json.Marshal(views.CaseCreated{Case: &quack.CaseResponse{ID: "case", CaseNumber: 42, TargetDiscordUserID: "member"}, Template: &quack.TemplateResponse{Name: "Original rule"}})
	if err != nil {
		t.Fatal(err)
	}
	return &publicationRepositoryStub{receipt: model.CasePublication{RefreshRequested: true, CaseID: "case", MessageID: "message", ChannelID: "channel", PresentationJSON: string(raw)}, item: &model.Case{ULIDModel: model.ULIDModel{ID: "case"}, Reason: "SECRET staff reason", ContextValuesJSON: "SECRET evidence", ModeratorDiscordUserID: "SECRET moderator"}, actions: []model.CaseActionExecution{{ULIDModel: model.ULIDModel{ID: "action"}, ActionType: model.ActionBanUser, Status: model.ActionExecutionSucceeded}}}
}

// TestCasePublicationReconcilesTerminalAndVoid verifies later independent passes
// refresh completed cases, including reversal actions, while preserving privacy.
func TestCasePublicationReconcilesTerminalAndVoid(t *testing.T) {
	repository := publicationFixture(t)
	now := time.Now().UTC()
	edits := 0
	edit := func(_ context.Context, _ model.CasePublication, message ui.Message) error {
		edits++
		if strings.Contains(message.Content, "SECRET") || !strings.Contains(message.Content, "Original rule") {
			t.Fatalf("unsafe or changed presentation: %s", message.Content)
		}
		if edits == 2 && !strings.Contains(message.Content, "voided") {
			t.Fatal("void outcome missing")
		}
		return nil
	}
	if err := refreshCasePublications(context.Background(), repository, edit, now); err != nil {
		t.Fatal(err)
	}
	if repository.receipt.LastDigest == "" || repository.receipt.RefreshRequested {
		t.Fatal("terminal refresh did not persist progress and sleep")
	}
	if err := refreshCasePublications(context.Background(), repository, edit, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if edits != 1 {
		t.Fatal("unchanged receipt edited again")
	}
	repository.receipt.RefreshRequested = true
	repository.receipt.Revision++
	repository.item.Validity = model.CaseValidityVoided
	repository.actions = append(repository.actions, model.CaseActionExecution{ActionType: model.ActionUnbanUser, Status: model.ActionExecutionSucceeded})
	if err := refreshCasePublications(context.Background(), repository, edit, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if edits != 2 || repository.deleted {
		t.Fatal("completed receipt failed to recover later reversal")
	}
}

// TestCasePublicationRetriesOutagesAndRetiresUnknownMessage distinguishes a
// missing message from temporary access loss; only the former is unrecoverable.
func TestCasePublicationRetriesOutagesAndRetiresUnknownMessage(t *testing.T) {
	for _, scenario := range []string{"read", "permission", "transient", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			repository := publicationFixture(t)
			repository.failRead = scenario == "read"
			now := time.Now().UTC()
			edit := func(context.Context, model.CasePublication, ui.Message) error {
				switch scenario {
				case "unknown":
					return &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage}}
				case "permission":
					return &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeMissingPermissions}}
				default:
					return errors.New("network outage")
				}
			}
			err := refreshCasePublications(context.Background(), repository, edit, now)
			if scenario == "unknown" {
				if err != nil || !repository.deleted {
					t.Fatal("unknown message not retired")
				}
				return
			}
			if err == nil || repository.deleted || repository.receipt.LastDigest != "" {
				t.Fatal("recoverable outage lost receipt")
			}
			repository.failRead = false
			if err := refreshCasePublications(context.Background(), repository, func(context.Context, model.CasePublication, ui.Message) error { return nil }, now.Add(5*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if repository.receipt.LastDigest == "" {
				t.Fatal("receipt did not recover")
			}
		})
	}
}

// CasePublicationEvidenceIncomplete exposes only the current public health flag.
func (r *publicationRepositoryStub) CasePublicationEvidenceIncomplete(context.Context, string) (bool, error) {
	return r.incomplete, nil
}

// TestCasePublicationRefreshesEvidenceHealth keeps preservation diagnostics
// private before and after repair, including older receipt snapshots.
func TestCasePublicationRefreshesEvidenceHealth(t *testing.T) {
	repository := publicationFixture(t)
	repository.incomplete = true
	now := time.Now().UTC()
	var content string
	edit := func(_ context.Context, _ model.CasePublication, message ui.Message) error {
		content = message.Content
		return nil
	}
	if err := refreshCasePublications(context.Background(), repository, edit, now); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "Some evidence could not be saved") {
		t.Fatal("private evidence warning leaked")
	}
	repository.receipt.RefreshRequested = true
	repository.receipt.Revision++
	repository.incomplete = false
	if err := refreshCasePublications(context.Background(), repository, edit, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "Some evidence could not be saved") {
		t.Fatal("recovered evidence warning persisted")
	}
}

// TestCasePublicationRepairsLateStaleEdit models two refreshes whose Discord
// edits finish out of order: the older completion must wake the current revision.
func TestCasePublicationRepairsLateStaleEdit(t *testing.T) {
	repository := publicationFixture(t)
	now := time.Now().UTC()
	edits := 0
	visible := ""
	var edit func(context.Context, model.CasePublication, ui.Message) error
	edit = func(ctx context.Context, _ model.CasePublication, message ui.Message) error {
		edits++
		if edits == 1 {
			repository.item.Validity = model.CaseValidityVoided
			repository.receipt.Revision++
			repository.receipt.RefreshRequested = true
			repository.receipt.LastDigest = ""
			if err := refreshCasePublications(ctx, repository, edit, now); err != nil {
				return err
			}
		}
		visible = message.Content
		return nil
	}
	if err := refreshCasePublications(context.Background(), repository, edit, now); err != nil {
		t.Fatal(err)
	}
	if !repository.receipt.RefreshRequested || strings.Contains(visible, "voided") {
		t.Fatal("did not model late stale delivery")
	}
	if err := refreshCasePublications(context.Background(), repository, edit, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if edits != 3 || !strings.Contains(visible, "voided") || repository.receipt.RefreshRequested {
		t.Fatal("stale delivery was not repaired then retired from polling")
	}
}
