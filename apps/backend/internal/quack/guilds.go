package quack

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// GuildRepository is the persistence GuildService needs: guild and staff cache
// rows, install bootstrap, deactivation, and channel-reference repair.
type GuildRepository interface {
	BootstrapGuild(context.Context, model.BootstrapGuildParams) (*model.BootstrapGuildResult, error)
	ClearGuildChannelReferences(context.Context, string, string, *model.AuditLogEntry) (*model.GuildSettings, error)
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
	DeactivateGuild(context.Context, string, *model.AuditLogEntry) (*model.Guild, error)
	GetGuildByDiscordID(context.Context, string) (*model.Guild, error)
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	GetStaffMember(context.Context, string, string) (*model.StaffMember, error)
	UpsertGuild(context.Context, model.UpsertGuildParams) (*model.Guild, error)
	UpsertStaffMember(context.Context, model.UpsertStaffMemberParams) (*model.StaffMember, error)
}

// ErrBotNotInGuild reports that Quack is not a member of the requested guild,
// so no staff context or live authorization can exist for it.
var ErrBotNotInGuild = errors.New("bot is not in guild")

type GuildService struct {
	store   GuildRepository
	discord DiscordClient
}

// GuildStaffContext is the per-request result of resolving an actor against
// live Discord state: the persisted guild, the cached staff row (nil when the
// actor was never seen as a member), and the capability map derived from the
// actor's current permission bits. Live keeps the snapshot it was built from
// so later preflight checks can compare against it.
type GuildStaffContext struct {
	Guild              *model.Guild
	Staff              *model.StaffMember
	ActorDiscordUserID string
	PermissionBits     uint64
	Permissions        map[model.PermissionAction]bool
	IsAdmin            bool
	IsModerator        bool
	Live               DiscordGuildAuthorization
}

// UserGuildListItem is one dashboard guild-picker row: a guild the user can
// manage or moderate, annotated with whether Quack is installed there.
type UserGuildListItem struct {
	DiscordGuildID  string `json:"discord_guild_id"`
	Name            string `json:"name"`
	IconURL         string `json:"icon_url"`
	PermissionBits  string `json:"permission_bits"`
	IsOwner         bool   `json:"is_owner"`
	IsAdministrator bool   `json:"is_administrator"`
	CanManageGuild  bool   `json:"can_manage_guild"`
	CanModerate     bool   `json:"can_moderate"`
	QuackInGuild    bool   `json:"quack_in_guild"`
	QuackGuildName  string `json:"quack_guild_name,omitempty"`
}

// DiscordStaffContextInput identifies the interaction actor for
// ResolveDiscordStaffContext. PermissionBits and LastActiveAt are accepted from
// the interaction payload but not consulted: authority always comes from a
// fresh Discord lookup, and the activity timestamp is taken at resolution time.
type DiscordStaffContextInput struct {
	DiscordGuildID string
	DiscordUserID  string
	DisplayName    string
	PermissionBits uint64
	LastActiveAt   time.Time
}

// DiscordGuildLifecycleInput carries authoritative guild metadata and an optional complete channel inventory from a gateway event.
type DiscordGuildLifecycleInput struct {
	DiscordGuildID         string
	Name                   string
	Icon                   string
	OwnerDiscordUserID     string
	KnownChannelDiscordIDs []string
}

// NewGuildService returns a service over store. discord may be nil for
// storage-only composition (see New); methods that need live Discord state then
// return ErrAuthorizationUnavailable or a configuration error.
func NewGuildService(store GuildRepository, discord DiscordClient) *GuildService {
	return &GuildService{store: store, discord: discord}
}

// ListUserManageableGuilds returns the session user's guilds where they own,
// administer, manage, or moderate, marking those the bot is also in. It reads
// Discord only and needs the session's OAuth access token.
func (s *GuildService) ListUserManageableGuilds(ctx context.Context, session *model.AuthSession) ([]UserGuildListItem, error) {
	if s.discord == nil {
		return nil, errors.New("discord client is not configured")
	}
	if session == nil || session.AccessToken == "" {
		return nil, errors.New("missing auth session")
	}

	userGuilds, err := s.discord.UserGuilds(ctx, session.AccessToken)
	if err != nil {
		return nil, err
	}

	botGuilds, err := s.discord.BotGuilds(ctx)
	if err != nil {
		return nil, err
	}

	botGuildsByID := make(map[string]DiscordBotGuild, len(botGuilds))
	for _, guild := range botGuilds {
		botGuildsByID[guild.ID] = guild
	}

	out := make([]UserGuildListItem, 0, len(userGuilds))
	for _, guild := range userGuilds {
		isAdmin := hasAllBits(guild.Permissions, permissionAdministrator)
		canManageGuild := guild.Owner || isAdmin || hasAllBits(guild.Permissions, permissionManageGuild)
		canModerate := guild.Owner || isAdmin || hasAllBits(guild.Permissions, permissionModerateMembers)
		if !canManageGuild && !canModerate {
			continue
		}

		item := UserGuildListItem{
			DiscordGuildID:  guild.ID,
			Name:            guild.Name,
			IconURL:         discordGuildIconURL(guild.ID, guild.Icon),
			PermissionBits:  PermissionBitsString(guild.Permissions),
			IsOwner:         guild.Owner,
			IsAdministrator: isAdmin,
			CanManageGuild:  canManageGuild,
			CanModerate:     canModerate,
		}

		if botGuild, ok := botGuildsByID[guild.ID]; ok {
			item.QuackInGuild = true
			item.QuackGuildName = botGuild.Name
		}

		out = append(out, item)
	}

	return out, nil
}

// ResolveStaffContext refreshes the dashboard session user's membership and
// permissions in one guild from Discord, upserts the guild and staff cache
// rows, and returns the context every authorized use case takes. Adapter
// failures surface as ErrBotNotInGuild or ErrAuthorizationUnavailable.
func (s *GuildService) ResolveStaffContext(
	ctx context.Context, session *model.AuthSession, discordGuildID string,
) (*GuildStaffContext, error) {
	if s.discord == nil {
		return nil, errors.New("discord client is not configured")
	}
	if session == nil || session.DiscordUserID == "" {
		return nil, errors.New("missing auth session")
	}

	discordGuildID = strings.TrimSpace(discordGuildID)
	if discordGuildID == "" {
		return nil, errors.New("missing discord guild id")
	}

	snapshot, err := s.discord.GuildAuthorization(ctx, discordGuildID, session.DiscordUserID, "")
	if err != nil || snapshot == nil {
		if errors.Is(err, ErrBotNotInGuild) {
			return nil, ErrBotNotInGuild
		}
		return nil, ErrAuthorizationUnavailable
	}
	if snapshot.Guild.ID != discordGuildID {
		return nil, ErrAuthorizationUnavailable
	}
	return s.contextFromAuthorization(ctx, snapshot, session.DiscordUserID, staffDisplayName(session))
}

// ResolveDiscordStaffContext is the interaction-side counterpart of
// ResolveStaffContext: it ignores permission bits carried by the interaction
// and reads the actor's current state from Discord.
func (s *GuildService) ResolveDiscordStaffContext(ctx context.Context, input DiscordStaffContextInput) (*GuildStaffContext, error) {
	if s.discord == nil {
		return nil, errors.New("discord client is not configured")
	}

	discordGuildID := strings.TrimSpace(input.DiscordGuildID)
	if discordGuildID == "" {
		return nil, errors.New("missing discord guild id")
	}
	discordUserID := strings.TrimSpace(input.DiscordUserID)
	if discordUserID == "" {
		return nil, errors.New("missing discord user id")
	}

	snapshot, err := s.discord.GuildAuthorization(ctx, discordGuildID, discordUserID, "")
	if err != nil || snapshot == nil {
		if errors.Is(err, ErrBotNotInGuild) {
			return nil, ErrBotNotInGuild
		}
		return nil, ErrAuthorizationUnavailable
	}
	if snapshot.Guild.ID != discordGuildID {
		return nil, ErrAuthorizationUnavailable
	}
	return s.contextFromAuthorization(ctx, snapshot, discordUserID, input.DisplayName)
}

// contextFromAuthorization materializes a request context from live Discord
// state. The guild row is always refreshed; the staff row is upserted only when
// the actor is currently a member, so departed staff keep their last-seen
// attribution without regaining authority.
func (s *GuildService) contextFromAuthorization(
	ctx context.Context, snapshot *DiscordGuildAuthorization, actorDiscordUserID, fallbackDisplayName string,
) (*GuildStaffContext, error) {
	if snapshot == nil || snapshot.Guild.ID == "" || snapshot.Guild.ID != strings.TrimSpace(snapshot.Guild.ID) {
		return nil, ErrAuthorizationUnavailable
	}
	if snapshot.Actor.DiscordUserID != actorDiscordUserID {
		return nil, ErrAuthorizationUnavailable
	}
	guild, err := s.store.UpsertGuild(ctx, model.UpsertGuildParams{
		DiscordGuildID:     snapshot.Guild.ID,
		Name:               snapshot.Guild.Name,
		IconURL:            discordGuildIconURL(snapshot.Guild.ID, snapshot.Guild.Icon),
		OwnerDiscordUserID: snapshot.Guild.OwnerID,
	})
	if err != nil {
		return nil, err
	}

	var staff *model.StaffMember
	if snapshot.Actor.Present {
		displayName := strings.TrimSpace(snapshot.Actor.DisplayName)
		if displayName == "" {
			displayName = strings.TrimSpace(fallbackDisplayName)
		}
		if displayName == "" {
			displayName = actorDiscordUserID
		}
		staff, err = s.store.UpsertStaffMember(ctx, model.UpsertStaffMemberParams{
			GuildID:                guild.ID,
			DiscordUserID:          actorDiscordUserID,
			LastSeenPermissionBits: snapshot.Actor.PermissionBits,
			LastKnownDisplayName:   displayName,
			LastActiveAt:           time.Now().UTC(),
		})
	} else {
		staff, err = s.store.GetStaffMember(ctx, guild.ID, actorDiscordUserID)
	}
	if err != nil {
		return nil, err
	}

	isOwner := snapshot.Guild.OwnerID == actorDiscordUserID
	role := discordRoleContext(snapshot.Actor.PermissionBits, isOwner)
	return &GuildStaffContext{
		Guild:              guild,
		Staff:              staff,
		ActorDiscordUserID: actorDiscordUserID,
		PermissionBits:     snapshot.Actor.PermissionBits,
		Permissions:        role.permissions,
		IsAdmin:            role.isAdmin,
		IsModerator:        role.isModerator,
		Live:               *snapshot,
	}, nil
}

// Can reports whether the context grants action. A nil context or nil
// capability map grants nothing.
func (ctx *GuildStaffContext) Can(action model.PermissionAction) bool {
	if ctx == nil || ctx.Permissions == nil {
		return false
	}
	return ctx.Permissions[action]
}

type discordRole struct {
	isAdmin     bool
	isModerator bool
	permissions map[model.PermissionAction]bool
}

// discordRoleContext classifies permission bits: the owner or Administrator is
// an admin with every capability; Moderate Members alone makes a moderator;
// Manage Guild alone configures without moderating.
func discordRoleContext(permissionBits uint64, isOwner bool) discordRole {
	isAdmin := isOwner || hasAllBits(permissionBits, permissionAdministrator)
	hasManageGuild := hasAllBits(permissionBits, permissionManageGuild)
	hasModerateMembers := hasAllBits(permissionBits, permissionModerateMembers)
	canManage := isAdmin || hasManageGuild
	canModerate := isAdmin || hasModerateMembers

	role := discordRole{
		isAdmin:     isAdmin,
		isModerator: !isAdmin && hasModerateMembers,
	}
	role.permissions = discordPermissionMap(canModerate, canManage)
	return role
}

// discordPermissionMap is the single capability table: moderation actions
// require Moderate Members and configuration requires Manage Guild.
func discordPermissionMap(canModerate, canManage bool) map[model.PermissionAction]bool {
	return map[model.PermissionAction]bool{
		model.PermissionActionCaseCreate:         canModerate,
		model.PermissionActionCaseRead:           canModerate,
		model.PermissionActionCaseTemplateRead:   canModerate || canManage,
		model.PermissionActionCaseTemplateWrite:  canManage,
		model.PermissionActionCaseTemplateDelete: canManage,
		model.PermissionActionAppealReview:       canModerate,
		model.PermissionActionTicketResolve:      canModerate,
		model.PermissionActionAuditRead:          canModerate,
		model.PermissionActionGuildSettingsRead:  canManage,
		model.PermissionActionGuildSettingsWrite: canManage,
		model.PermissionActionCaseVoid:           canModerate,
		model.PermissionActionFailureDismiss:     canModerate,
	}
}

// hasAllBits reports whether every bit in required is set; required == 0 always matches.
func hasAllBits(bits, required uint64) bool {
	if required == 0 {
		return true
	}

	return bits&required == required
}

func staffDisplayName(session *model.AuthSession) string {
	if session == nil {
		return ""
	}
	if strings.TrimSpace(session.GlobalName) != "" {
		return session.GlobalName
	}
	return session.Username
}

// PermissionMapStrings converts a capability map to string keys for JSON responses.
func PermissionMapStrings(permissions map[model.PermissionAction]bool) map[string]bool {
	out := make(map[string]bool, len(permissions))
	for action, allowed := range permissions {
		out[string(action)] = allowed
	}
	return out
}

// PermissionBitsString renders permission bits in decimal. Responses carry
// them as strings because Discord's bit set exceeds JavaScript's safe integer range.
func PermissionBitsString(bits uint64) string {
	return strconv.FormatUint(bits, 10)
}

// GuildOperationalHealth reports guild-local Discord and managed-channel
// degradation without changing process readiness for healthy guilds.
type GuildOperationalHealth struct {
	Degraded        bool            `json:"degraded"`
	Reasons         []string        `json:"reasons"`
	BotPermissions  map[string]bool `json:"bot_permissions"`
	ManagedChannels map[string]bool `json:"managed_channels"`
}

// OperationalGuildID resolves a stored guild identity for an already authorized
// operator. It does not require live Discord access, so diagnostics remain usable
// during an outage. An unknown guild returns an empty ID without creating it.
func (s *GuildService) OperationalGuildID(ctx context.Context, discordGuildID string) (string, error) {
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil || guild == nil {
		return "", err
	}
	return guild.ID, nil
}

// OperationalGuildHealth refreshes the bot's current guild permissions and
// combines them with managed-channel configuration. Deleted channel gateway
// reconciliation clears stale references, so missing required references are
// reported immediately and isolated to this guild.
func (s *GuildService) OperationalGuildHealth(ctx context.Context, discordGuildID string) (GuildOperationalHealth, error) {
	status := GuildOperationalHealth{
		Reasons:         []string{},
		BotPermissions:  map[string]bool{},
		ManagedChannels: map[string]bool{},
	}
	if s.discord == nil {
		return status, ErrAuthorizationUnavailable
	}
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil || guild == nil {
		return status, ErrBotNotInGuild
	}
	live, err := s.discord.GuildAuthorization(ctx, guild.DiscordGuildID, "", "")
	if err != nil || live == nil || !live.Bot.Present {
		status.Degraded = true
		status.Reasons = append(status.Reasons, "discord_bot_unavailable")
		return status, nil
	}
	permissions := map[string]uint64{
		"moderate_members": permissionModerateMembers,
		"kick_members":     permissionKickMembers,
		"ban_members":      permissionBanMembers,
		"manage_channels":  permissionManageChannels,
	}
	for name, permission := range permissions {
		available := hasDiscordPermission(live.Bot.PermissionBits, permission)
		status.BotPermissions[name] = available
		if !available {
			status.Degraded = true
			status.Reasons = append(status.Reasons, "missing_bot_permission:"+name)
		}
	}
	settings, err := s.store.GetGuildSettings(ctx, guild.ID)
	if err != nil || settings == nil {
		return status, err
	}
	status.ManagedChannels["evidence"] = strings.TrimSpace(settings.ManagedEvidenceChannelDiscordID) != ""
	status.ManagedChannels["audit_mirror"] = strings.TrimSpace(settings.AuditMirrorChannelDiscordID) != ""
	if !status.ManagedChannels["evidence"] {
		status.Degraded = true
		status.Reasons = append(status.Reasons, "managed_evidence_channel_unavailable")
	}
	return status, nil
}

// BootstrapDiscordGuild atomically installs or reactivates a guild, refreshes
// metadata, repairs stale channels, and ensures one starter policy.
func (s *GuildService) BootstrapDiscordGuild(
	ctx context.Context, input DiscordGuildLifecycleInput,
) (*model.BootstrapGuildResult, error) {
	guildID := strings.TrimSpace(input.DiscordGuildID)
	if guildID == "" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.OwnerDiscordUserID) == "" {
		return nil, errors.New("guild id, name, and owner are required")
	}
	return s.store.BootstrapGuild(ctx, model.BootstrapGuildParams{
		DiscordGuildID:         guildID,
		Name:                   strings.TrimSpace(input.Name),
		IconURL:                discordGuildIconURL(guildID, strings.TrimSpace(input.Icon)),
		OwnerDiscordUserID:     strings.TrimSpace(input.OwnerDiscordUserID),
		KnownChannelDiscordIDs: input.KnownChannelDiscordIDs,
	})
}

// DeactivateDiscordGuild marks a true guild departure inactive while preserving settings, starter policy, and history.
func (s *GuildService) DeactivateDiscordGuild(ctx context.Context, discordGuildID string) (*model.Guild, error) {
	return s.store.DeactivateGuild(ctx, strings.TrimSpace(discordGuildID), systemGuildAudit("guild.lifecycle.leave", "guild"))
}

// ClearDeletedChannel removes stale core settings references when Discord confirms a configured channel was deleted.
func (s *GuildService) ClearDeletedChannel(ctx context.Context, discordGuildID, channelID string) (*model.GuildSettings, error) {
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil || guild == nil {
		return nil, err
	}
	return s.store.ClearGuildChannelReferences(
		ctx, guild.ID, strings.TrimSpace(channelID), systemGuildAudit("guild_settings.channel_reference.cleared", "guild_settings"),
	)
}

// systemGuildAudit creates adapter-attributed lifecycle evidence without pretending the bot is a Discord staff member.
func systemGuildAudit(action, resourceType string) *model.AuditLogEntry {
	return &model.AuditLogEntry{
		ActorDiscordUserID: "quack-system",
		Source:             model.AuditSourceDiscord,
		Action:             action,
		ResourceType:       resourceType,
		Result:             model.AuditResultSuccess,
		MetadataJSON:       "{}",
	}
}
