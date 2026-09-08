package commands

import (
	"context"
	"errors"
	"github.com/quackdiscord/bot/internal/quack/model"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// failingCasePublication simulates delivery and cleanup failures after moderation
// has already committed, without obscuring the persisted private receipt.
type failingCasePublication struct {
	fakeResponder
	failEditCount int
	failPublish   bool
	failCleanup   bool
}

// Followup fails only public publication, preserving the earlier private edit.
func (r *failingCasePublication) Followup(message ui.Message) (*discordgo.Message, error) {
	if r.failPublish {
		return nil, errors.New("Discord unavailable")
	}
	return r.fakeResponder.Followup(message)
}

// DeleteOriginal simulates failure to remove the now-redundant private copy.
func (r *failingCasePublication) DeleteOriginal() error {
	if r.failCleanup {
		return errors.New("Discord unavailable")
	}
	return r.fakeResponder.DeleteOriginal()
}

// TestCasePublicationFailureKeepsSuccessfulReceipt ensures the dispatcher cannot
// replace an already-created case with a generic command-failed response.
func TestCasePublicationFailureKeepsSuccessfulReceipt(t *testing.T) {
	for _, failure := range []string{"publish", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			responder := &failingCasePublication{failPublish: failure == "publish", failCleanup: failure == "cleanup"}
			created := &quack.CaseResponse{ID: "case", CaseNumber: 12, TargetDiscordUserID: "member", Reason: "Rule violation"}
			if err := publishPrivateContextCase(context.Background(), responder, nil, created, nil); err != nil {
				t.Fatalf("committed case reported as failed: %v", err)
			}
			if responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "12") {
				t.Fatalf("case receipt lost: %+v", responder.edit.Content)
			}
			if failure == "publish" && (!strings.Contains(*responder.edit.Content, "case was created") || responder.deleted) {
				t.Fatal("publication failure discarded successful private result")
			}
			if failure == "cleanup" && responder.followup.Content == "" {
				t.Fatal("public receipt missing")
			}
		})
	}
}

// EditOriginal injects acknowledgement failures without recording a successful edit.
func (r *failingCasePublication) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	if r.failEditCount > 0 {
		r.failEditCount--
		return nil, errors.New("Discord unavailable")
	}
	return r.fakeResponder.EditOriginal(edit)
}

// TestCaseReceiptInitialEditRecovery checks both transient acknowledgement recovery
// and a private fallback that never reports the committed moderation as failed.
func TestCaseReceiptInitialEditRecovery(t *testing.T) {
	for _, failures := range []int{1, 3} {
		responder := &failingCasePublication{failEditCount: failures}
		created := &quack.CaseResponse{ID: "case", CaseNumber: 12, TargetDiscordUserID: "member"}
		if err := publishPrivateContextCase(context.Background(), responder, nil, created, nil); err != nil {
			t.Fatal(err)
		}
		if failures == 1 && (responder.followup.Ephemeral || !responder.deleted) {
			t.Fatal("recovered edit did not produce public receipt")
		}
		if failures == 3 && (!responder.followup.Ephemeral || responder.deleted || !strings.Contains(responder.followup.Content, "Do not create it again")) {
			t.Fatal("failed acknowledgement lost private committed-case fallback")
		}
	}
}

// retryingCaseRefresh records delivery attempts to verify failed public edits are retried.
type retryingCaseRefresh struct {
	fakeResponder
	attempts int
}

// EditFollowup simulates a transient Discord outage on the first status refresh.
func (r *retryingCaseRefresh) EditFollowup(id string, edit ui.Edit) (*discordgo.Message, error) {
	r.attempts++
	if r.attempts == 1 {
		return nil, errors.New("unavailable")
	}
	return r.fakeResponder.EditFollowup(id, edit)
}

// TestPublicCaseRefreshRetriesReadAndEdit ensures transient failures cannot leave
// a pending public receipt when enforcement has already completed.
func TestPublicCaseRefreshRetriesReadAndEdit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	responder := &retryingCaseRefresh{}
	reads := 0
	list := func(context.Context, string) ([]model.CaseActionExecution, error) {
		reads++
		if reads == 1 {
			return nil, errors.New("database unavailable")
		}
		return []model.CaseActionExecution{{ULIDModel: model.ULIDModel{ID: "action"}, Status: model.ActionExecutionSucceeded}}, nil
	}
	snapshot := &quack.CaseResponse{ID: "case", Actions: []quack.CaseActionResponse{{ID: "action", Status: model.ActionExecutionPending}}}
	refreshPublicCaseResult(ctx, responder, list, snapshot, "message", nil, time.Millisecond)
	if responder.attempts != 2 || reads != 3 || snapshot.Actions[0].Status != model.ActionExecutionSucceeded {
		t.Fatalf("refresh did not recover: edits=%d reads=%d snapshot=%+v", responder.attempts, reads, snapshot)
	}
}

// publicationCaptureRepository records the durable write without performing
// unrelated moderation repository operations.
type publicationCaptureRepository struct {
	quack.Repository
	receipt model.CasePublication
}

// SaveCasePublication captures the persisted public presentation.
func (r *publicationCaptureRepository) SaveCasePublication(_ context.Context, receipt model.CasePublication) error {
	r.receipt = receipt
	return nil
}

// TestCasePublicationPersistsOnlyPublicSnapshot checks the durable boundary does
// not store staff identity, reasons, context, policy configuration or evidence.
func TestCasePublicationPersistsOnlyPublicSnapshot(t *testing.T) {
	repository := &publicationCaptureRepository{}
	created := &quack.CaseResponse{ID: "case", CaseNumber: 42, TargetDiscordUserID: "member", Reason: "SECRET reason", ModeratorDiscordUserID: "SECRET moderator", ContextURL: "SECRET evidence", Metadata: "SECRET metadata", SelectedLevel: &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: "Public level", TriggerCaseCount: 12345}, MatchedCaseCount: 54321}}
	template := &quack.TemplateResponse{Name: "Public rule", Slug: "rule", Description: "SECRET description"}
	if err := updatePublicCaseResult(context.Background(), &fakeResponder{}, &quack.Services{Store: repository}, created, "message", "channel", template); err != nil {
		t.Fatal(err)
	}
	if repository.receipt.ChannelID != "channel" || repository.receipt.CaseID != "case" || strings.Contains(repository.receipt.PresentationJSON, "SECRET") || strings.Contains(repository.receipt.PresentationJSON, "12345") || strings.Contains(repository.receipt.PresentationJSON, "54321") || !strings.Contains(repository.receipt.PresentationJSON, "Public rule") {
		t.Fatalf("invalid public snapshot: %+v", repository.receipt)
	}
}
