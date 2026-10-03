package store

import (
	"context"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// intentDeliveryClient retains only the typed member notices observed by delivery.
type intentDeliveryClient struct {
	appealNotificationClientStub
	notices   []quack.AppealMemberNotification
	deferOnce bool
}

// SendAppealMemberNotification simulates a safe preparation failure and later retry.
func (c *intentDeliveryClient) SendAppealMemberNotification(_ context.Context, _ string, notice quack.AppealMemberNotification) (string, error) {
	c.notices = append(c.notices, notice)
	if c.deferOnce {
		c.deferOnce = false
		return "", quack.ErrAppealDeliveryDeferred
	}
	return "member-receipt", nil
}

// TestAppealIntentSurvivesRestartSettingsChangeAndRetry verifies durable decision
// facts remain unchanged after live settings/appeal data change and safe retry.
func TestAppealIntentSurvivesRestartSettingsChangeAndRetry(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	if err := repository.db.Create(&model.GuildSettings{ULIDModel: model.ULIDModel{ID: "intent-settings"}, GuildID: guild.ID, AppealRejoinURL: "https://discord.gg/original"}).Error; err != nil {
		t.Fatal(err)
	}
	service := quack.NewAppealService(repository)
	appeal, err := service.Submit(ctx, item.ID, "target", quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}})
	if err != nil {
		t.Fatal(err)
	}
	actor := &quack.GuildStaffContext{Guild: guild, Staff: &model.StaffMember{GuildID: guild.ID, DiscordUserID: "staff"}, Permissions: map[model.PermissionAction]bool{model.PermissionActionAppealReview: true}}
	if _, err := service.Accept(ctx, actor, appeal.ID, "original decision"); err != nil {
		t.Fatal(err)
	}
	if err := repository.db.Model(&model.GuildSettings{}).Where("guild_id = ?", guild.ID).Update("appeal_rejoin_url", "https://discord.gg/changed").Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.db.Model(&model.Appeal{}).Where("id = ?", appeal.ID).Update("decision_reason", "changed later").Error; err != nil {
		t.Fatal(err)
	}
	restarted := New(repository.db, nil)
	client := &intentDeliveryClient{deferOnce: true}
	dispatcher := quack.NewAppealNotificationDispatcher(restarted, client)
	if err := dispatcher.DispatchPending(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var row model.AppealNotification
	if err := repository.db.Where("appeal_id = ? AND audience = ?", appeal.ID, model.AppealNotificationMember).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.LastErrorCode != "delivery_deferred" {
		t.Fatal("retry safety changed", row)
	}
	if err := repository.db.Model(&row).Update("updated_at", time.Now().Add(-2*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := quack.NewAppealNotificationDispatcher(restarted, client).DispatchPending(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if len(client.notices) != 2 {
		t.Fatal(client.notices)
	}
	for _, notice := range client.notices {
		if notice.Intent == nil || notice.Intent.Reason != "original decision" || notice.Intent.RejoinURL != "https://discord.gg/original" || notice.LegacyBody != "" {
			t.Fatal("snapshot changed", notice)
		}
	}
	if err := repository.db.First(&row, "id = ?", row.ID).Error; err != nil || row.Status != model.AppealNotificationSent || row.DeliveryMessageID != "member-receipt" {
		t.Fatal("receipt changed", row, err)
	}
}

// TestAppealInvalidIntentNeverDeliversLegacyFallback proves corrupt or newer
// payloads become classified failures while absent payloads deliver saved copy.
func TestAppealInvalidIntentNeverDeliversLegacyFallback(t *testing.T) {
	repository, guild := newAppealTestStore(t)
	for _, row := range []model.AppealNotification{
		{ULIDModel: model.ULIDModel{ID: "invalid-notice"}, AppealID: "appeal", EventID: "invalid-event", GuildID: guild.ID, TargetDiscordUserID: "member", Audience: model.AppealNotificationMember, Status: model.AppealNotificationPending, Body: "must not fall back", DecisionIntentJSON: `{"version":99,"status":"accepted","reason":"unsupported"}`},
		{ULIDModel: model.ULIDModel{ID: "legacy-notice"}, AppealID: "appeal", EventID: "legacy-event", GuildID: guild.ID, TargetDiscordUserID: "member", Audience: model.AppealNotificationMember, Status: model.AppealNotificationPending, Body: "exact legacy body"},
	} {
		if err := repository.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	client := &intentDeliveryClient{}
	dispatcher := quack.NewAppealNotificationDispatcher(repository, client)
	for range 2 {
		if err := dispatcher.DispatchPending(context.Background(), 100); err != nil {
			t.Fatal(err)
		}
	}
	if len(client.notices) != 1 || client.notices[0].LegacyBody != "exact legacy body" || client.notices[0].Intent != nil {
		t.Fatal("invalid intent sent or legacy altered", client.notices)
	}
	var invalid model.AppealNotification
	if err := repository.db.First(&invalid, "id = ?", "invalid-notice").Error; err != nil || invalid.Status != model.AppealNotificationFailed || invalid.LastErrorCode != "invalid_notification_intent" || invalid.Body != "must not fall back" {
		t.Fatal("invalid row safety changed", invalid, err)
	}
}
