package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

var (
	// ErrAuthorizationDenied reports a current Discord authority or target-safety denial.
	ErrAuthorizationDenied = errors.New("authorization denied")
	// ErrAuthorizationUnavailable reports that live Discord authority could not be refreshed safely.
	ErrAuthorizationUnavailable = errors.New("live discord authorization unavailable")
)

const (
	authorizationReasonMemberRequired     = "actor_not_in_guild"
	authorizationReasonPermissionRequired = "permission_required"
	authorizationReasonSelfTarget         = "self_target"
	authorizationReasonBotTarget          = "bot_target"
	authorizationReasonOwnerTarget        = "guild_owner_target"
	authorizationReasonTargetRequired     = "target_not_in_guild"
	authorizationReasonActorHierarchy     = "actor_hierarchy"
	authorizationReasonBotHierarchy       = "bot_hierarchy"
	authorizationReasonBotPermission      = "bot_permission_required"
	authorizationReasonBotMembership      = "bot_not_in_guild"
	authorizationReasonGuildMismatch      = "guild_mismatch"
	authorizationReasonIdentityMismatch   = "identity_mismatch"
)

// AuthorizationError is a stable transport-neutral denial with a non-sensitive reason code.
type AuthorizationError struct {
	Capability   model.PermissionAction
	Reason       string
	MetadataJSON string
	// RequiredPermission identifies the specific Discord permission that failed; zero means no specific failing permission was recorded.
	RequiredPermission uint64
}

// Error returns a safe denial description suitable for adapter mapping and audit evidence.
func (e *AuthorizationError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return ErrAuthorizationDenied.Error()
	}
	return fmt.Sprintf("%s: %s", ErrAuthorizationDenied, e.Reason)
}

// Unwrap preserves sentinel matching without exposing Discord adapter failures.
func (e *AuthorizationError) Unwrap() error { return ErrAuthorizationDenied }

// Authorize checks a successfully refreshed staff context against one shared capability map.
func (s *GuildService) Authorize(ctx context.Context, guildContext *GuildStaffContext, capability model.PermissionAction, source model.AuditSource) error {
	ctx = ensureTraceContext(ctx)
	if guildContext == nil || guildContext.Guild == nil {
		return &AuthorizationError{Capability: capability, Reason: authorizationReasonMemberRequired}
	}
	if !guildContext.Live.Actor.Present {
		err := &AuthorizationError{Capability: capability, Reason: authorizationReasonMemberRequired}
		_ = s.auditAuthorizationDenial(ctx, guildContext, capability, source, err.Reason)
		return err
	}
	if capability != "" && !guildContext.Can(capability) {
		err := &AuthorizationError{Capability: capability, Reason: authorizationReasonPermissionRequired}
		if capability == model.PermissionActionCaseCreate {
			err.RequiredPermission = permissionModerateMembers
		}
		_ = s.auditAuthorizationDenial(ctx, guildContext, capability, source, err.Reason)
		return err
	}
	return nil
}

// PreflightCase refreshes actor, target, bot, guild, permission, and hierarchy state immediately before case persistence.
func (s *GuildService) PreflightCase(ctx context.Context, guildContext *GuildStaffContext, targetDiscordUserID string, actionType model.ActionType) error {
	ctx = ensureTraceContext(ctx)
	if s == nil || s.store == nil || s.discord == nil || guildContext == nil || guildContext.Guild == nil {
		return ErrAuthorizationUnavailable
	}
	actorID := guildContext.ActorDiscordUserID
	if actorID == "" && guildContext.Staff != nil {
		actorID = guildContext.Staff.DiscordUserID
	}
	snapshot, err := s.discord.GuildAuthorization(ctx, guildContext.Guild.DiscordGuildID, actorID, strings.TrimSpace(targetDiscordUserID))
	if err != nil || snapshot == nil {
		return ErrAuthorizationUnavailable
	}
	if snapshot.Guild.ID != guildContext.Guild.DiscordGuildID {
		return caseDenial(actionType, authorizationReasonGuildMismatch)
	}
	if snapshot.Actor.DiscordUserID != actorID || snapshot.Target == nil || snapshot.Target.DiscordUserID != strings.TrimSpace(targetDiscordUserID) {
		return caseDenial(actionType, authorizationReasonIdentityMismatch)
	}

	guildContext.Live = *snapshot
	guildContext.PermissionBits = snapshot.Actor.PermissionBits
	role := discordRoleContext(snapshot.Actor.PermissionBits, snapshot.Guild.OwnerID == actorID)
	guildContext.Permissions = role.permissions
	guildContext.IsAdmin = role.isAdmin
	guildContext.IsModerator = role.isModerator

	if !snapshot.Actor.Present {
		return caseDenial(actionType, authorizationReasonMemberRequired)
	}
	if !guildContext.Can(model.PermissionActionCaseCreate) {
		return caseDenial(actionType, authorizationReasonPermissionRequired)
	}
	if !snapshot.Bot.Present {
		return caseDenial(actionType, authorizationReasonBotMembership)
	}
	if !snapshot.Target.Present {
		return caseDenial(actionType, authorizationReasonTargetRequired)
	}
	target := *snapshot.Target
	if target.DiscordUserID == snapshot.Actor.DiscordUserID {
		return caseDenial(actionType, authorizationReasonSelfTarget)
	}
	if target.Bot || target.DiscordUserID == snapshot.Bot.DiscordUserID {
		return caseDenial(actionType, authorizationReasonBotTarget)
	}
	if target.DiscordUserID == snapshot.Guild.OwnerID {
		return caseDenial(actionType, authorizationReasonOwnerTarget)
	}
	if snapshot.Actor.DiscordUserID != snapshot.Guild.OwnerID && target.TopRolePosition >= snapshot.Actor.TopRolePosition {
		return caseDenial(actionType, authorizationReasonActorHierarchy)
	}
	if target.TopRolePosition >= snapshot.Bot.TopRolePosition {
		return caseDenial(actionType, authorizationReasonBotHierarchy)
	}

	required := actionPermission(actionType)
	if required != 0 && !hasDiscordPermission(snapshot.Actor.PermissionBits, required) && snapshot.Actor.DiscordUserID != snapshot.Guild.OwnerID {
		return casePermissionDenial(actionType, authorizationReasonPermissionRequired, required)
	}
	if required != 0 && !hasDiscordPermission(snapshot.Bot.PermissionBits, required) {
		return casePermissionDenial(actionType, authorizationReasonBotPermission, required)
	}
	return nil
}

// PreflightSystemCase refreshes the guild, bot, and target immediately before
// honeypot persistence. It deliberately omits staff authority while preserving
// Quack self-protection, live staff exemptions, hierarchy and bot capability.
func (s *GuildService) PreflightSystemCase(ctx context.Context, guildContext *GuildStaffContext, targetDiscordUserID string, actionType model.ActionType) error {
	ctx = ensureTraceContext(ctx)
	if s == nil || s.store == nil || s.discord == nil || guildContext == nil || guildContext.Guild == nil {
		return ErrAuthorizationUnavailable
	}
	targetDiscordUserID = strings.TrimSpace(targetDiscordUserID)
	snapshot, err := s.discord.GuildAuthorization(ctx, guildContext.Guild.DiscordGuildID, "", targetDiscordUserID)
	if err != nil || snapshot == nil {
		return ErrAuthorizationUnavailable
	}
	if snapshot.Guild.ID != guildContext.Guild.DiscordGuildID {
		return caseDenial(actionType, authorizationReasonGuildMismatch)
	}
	if snapshot.Target == nil || snapshot.Target.DiscordUserID != targetDiscordUserID {
		return caseDenial(actionType, authorizationReasonIdentityMismatch)
	}
	guildContext.Live = *snapshot
	guildContext.PermissionBits = 0
	if !snapshot.Bot.Present {
		return caseDenial(actionType, authorizationReasonBotMembership)
	}
	if !snapshot.Target.Present {
		return caseDenial(actionType, authorizationReasonTargetRequired)
	}
	target := *snapshot.Target
	if target.DiscordUserID == snapshot.Bot.DiscordUserID {
		return caseDenial(actionType, authorizationReasonBotTarget)
	}
	if target.DiscordUserID == snapshot.Guild.OwnerID {
		return caseDenial(actionType, authorizationReasonOwnerTarget)
	}
	// Honeypots apply to ordinary members, including third-party bots. Current
	// moderation authority exempts humans and bots alike, even after queue delay.
	if hasDiscordPermission(target.PermissionBits, permissionModerateMembers) {
		return caseDenial(actionType, "honeypot_staff_exempt")
	}
	if target.TopRolePosition >= snapshot.Bot.TopRolePosition {
		return caseDenial(actionType, authorizationReasonBotHierarchy)
	}
	required := actionPermission(actionType)
	if required != 0 && !hasDiscordPermission(snapshot.Bot.PermissionBits, required) {
		return casePermissionDenial(actionType, authorizationReasonBotPermission, required)
	}
	return nil
}

// PreflightReversal refreshes current authority while allowing unban to reference a departed member.
func (s *GuildService) PreflightReversal(ctx context.Context, guildContext *GuildStaffContext, targetDiscordUserID string, actionType model.ActionType) error {
	if actionType != model.ActionRemoveTimeout && actionType != model.ActionUnbanUser {
		return caseDenial(actionType, "invalid_reversal")
	}
	if s == nil || s.store == nil || s.discord == nil || guildContext == nil || guildContext.Guild == nil {
		return ErrAuthorizationUnavailable
	}
	actorID := guildContext.ActorDiscordUserID
	if actorID == "" && guildContext.Staff != nil {
		actorID = guildContext.Staff.DiscordUserID
	}
	snapshot, err := s.discord.GuildAuthorization(ctx, guildContext.Guild.DiscordGuildID, actorID, targetDiscordUserID)
	if err != nil || snapshot == nil {
		return ErrAuthorizationUnavailable
	}
	guildContext.Live = *snapshot
	guildContext.PermissionBits = snapshot.Actor.PermissionBits
	role := discordRoleContext(snapshot.Actor.PermissionBits, snapshot.Guild.OwnerID == actorID)
	guildContext.Permissions = role.permissions
	guildContext.IsAdmin = role.isAdmin
	guildContext.IsModerator = role.isModerator
	if !snapshot.Actor.Present || !guildContext.Can(model.PermissionActionCaseCreate) {
		return caseDenial(actionType, authorizationReasonPermissionRequired)
	}
	if !snapshot.Bot.Present {
		return caseDenial(actionType, authorizationReasonBotMembership)
	}
	required := actionPermission(actionType)
	if !hasDiscordPermission(snapshot.Actor.PermissionBits, required) && actorID != snapshot.Guild.OwnerID {
		return casePermissionDenial(actionType, authorizationReasonPermissionRequired, required)
	}
	if !hasDiscordPermission(snapshot.Bot.PermissionBits, required) {
		return casePermissionDenial(actionType, authorizationReasonBotPermission, required)
	}
	if actionType == model.ActionRemoveTimeout {
		if snapshot.Target == nil || !snapshot.Target.Present {
			return caseDenial(actionType, authorizationReasonTargetRequired)
		}
		if actorID != snapshot.Guild.OwnerID && snapshot.Target.TopRolePosition >= snapshot.Actor.TopRolePosition {
			return caseDenial(actionType, authorizationReasonActorHierarchy)
		}
		if snapshot.Target.TopRolePosition >= snapshot.Bot.TopRolePosition {
			return caseDenial(actionType, authorizationReasonBotHierarchy)
		}
	}
	return nil
}

// hasDiscordPermission applies Discord's Administrator implication before checking a specific permission.
func hasDiscordPermission(bits, required uint64) bool {
	return hasAllBits(bits, permissionAdministrator) || hasAllBits(bits, required)
}

// caseDenial constructs the typed denial that CaseService audits only after its transaction rolls back.
func caseDenial(actionType model.ActionType, reason string) *AuthorizationError {
	capability := model.PermissionActionCaseCreate
	metadata, _ := json.Marshal(map[string]string{"selected_action": string(actionType)})
	denial := &AuthorizationError{Capability: capability, Reason: reason, MetadataJSON: string(metadata)}
	if reason == authorizationReasonPermissionRequired {
		denial.RequiredPermission = permissionModerateMembers
	}
	return denial
}

// casePermissionDenial retains the permission that actually failed, distinguishing
// basic case creation authority from the selected enforcement action's authority.
func casePermissionDenial(actionType model.ActionType, reason string, required uint64) error {
	denial := caseDenial(actionType, reason)
	denial.RequiredPermission = required
	return denial
}

// auditAuthorizationDenial appends immutable capability evidence with trace identifiers.
func (s *GuildService) auditAuthorizationDenial(ctx context.Context, guildContext *GuildStaffContext, capability model.PermissionAction, source model.AuditSource, reason string) error {
	return s.auditAuthorizationDenialWithMetadata(ctx, guildContext, capability, source, reason, "{}")
}

// auditAuthorizationDenialWithMetadata appends a denial while retaining operation-specific safe metadata.
func (s *GuildService) auditAuthorizationDenialWithMetadata(ctx context.Context, guildContext *GuildStaffContext, capability model.PermissionAction, source model.AuditSource, reason, metadata string) error {
	if s == nil || s.store == nil || guildContext == nil || guildContext.Guild == nil {
		return errors.New("authorization audit is not configured")
	}
	requestID, correlationID := TraceIDsFromContext(ctx)
	actorID := guildContext.ActorDiscordUserID
	if actorID == "" && guildContext.Staff != nil {
		actorID = guildContext.Staff.DiscordUserID
	}
	return recordAudit(ctx, s.store, &model.AuditLogEntry{
		GuildID: guildContext.Guild.ID, ActorDiscordUserID: actorID,
		ActorPermissionBits: guildContext.PermissionBits, Source: source,
		Action: "authorization.denied", ResourceType: "permission", ResourceID: string(capability),
		Result: model.AuditResultDenied, FailureReason: reason,
		CorrelationID: correlationID, RequestID: requestID, MetadataJSON: metadata,
	})
}

// actionPermission maps the configured enforcement action to Discord's current required permission.
func actionPermission(actionType model.ActionType) uint64 {
	switch actionType {
	case model.ActionTimeoutUser:
		return permissionModerateMembers
	case model.ActionKickUser:
		return permissionKickMembers
	case model.ActionBanUser:
		return permissionBanMembers
	case model.ActionRemoveTimeout:
		return permissionModerateMembers
	case model.ActionUnbanUser:
		return permissionBanMembers
	default:
		return 0
	}
}

// authorizationSource converts a case source into the adapter source recorded by the audit log.
func authorizationSource(source model.CaseSource) model.AuditSource {
	if source == model.CaseSourceDiscord {
		return model.AuditSourceDiscord
	}
	return model.AuditSourceAPI
}

// authorizationNow centralizes cache activity timestamps for live resolution.
func authorizationNow() time.Time { return time.Now().UTC() }
