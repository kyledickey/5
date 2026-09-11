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
// has already committed, without hiding its saved-case identity.
type failingCasePublication struct {
	fakeResponder
	editAttempts  int
	failEditCount int
	failPublish   bool
	failCleanup   bool
}

// DeleteOriginal simulates failure to remove the now-redundant private copy.
func (r *failingCasePublication) DeleteOriginal() error {
	if r.failCleanup {
		return errors.New("Discord unavailable")
	}
	return r.fakeResponder.DeleteOriginal()
}

// TestCasePublicationFailureKeepsSuccessfulReceipt preserves the committed case
// on a one-shot context publication failure without sending another public copy.
func TestCasePublicationFailureKeepsSuccessfulReceipt(t *testing.T) {
	for _, failure := range []string{"publish", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			responder := &failingCasePublication{failPublish: failure == "publish", failCleanup: failure == "cleanup"}
			created := &quack.CaseResponse{ID: "case", CaseNumber: 12, TargetDiscordUserID: "member", Reason: "PRIVATE reason", ContextURL: "PRIVATE evidence", ModeratorDiscordUserID: "PRIVATE moderator"}
			if err := publishPrivateContextCase(context.Background(), responder, nil, created, nil); err != nil {
				t.Fatalf("committed case reported failed: %v", err)
			}
			if responder.channelPublishes != 1 || (failure == "publish" && responder.editCount != 0) {
				t.Fatal("context result was duplicated", responder)
			}
			if failure == "publish" {
				if responder.webhookFollowups != 1 || !responder.followup.Ephemeral || !strings.Contains(responder.followup.Content, "Case #12 was saved") || len(responder.followup.Components) != 0 || responder.deleted {
					t.Fatal("missing private committed-case failure", responder)
				}
			} else if responder.editCount != 1 || responder.edit.Components == nil || len(*responder.edit.Components) != 0 {
				t.Fatal("failed cleanup left an active selector", responder)
			} else if responder.webhookFollowups != 0 || responder.followup.Ephemeral || !strings.Contains(responder.followup.Content, "Case #12") {
				t.Fatal("public result lost after cleanup failure", responder)
			}
			if failure == "cleanup" && !strings.Contains(responder.followup.Content, "PRIVATE reason") {
				t.Fatal("staff case reason missing", responder.followup.Content)
			}
		})
	}
}

// EditOriginal injects acknowledgement failures without recording a successful edit.
func (r *failingCasePublication) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	r.editAttempts++
	if r.failEditCount > 0 {
		r.failEditCount--
		return nil, errors.New("Discord unavailable")
	}
	return r.fakeResponder.EditOriginal(edit)
}

// TestCaseReceiptInitialEditRecovery retries only the original slash response;
// a terminal edit failure gets a private saved-case notice without another public send.
func TestCaseReceiptInitialEditRecovery(t *testing.T) {
	for _, failures := range []int{1, 3} {
		responder := &failingCasePublication{failEditCount: failures}
		created := &quack.CaseResponse{ID: "case", CaseNumber: 12, TargetDiscordUserID: "member"}
		if err := publishCaseResult(context.Background(), responder, nil, created, nil, true); err != nil {
			t.Fatal(err)
		}
		if responder.channelPublishes != 0 || responder.deleted {
			t.Fatal("slash result moved to another message", responder)
		}
		if failures == 1 && (responder.editAttempts != 2 || responder.editCount != 1 || responder.webhookFollowups != 0 || responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "Case #12")) {
			t.Fatal("original edit did not recover", responder)
		}
		if failures == 3 && (responder.editAttempts != 3 || responder.editCount != 0 || responder.webhookFollowups != 1 || !responder.followup.Ephemeral || !strings.Contains(responder.followup.Content, "Case #12 was saved") || len(responder.followup.Components) != 0) {
			t.Fatal("saved-case fallback missing", responder)
		}
	}
}

// retryingCaseRefresh records delivery attempts to verify failed public edits are retried.
type retryingCaseRefresh struct {
	fakeResponder
	attempts int
}

// EditChannel simulates a transient Discord outage on the first status refresh.
func (r *retryingCaseRefresh) EditChannel(ctx context.Context, id string, edit ui.Edit) (*discordgo.Message, error) {
	r.attempts++
	if r.attempts == 1 {
		return nil, errors.New("unavailable")
	}
	return r.fakeResponder.EditChannel(ctx, id, edit)
}

// TestPublicCaseRefreshRetriesReadAndEdit ensures transient failures cannot leave
// a pending public receipt when enforcement has already completed.
func TestPublicCaseRefreshRetriesReadAndEdit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	responder := &retryingCaseRefresh{}
	reads := 0
	list := func(context.Context, string) ([]quack.CaseActionResponse, error) {
		reads++
		if reads == 1 {
			return nil, errors.New("database unavailable")
		}
		return []quack.CaseActionResponse{{ID: "action", Status: model.ActionExecutionSucceeded}}, nil
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

// TestCasePublicationPersistsStaffSnapshot checks the durable boundary does
// retains staff display fields without unrelated policy configuration.
func TestCasePublicationPersistsStaffSnapshot(t *testing.T) {
	repository := &publicationCaptureRepository{}
	created := &quack.CaseResponse{ID: "case", CaseNumber: 42, TargetDiscordUserID: "member", Reason: "SECRET reason", ModeratorDiscordUserID: "SECRET moderator", ContextURL: "SECRET evidence", Metadata: "SECRET metadata", SelectedLevel: &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: "Public level", TriggerCaseCount: 12345}, MatchedCaseCount: 54321}}
	template := &quack.TemplateResponse{Name: "Public rule", Slug: "rule", Description: "SECRET description"}
	if err := updatePublicCaseResult(context.Background(), &fakeResponder{}, &quack.Services{Cases: quack.NewCaseService(repository, nil)}, created, "message", "channel", template); err != nil {
		t.Fatal(err)
	}
	if repository.receipt.ChannelID != "channel" || repository.receipt.CaseID != "case" || !strings.Contains(repository.receipt.PresentationJSON, "SECRET reason") || !strings.Contains(repository.receipt.PresentationJSON, "SECRET moderator") || strings.Contains(repository.receipt.PresentationJSON, "SECRET description") || strings.Contains(repository.receipt.PresentationJSON, "SECRET metadata") || strings.Contains(repository.receipt.PresentationJSON, "12345") || strings.Contains(repository.receipt.PresentationJSON, "54321") || !strings.Contains(repository.receipt.PresentationJSON, "Public rule") {
		t.Fatalf("invalid public snapshot: %+v", repository.receipt)
	}
}

// PublishChannel models a one-shot public send failure without retrying a POST.
func (r *failingCasePublication) PublishChannel(ctx context.Context, message ui.Message) (*discordgo.Message, error) {
	if r.failPublish {
		r.channelPublishes++
		return nil, errors.New("Discord unavailable")
	}
	return r.fakeResponder.PublishChannel(ctx, message)
}
