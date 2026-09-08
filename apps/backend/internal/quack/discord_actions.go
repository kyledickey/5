package quack

import (
	"context"

	actionmods "github.com/quackdiscord/bot/internal/quack/actionmods"
)

// DiscordActionClient defines the external operations needed by this package, keeping the concrete client at the adapter boundary.
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

// DiscordActionError carries classified discord action error failure details across package boundaries.
type DiscordActionError = actionmods.DiscordError
