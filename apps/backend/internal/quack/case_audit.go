package quack

import (
	"context"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// audit writes one staff-attributed audit row for a case operation. It returns
// the storage error so callers can decide whether a lost audit fails the request.
func (s *CaseService) audit(
	ctx context.Context,
	guildContext *GuildStaffContext,
	action, resourceType, resourceID string,
	result model.AuditResult,
	failureReason string,
) error {
	attribution := caseCreateAttribution{actorType: "staff", auditSource: model.AuditSourceAPI}
	return s.auditWithAttribution(ctx, guildContext, attribution, action, resourceType, resourceID, result, failureReason)
}

// auditWithAttribution appends case evidence without inventing a Discord actor
// for system automation. A missing guild or staff context records nothing.
func (s *CaseService) auditWithAttribution(
	ctx context.Context,
	guildContext *GuildStaffContext,
	attribution caseCreateAttribution,
	action, resourceType, resourceID string,
	result model.AuditResult,
	failureReason string,
) error {
	entry := s.auditEntryWithAttribution(ctx, guildContext, attribution, action, resourceType, resourceID, result, failureReason)
	if entry == nil {
		return nil
	}
	return recordAudit(ctx, s.store, entry)
}

// auditEntry builds, without persisting, the staff-attributed audit row that
// repository writes attach to their transaction.
func (s *CaseService) auditEntry(
	ctx context.Context,
	guildContext *GuildStaffContext,
	action, resourceType, resourceID string,
	result model.AuditResult,
	failureReason string,
) *model.AuditLogEntry {
	attribution := caseCreateAttribution{actorType: "staff", auditSource: model.AuditSourceAPI}
	return s.auditEntryWithAttribution(ctx, guildContext, attribution, action, resourceType, resourceID, result, failureReason)
}

// auditEntryWithAttribution builds the atomic case audit row for either a
// current staff actor or Quack's restricted honeypot system actor. It returns
// nil when the guild or staff context is missing; an empty resource ID is
// recorded as "unknown".
func (s *CaseService) auditEntryWithAttribution(
	ctx context.Context,
	guildContext *GuildStaffContext,
	attribution caseCreateAttribution,
	action, resourceType, resourceID string,
	result model.AuditResult,
	failureReason string,
) *model.AuditLogEntry {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil
	}
	requestID, correlationID := TraceIDsFromContext(ctx)
	actorDiscordUserID := guildContext.Staff.DiscordUserID
	permissionBits := guildContext.PermissionBits
	if attribution.system {
		actorDiscordUserID = ""
		permissionBits = 0
	}

	entry := &model.AuditLogEntry{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  actorDiscordUserID,
		ActorPermissionBits: permissionBits,
		Source:              AuditSourceFromContext(ctx),
		Action:              action,
		ResourceType:        resourceType,
		ResourceID:          resourceID,
		Result:              result,
		FailureReason:       failureReason,
		CorrelationID:       correlationID,
		RequestID:           requestID,
		MetadataJSON:        "{}",
	}
	if entry.ResourceID == "" {
		entry.ResourceID = "unknown"
	}
	return entry
}

// ensureTraceContext guarantees the context carries both a request ID and a
// correlation ID, generating whichever is missing, so every row written by a
// case operation can be joined to the same trace.
func ensureTraceContext(ctx context.Context) context.Context {
	if RequestIDFromContext(ctx) != "" && CorrelationIDFromContext(ctx) != "" {
		return ctx
	}
	return ContextWithTrace(ctx, RequestIDFromContext(ctx), CorrelationIDFromContext(ctx))
}
