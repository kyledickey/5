package quack

import (
	"context"
	"errors"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// AppealNotificationClient sends already-rendered, staff-identity-free appeal messages.
type AppealNotificationClient interface {
	SendAppealMemberNotification(context.Context, string, string) (string, error)
	SendAppealStaffNotification(context.Context, string, *AppealResponse) (string, error)
}

// AppealNotificationDispatcher drains durable appeal outbox items through a Discord adapter.
type AppealNotificationDispatcher struct {
	store  AppealRepository
	client AppealNotificationClient
}

// NewAppealNotificationDispatcher constructs an integration-ready appeal notification worker.
func NewAppealNotificationDispatcher(store AppealRepository, client AppealNotificationClient) *AppealNotificationDispatcher {
	return &AppealNotificationDispatcher{store: store, client: client}
}

// DispatchPending delivers a bounded batch and records every success or classified failure idempotently.
func (d *AppealNotificationDispatcher) DispatchPending(ctx context.Context, limit int) error {
	if d == nil || d.store == nil || d.client == nil {
		return errors.New("appeal notification dispatcher is not configured")
	}
	if limit < 1 || limit > 100 {
		return errors.New("appeal notification limit is invalid")
	}
	items, err := d.store.ClaimPendingAppealNotifications(ctx, limit)
	if err != nil {
		return err
	}
	for _, item := range items {
		var messageID string
		var sendErr error
		switch item.Audience {
		case model.AppealNotificationMember:
			messageID, sendErr = d.client.SendAppealMemberNotification(ctx, item.TargetDiscordUserID, item.Body)
		case model.AppealNotificationStaff:
			var record *model.Appeal
			record, sendErr = d.store.GetAppealByID(ctx, item.AppealID)
			if sendErr == nil && (record == nil || record.GuildID != item.GuildID) {
				sendErr = ErrAppealNotFound
			}
			if sendErr == nil {
				var appeal *AppealResponse
				appeal, sendErr = NewAppealService(d.store).response(ctx, record, false)
				if sendErr == nil {
					messageID, sendErr = d.client.SendAppealStaffNotification(ctx, item.GuildID, appeal)
				}
			}
		default:
			sendErr = errors.New("appeal notification audience is invalid")
		}
		params := model.CompleteAppealNotificationParams{NotificationID: item.ID, LeaseToken: item.LeaseToken, DeliveryMessageID: messageID, Status: model.AppealNotificationSent}
		if sendErr != nil {
			params.Status = model.AppealNotificationFailed
			params.ErrorCode = appealNotificationErrorCode(sendErr)
		}
		if err := d.store.CompleteAppealNotification(ctx, params); err != nil {
			return err
		}
	}
	return nil
}

func appealNotificationErrorCode(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "permission"), strings.Contains(text, "forbidden"):
		return "discord_forbidden"
	case strings.Contains(text, "rate"):
		return "discord_rate_limited"
	case strings.Contains(text, "timeout"):
		return "discord_timeout"
	default:
		return "discord_delivery_failed"
	}
}
