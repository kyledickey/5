package quack

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// ErrAppealDeliveryDeferred means no message was delivered and a later retry is safe.
var ErrAppealDeliveryDeferred = errors.New("appeal delivery deferred")

// AppealQueueReceipt identifies the existing staff queue message for in-place refresh.
type AppealQueueReceipt struct{ ChannelID, MessageID string }

// AppealNotificationClient delivers validated member intent or a legacy body,
// and projects staff queue updates without changing durable delivery ownership.
type AppealNotificationClient interface {
	SendAppealMemberNotification(context.Context, string, AppealMemberNotification) (string, error)
	SendAppealStaffNotification(context.Context, string, *AppealResponse, AppealQueueReceipt) (AppealQueueReceipt, error)
}

// AppealNotificationDispatcher drains durable appeal outbox items through a Discord adapter.
type AppealNotificationDispatcher struct {
	store  AppealRepository
	client AppealNotificationClient
}

// NewAppealNotificationDispatcher returns a dispatcher over store and client.
// Both are required; it panics on nil so a misconfigured process fails at
// startup rather than on the first outbox poll.
func NewAppealNotificationDispatcher(store AppealRepository, client AppealNotificationClient) *AppealNotificationDispatcher {
	if store == nil {
		panic("quack: NewAppealNotificationDispatcher requires a repository")
	}
	if client == nil {
		panic("quack: NewAppealNotificationDispatcher requires a notification client")
	}
	return &AppealNotificationDispatcher{store: store, client: client}
}

// DispatchPending delivers a bounded batch and records every success or classified failure idempotently.
func (d *AppealNotificationDispatcher) DispatchPending(ctx context.Context, limit int) error {
	if limit < 1 || limit > 100 {
		return errors.New("appeal notification limit is invalid")
	}
	items, err := d.store.ClaimPendingAppealNotifications(ctx, limit)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := d.store.BeginAppealNotificationDelivery(ctx, item.ID, item.LeaseToken); err != nil {
			return err
		}
		var messageID string
		receipt := AppealQueueReceipt{ChannelID: item.DeliveryChannelID, MessageID: item.DeliveryMessageID}
		var sendErr error
		switch item.Audience {
		case model.AppealNotificationMember:
			notice, decodeErr := appealMemberNotification(item)
			sendErr = decodeErr
			if sendErr == nil {
				messageID, sendErr = d.client.SendAppealMemberNotification(ctx, item.TargetDiscordUserID, notice)
			}
		case model.AppealNotificationStaff:
			var record *model.Appeal
			record, sendErr = d.store.GetAppealByID(ctx, item.AppealID)
			if sendErr != nil {
				sendErr = fmt.Errorf("%w: %v", ErrAppealDeliveryDeferred, sendErr)
			}
			if sendErr == nil && (record == nil || record.GuildID != item.GuildID) {
				sendErr = ErrAppealNotFound
			}
			if sendErr == nil {
				var appeal *AppealResponse
				appeal, sendErr = NewAppealService(d.store).response(ctx, record, false)
				if sendErr != nil {
					sendErr = fmt.Errorf("%w: %v", ErrAppealDeliveryDeferred, sendErr)
				}
				if sendErr == nil {
					receipt, sendErr = d.client.SendAppealStaffNotification(ctx, item.GuildID, appeal, receipt)
					messageID = receipt.MessageID
				}
			}
		default:
			sendErr = errors.New("appeal notification audience is invalid")
		}
		params := model.CompleteAppealNotificationParams{
			NotificationID:    item.ID,
			LeaseToken:        item.LeaseToken,
			DeliveryMessageID: messageID,
			DeliveryChannelID: receipt.ChannelID,
			Status:            model.AppealNotificationSent,
		}
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

// appealNotificationErrorCode classifies a delivery error into the stable code
// stored on the outbox row. Unknown Discord errors are bucketed by message text.
func appealNotificationErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrAppealNotificationIntent) {
		return "invalid_notification_intent"
	}
	if errors.Is(err, ErrAppealDeliveryDeferred) {
		return "delivery_deferred"
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
