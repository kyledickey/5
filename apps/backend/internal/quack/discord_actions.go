package quack

import (
	"context"

	actionmods "github.com/quackdiscord/bot/internal/quack/actionmods"
)

// DiscordActionClient is the minimum Discord write capability ActionService
// requires. Richer adapters additionally implement the optional interfaces
// below, which the service discovers by type assertion.
type DiscordActionClient interface {
	SendDM(ctx context.Context, discordUserID, message string) (map[string]any, error)
}

// DiscordEnforcementClient is the optional adapter capability for real moderation and reversal operations.
type DiscordEnforcementClient interface {
	TimeoutMember(context.Context, string, string, int, string) (map[string]any, error)
	KickMember(context.Context, string, string, string) (map[string]any, error)
	BanMember(context.Context, string, string, int, string) (map[string]any, error)
	RemoveMemberTimeout(context.Context, string, string, string) (map[string]any, error)
	UnbanMember(context.Context, string, string, string) (map[string]any, error)
}

// DiscordPreparedDMClient opens a DM before enforcement and sends the final structured notification afterward.
type DiscordPreparedDMClient interface {
	PrepareDM(context.Context, string) (string, error)
	SendPreparedDM(context.Context, string, string) (map[string]any, error)
}

// DiscordCaseNotificationClient owns case-notification presentation and delivery,
// returning the rendered attempt even on error for durable core bookkeeping.
type DiscordCaseNotificationClient interface {
	SendCaseNotification(context.Context, CaseNotificationRequest) (CaseNotificationReceipt, error)
}

// DiscordActionError is the classified Discord failure adapters return from
// enforcement calls. It is re-exported so callers of this package can inspect
// the classification without importing actionmods.
type DiscordActionError = actionmods.DiscordError
