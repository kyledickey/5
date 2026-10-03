package quack

import (
	"context"
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/actionmods"
)

// Discord permission bits this package evaluates. They mirror Discord's
// documented values so the core never depends on discordgo.
const (
	permissionKickMembers     uint64 = 1 << 1
	permissionBanMembers      uint64 = 1 << 2
	permissionAdministrator   uint64 = 1 << 3
	permissionManageChannels  uint64 = 1 << 4
	permissionManageGuild     uint64 = 1 << 5
	permissionModerateMembers uint64 = 1 << 40
)

// DiscordClient is the read-only Discord access GuildService needs: the OAuth
// user's guild list for the dashboard picker, the bot's guild membership, and a
// fresh per-request authorization snapshot. The discordbot adapter implements
// it; tests substitute fakes.
type DiscordClient interface {
	UserGuilds(ctx context.Context, accessToken string) ([]DiscordUserGuild, error)
	BotGuilds(ctx context.Context) ([]DiscordBotGuild, error)
	BotGuild(ctx context.Context, discordGuildID string) (*DiscordBotGuild, error)
	GuildAuthorization(ctx context.Context, discordGuildID, actorDiscordUserID, targetDiscordUserID string) (*DiscordGuildAuthorization, error)
}

// DiscordUserGuild is one guild from the OAuth user's guild list together with
// the permission bits Discord reports for that user in it.
type DiscordUserGuild struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Owner       bool   `json:"owner"`
	Permissions uint64 `json:"permissions,string"`
}

type DiscordBotGuild struct {
	ID      string
	Name    string
	Icon    string
	OwnerID string
}

// DiscordGuildAuthorization is a request-scoped snapshot fetched from Discord for one protected operation.
// Target is nil when the operation has no target member.
type DiscordGuildAuthorization struct {
	Guild  DiscordBotGuild
	Actor  DiscordMemberAuthorization
	Bot    DiscordMemberAuthorization
	Target *DiscordMemberAuthorization
}

// DiscordMemberAuthorization captures current guild membership, permissions, account type, and hierarchy position.
// Present is false when the user is no longer a member; the other fields are then zero.
type DiscordMemberAuthorization struct {
	DiscordUserID   string
	DisplayName     string
	PermissionBits  uint64
	TopRolePosition int
	Present         bool
	Bot             bool
}

// discordGuildIconURL builds the CDN icon URL, using gif for animated hashes.
// It returns "" when the guild has no icon.
func discordGuildIconURL(guildID, iconHash string) string {
	if guildID == "" || iconHash == "" {
		return ""
	}

	ext := "png"
	if strings.HasPrefix(iconHash, "a_") {
		ext = "gif"
	}

	return fmt.Sprintf("https://cdn.discordapp.com/icons/%s/%s.%s", guildID, iconHash, ext)
}

// DiscordActionClient is the minimum Discord write capability ActionService
// requires. Richer adapters additionally implement the optional interfaces
// below, which the service discovers by type assertion.
type DiscordActionClient = actionmods.DiscordClient

// DiscordEnforcementClient is the optional adapter capability for real moderation and reversal operations.
type DiscordEnforcementClient = actionmods.EnforcementClient

// DiscordPreparedDMClient opens the member DM channel before an irreversible
// enforcement action so the member is still reachable once it lands.
type DiscordPreparedDMClient interface {
	PrepareDM(context.Context, string) (string, error)
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
