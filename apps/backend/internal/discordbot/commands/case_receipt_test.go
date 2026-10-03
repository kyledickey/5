package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// TestPrivateReceiptWaitsForNotification checks that finished enforcement does
// not stop refresh before the independent DM reaches its final delivery state.
func TestPrivateReceiptWaitsForNotification(t *testing.T) {
	for _, status := range []model.NotificationStatus{model.NotificationSent, model.NotificationFailed} {
		t.Run(string(status), func(t *testing.T) {
			calls := 0
			read := func(context.Context, string) (*quack.CaseReceiptResponse, error) {
				calls++
				if calls == 1 {
					return nil, errors.New("transient read failure")
				}
				notification := model.NotificationPending
				if calls >= 3 {
					notification = status
				}
				return &quack.CaseReceiptResponse{Case: &quack.CaseResponse{ID: "case", CaseNumber: 1}, Notification: &quack.CaseNotificationResponse{Status: notification}}, nil
			}
			responder := &failingCasePublication{failEditCount: 1}
			refreshPrivateCaseReceipt(context.Background(), responder, read, "case", time.Millisecond, time.Second)
			if calls != 3 || responder.edit.Content == nil {
				t.Fatalf("refresh stopped incorrectly calls=%d receipt=%+v", calls, responder.edit)
			}
			if strings.Contains(*responder.edit.Content, "pending") {
				t.Fatal("final DM outcome missing")
			}
		})
	}
}

// TestPrivateReceiptRefreshBoundAndCancellation prevents orphan interaction work.
func TestPrivateReceiptRefreshBoundAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	read := func(context.Context, string) (*quack.CaseReceiptResponse, error) {
		calls++
		return nil, errors.New("offline")
	}
	refreshPrivateCaseReceipt(ctx, &fakeResponder{}, read, "case", time.Millisecond, time.Second)
	if calls != 0 {
		t.Fatal("cancelled work read storage")
	}
	start := time.Now()
	refreshPrivateCaseReceipt(context.Background(), &fakeResponder{}, read, "case", time.Millisecond, 10*time.Millisecond)
	if calls == 0 || time.Since(start) > time.Second {
		t.Fatal("retry bound ignored")
	}
}

// TestReceiptProjectionUsesImmutablePolicyAndMinimalQueries verifies the receipt
// excludes private material and never loads event, attempt or evidence histories.
func TestReceiptProjectionUsesImmutablePolicyAndMinimalQueries(t *testing.T) {
	repository, services, template := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	created, err := services.Cases.Create(context.Background(), guild, quack.CaseInput{TemplateID: template, TargetDiscordUserID: "target", Source: model.CaseSourceDiscord, IdempotencyKey: "receipt-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Model(&model.CaseTemplate{}).Where("id = ?", template).Updates(map[string]any{"name": "CHANGED", "reason_template": "CHANGED"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Callback().Query().Before("gorm:query").Register("receipt_bounded", func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "case_events", "case_action_attempts", "case_evidence_attachments":
			tx.AddError(errors.New("unrelated history read"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := model.CaseEvidenceSnapshot{ULIDModel: model.ULIDModel{ID: "receipt-evidence"}, CaseID: created.ID, GuildID: guild.Guild.ID, Content: "PRIVATE EVIDENCE", CaptureWarning: "copy failed"}
	if err := repository.DB().Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Callback().Query().After("gorm:query").Register("receipt_evidence_scalar", func(tx *gorm.DB) {
		if tx.Statement.Table == "case_evidence_snapshots" && !strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "count(") {
			t.Error("evidence bodies loaded")
		}
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := services.Cases.ReceiptForPublication(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Case.EvidenceIncomplete {
		t.Fatal("existing evidence warning omitted")
	}
	if receipt.RuleName != "Spam" || receipt.MemberReason != "Spam" || receipt.Case.ModeratorDiscordUserID != created.ModeratorDiscordUserID {
		t.Fatalf("unsafe or mutable projection %+v", receipt)
	}
	if err := repository.DB().Model(&model.CaseEvidenceSnapshot{}).Where("id = ?", snapshot.ID).Update("capture_warning", "").Error; err != nil {
		t.Fatal(err)
	}
	repaired, err := services.Cases.ReceiptForPublication(context.Background(), created.ID)
	if err != nil || repaired.Case.EvidenceIncomplete {
		t.Fatal("repaired evidence stayed incomplete", err)
	}

	private := views.CaseModeratorReceipt(receipt)
	for _, want := range []string{"Default", "DM", "appeal"} {
		if !strings.Contains(strings.ToLower(private.Content), strings.ToLower(want)) {
			t.Fatalf("missing %s: %s", want, private.Content)
		}
	}
	public := views.CaseCreatedMessage(views.CaseCreated{Case: receipt.Case, Template: &quack.TemplateResponse{Name: receipt.RuleName}, MemberReason: receipt.MemberReason})
	for _, hidden := range []string{"Default", "DM", "appeal"} {
		if strings.Contains(public.Content, hidden) {
			t.Fatalf("public field leaked: %s", hidden)
		}
	}
}

// TestReceiptViewCaseRechecksAuthority verifies an old receipt grants no access
// after moderator permission is revoked and opens a private detail when allowed.
func TestReceiptViewCaseRechecksAuthority(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		bits := uint64(0)
		if allowed {
			bits = uint64(discordgo.PermissionModerateMembers)
		}
		repository, services, _ := newCaseCommandHarnessWithLivePermissions(t, bits)
		guild := caseCommandGuildContext(t, services)
		item := model.Case{ULIDModel: model.ULIDModel{ID: "receipt-case"}, GuildID: guild.Guild.ID, CaseNumber: 1, Validity: model.CaseValidityValid, TemplateSnapshotJSON: "{}"}
		if err := repository.DB().Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
		interaction.Type = discordgo.InteractionMessageComponent
		interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "view", Version: "v1", Payload: item.ID})}
		result := handleCaseViewComponent(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
		assertRecoveryPublic(t, result)
		responder := &fakeResponder{}
		err := result.Task(context.Background(), responder)
		if allowed && (err != nil || responder.edit.Content == nil) {
			t.Fatal("authorized receipt inaccessible", err)
		}
		if !allowed && (err == nil || responder.edit.Content != nil) {
			t.Fatal("revoked authority retained access")
		}
	}
}

// TestUnavailableReceiptKeepsBoundedRefresh distinguishes failed progress reads
// from explicitly disabled notifications, even for a warning with no actions.
func TestUnavailableReceiptKeepsBoundedRefresh(t *testing.T) {
	receipt := initialModeratorReceipt(&quack.CaseResponse{ID: "case"}, nil)
	if !receipt.Pending() {
		t.Fatal("unavailable progress stopped recovery")
	}
	receipt.Notification = nil
	if receipt.Pending() {
		t.Fatal("disabled warning notification kept polling")
	}
}

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
			if err := publishCaseResult(context.Background(), responder, nil, created, nil, false); err != nil {
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

// TestCaseProfileSummarySurvivesEveryNativeEntryPoint checks command, View user
// button, and page navigation against stored history larger than one page.
func TestCaseProfileSummarySurvivesEveryNativeEntryPoint(t *testing.T) {
	ctx := context.Background()
	repository, services, _ := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	services.Config.ApplicationBaseURL = "https://dashboard.example/base"
	for i := 1; i <= 11; i++ {
		item := model.Case{ULIDModel: model.ULIDModel{ID: fmt.Sprintf("profile-case-%d", i)}, GuildID: guild.Guild.ID, CaseNumber: uint64(i), TargetDiscordUserID: "target-1", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, TemplateSnapshotJSON: "{}", MetadataJSON: "{}", ContextValuesJSON: "[]"}
		if i == 1 {
			item.Source = model.CaseSourceV4Import
		}
		if i == 2 {
			item.Validity = model.CaseValidityVoided
		}
		if err := repository.DB().Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []string{"command", "button", "page"} {
		t.Run(entry, func(t *testing.T) {
			interaction := caseAddInteraction("", "target-1", uint64(discordgo.PermissionModerateMembers))
			handler := HandleCaseInteraction
			switch entry {
			case "command":
				interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "case", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "user", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "user", Type: discordgo.ApplicationCommandOptionUser, Value: "target-1"}}}}}
			case "button":
				interaction.Type = discordgo.InteractionMessageComponent
				interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "user_detail", Version: "v1", Payload: "target-1"})}
				handler = handleCaseUserComponent
			case "page":
				interaction.Type = discordgo.InteractionMessageComponent
				interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "user_next", Version: "v1", Payload: "1|target-1"})}
				handler = pageCases(1, true)
			}
			result := handler(ui.Context{Context: ctx, Services: services, Interaction: interaction})
			responder := &fakeResponder{}
			if result.Task == nil {
				t.Fatal("missing history task")
			}
			if err := result.Task(ctx, responder); err != nil {
				t.Fatal(err)
			}
			components := responder.edit.Components
			if entry == "page" {
				components = responder.updated.Components
			}
			if components == nil {
				t.Fatal("missing web navigation")
			}
			found := false
			for _, component := range *components {
				for _, control := range component.(discordgo.ActionsRow).Components {
					if button, ok := control.(discordgo.Button); ok && button.Style == discordgo.LinkButton {
						found = button.URL == "https://dashboard.example/base/guilds/"+guild.Guild.DiscordGuildID+"/members/target-1"
					}
				}
			}
			if !found {
				t.Fatalf("profile web destination absent on %s", entry)
			}
			content := responder.edit.Content
			if entry == "page" {
				content = responder.updated.Content
			}
			if content == nil || !strings.Contains(*content, "11 total · 10 active · 1 voided") {
				t.Fatalf("summary lost on %s: %v", entry, content)
			}
			if entry == "page" && !strings.Contains(*content, "Page 2/2") {
				t.Fatal("user pagination lost")
			}
		})
	}
}
