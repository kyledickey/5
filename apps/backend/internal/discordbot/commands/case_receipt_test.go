package commands

import (
	"context"
	"errors"
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
	if receipt.RuleName != "Spam" || receipt.MemberReason != "Spam" || receipt.Case.ContextValues != nil || receipt.Case.ModeratorDiscordUserID != "" {
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
		assertRecoveryPrivate(t, result)
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
