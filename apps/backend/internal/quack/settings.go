package quack

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

const maxGuildNotificationBrandingLength = 2000

var (
	// ErrGuildSettingsValidation reports a malformed guild settings write.
	ErrGuildSettingsValidation = errors.New("guild settings validation failed")
	// ErrGuildSettingsPermissionDenied reports that current Discord authority does not grant Manage Guild configuration access.
	ErrGuildSettingsPermissionDenied = errors.New("guild settings permission denied")
	// ErrGuildSettingsNotFound reports a guild that has not completed install bootstrap.
	ErrGuildSettingsNotFound = errors.New("guild settings not found")
)

// GuildSettingsService owns authorized guild configuration, one-time notice acknowledgement, and immutable audit evidence.
type GuildSettingsService struct {
	store    SettingsRepository
	channels StaffChannelValidator
}

// StaffChannelValidator validates live Discord ownership and privacy of outbound staff destinations.
type StaffChannelValidator interface {
	ValidateStaffChannel(context.Context, string, string) error
}

// WithStaffChannelValidator installs the live outbound destination boundary.
func (s *GuildSettingsService) WithStaffChannelValidator(channels StaffChannelValidator) *GuildSettingsService {
	s.channels = channels
	return s
}

// GuildSettingsInput is a partial settings update; omitted fields retain their current values.
type GuildSettingsInput struct {
	AppealQueueChannelDiscordID     *string `json:"appeal_queue_channel_discord_id"`
	AuditMirrorChannelDiscordID     *string `json:"audit_mirror_channel_discord_id"`
	ManagedEvidenceChannelDiscordID *string `json:"managed_evidence_channel_discord_id"`
	NotificationIntroduction        *string `json:"notification_introduction"`
	NotificationFooter              *string `json:"notification_footer"`
	TicketsEnabled                  *bool   `json:"tickets_enabled"`
	GeneralLoggingEnabled           *bool   `json:"general_logging_enabled"`
	HoneypotEnabled                 *bool   `json:"honeypot_enabled"`
}

// GuildSettingsResponse is the transport-neutral guild setup contract shared by the dashboard and internal adapters.
type GuildSettingsResponse struct {
	AppealQueueChannelDiscordID       string     `json:"appeal_queue_channel_discord_id,omitempty"`
	ID                                string     `json:"id"`
	GuildID                           string     `json:"guild_id"`
	AuditMirrorChannelDiscordID       string     `json:"audit_mirror_channel_discord_id,omitempty"`
	ManagedEvidenceChannelDiscordID   string     `json:"managed_evidence_channel_discord_id,omitempty"`
	NotificationIntroduction          string     `json:"notification_introduction,omitempty"`
	NotificationFooter                string     `json:"notification_footer,omitempty"`
	TicketsEnabled                    bool       `json:"tickets_enabled"`
	GeneralLoggingEnabled             bool       `json:"general_logging_enabled"`
	HoneypotEnabled                   bool       `json:"honeypot_enabled"`
	StarterPolicyTemplateID           string     `json:"starter_policy_template_id"`
	StarterPolicyReviewRequired       bool       `json:"starter_policy_review_required"`
	StarterPolicyNoticeAcknowledgedAt *time.Time `json:"starter_policy_notice_acknowledged_at,omitempty"`
}

// NewGuildSettingsService binds guild configuration to audited persistence.
func NewGuildSettingsService(store SettingsRepository) *GuildSettingsService {
	return &GuildSettingsService{store: store}
}

// Get returns guild settings to current Manage Guild authorities.
func (s *GuildSettingsService) Get(ctx context.Context, guildContext *GuildStaffContext) (*GuildSettingsResponse, error) {
	ctx = ensureTraceContext(ctx)
	if s == nil || s.store == nil || guildContext == nil || guildContext.Guild == nil {
		return nil, errors.New("guild settings service is not configured")
	}
	if !guildContext.Can(model.PermissionActionGuildSettingsRead) {
		_ = s.audit(ctx, guildContext, string(model.AuditActionSettingsRead), model.AuditResultDenied, ErrGuildSettingsPermissionDenied.Error())
		return nil, ErrGuildSettingsPermissionDenied
	}
	settings, err := s.store.GetGuildSettings(ctx, guildContext.Guild.ID)
	if err != nil {
		_ = s.audit(ctx, guildContext, string(model.AuditActionSettingsRead), model.AuditResultFailure, "query_failed")
		return nil, err
	}
	if settings == nil {
		_ = s.audit(ctx, guildContext, string(model.AuditActionSettingsRead), model.AuditResultFailure, "not_found")
		return nil, ErrGuildSettingsNotFound
	}
	response := guildSettingsResponse(*settings)
	if err := s.audit(ctx, guildContext, string(model.AuditActionSettingsRead), model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return &response, nil
}

// Update validates a partial settings write, enforces current Manage Guild authority, and records success, failure, or denial.
func (s *GuildSettingsService) Update(ctx context.Context, guildContext *GuildStaffContext, input GuildSettingsInput) (*GuildSettingsResponse, error) {
	ctx = ensureTraceContext(ctx)
	if s == nil || s.store == nil || guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, errors.New("guild settings service is not configured")
	}
	if !guildContext.Can(model.PermissionActionGuildSettingsWrite) {
		_ = s.audit(ctx, guildContext, "guild_settings.update", model.AuditResultDenied, ErrGuildSettingsPermissionDenied.Error())
		return nil, ErrGuildSettingsPermissionDenied
	}

	settings, err := s.store.GetGuildSettings(ctx, guildContext.Guild.ID)
	if err != nil {
		_ = s.audit(ctx, guildContext, "guild_settings.update", model.AuditResultFailure, err.Error())
		return nil, err
	}
	if settings == nil {
		_ = s.audit(ctx, guildContext, "guild_settings.update", model.AuditResultFailure, ErrGuildSettingsNotFound.Error())
		return nil, ErrGuildSettingsNotFound
	}
	if err := applyGuildSettingsInput(settings, input); err != nil {
		_ = s.audit(ctx, guildContext, "guild_settings.update", model.AuditResultFailure, err.Error())
		return nil, err
	}

	if input.AppealQueueChannelDiscordID != nil && settings.AppealQueueChannelDiscordID != "" {
		if s.channels == nil {
			return nil, fmt.Errorf("%w: channel validation unavailable", ErrGuildSettingsValidation)
		}
		if err := s.channels.ValidateStaffChannel(ctx, guildContext.Guild.DiscordGuildID, settings.AppealQueueChannelDiscordID); err != nil {
			return nil, fmt.Errorf("%w: appeal queue channel must be private and belong to this guild", ErrGuildSettingsValidation)
		}
	}
	if input.AuditMirrorChannelDiscordID != nil && settings.AuditMirrorChannelDiscordID != "" {
		if s.channels == nil {
			return nil, fmt.Errorf("%w: channel validation unavailable", ErrGuildSettingsValidation)
		}
		if err := s.channels.ValidateStaffChannel(ctx, guildContext.Guild.DiscordGuildID, settings.AuditMirrorChannelDiscordID); err != nil {
			return nil, fmt.Errorf("%w: audit channel must be private and belong to this guild", ErrGuildSettingsValidation)
		}
	}
	updated, err := s.store.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{
		Settings: *settings,
		Audit:    s.auditEntry(ctx, guildContext, "guild_settings.update", model.AuditResultSuccess, ""),
	})
	if err != nil {
		_ = s.audit(ctx, guildContext, "guild_settings.update", model.AuditResultFailure, err.Error())
		return nil, err
	}
	response := guildSettingsResponse(*updated)
	return &response, nil
}

// RejectUpdatePayload audits a transport-level settings write rejection while preserving authorization precedence.
func (s *GuildSettingsService) RejectUpdatePayload(ctx context.Context, guildContext *GuildStaffContext, payloadErr error) error {
	ctx = ensureTraceContext(ctx)
	if s == nil || s.store == nil || guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return errors.New("guild settings service is not configured")
	}
	if !guildContext.Can(model.PermissionActionGuildSettingsWrite) {
		_ = s.audit(ctx, guildContext, "guild_settings.update", model.AuditResultDenied, ErrGuildSettingsPermissionDenied.Error())
		return ErrGuildSettingsPermissionDenied
	}
	reason := "invalid guild settings payload"
	if payloadErr != nil {
		reason = payloadErr.Error()
	}
	err := fmt.Errorf("%w: %s", ErrGuildSettingsValidation, reason)
	_ = s.audit(ctx, guildContext, "guild_settings.update", model.AuditResultFailure, err.Error())
	return err
}

// AcknowledgeStarterPolicyNotice explicitly completes the one-time review notice without changing starter-template availability.
func (s *GuildSettingsService) AcknowledgeStarterPolicyNotice(ctx context.Context, guildContext *GuildStaffContext) (*GuildSettingsResponse, error) {
	ctx = ensureTraceContext(ctx)
	if s == nil || s.store == nil || guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, errors.New("guild settings service is not configured")
	}
	if !guildContext.Can(model.PermissionActionGuildSettingsWrite) {
		_ = s.audit(ctx, guildContext, "guild_settings.starter_policy_notice.acknowledge", model.AuditResultDenied, ErrGuildSettingsPermissionDenied.Error())
		return nil, ErrGuildSettingsPermissionDenied
	}
	settings, err := s.store.GetGuildSettings(ctx, guildContext.Guild.ID)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		return nil, ErrGuildSettingsNotFound
	}
	if settings.StarterPolicyNoticePending {
		now := time.Now().UTC()
		settings.StarterPolicyNoticePending = false
		settings.StarterPolicyNoticeAcknowledgedAt = &now
	}
	updated, err := s.store.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{
		Settings: *settings,
		Audit:    s.auditEntry(ctx, guildContext, "guild_settings.starter_policy_notice.acknowledge", model.AuditResultSuccess, ""),
	})
	if err != nil {
		_ = s.audit(ctx, guildContext, "guild_settings.starter_policy_notice.acknowledge", model.AuditResultFailure, err.Error())
		return nil, err
	}
	response := guildSettingsResponse(*updated)
	return &response, nil
}

// applyGuildSettingsInput normalizes transport values before they can reach durable storage.
func applyGuildSettingsInput(settings *model.GuildSettings, input GuildSettingsInput) error {
	if settings == nil {
		return fmt.Errorf("%w: settings are required", ErrGuildSettingsValidation)
	}
	if input.AppealQueueChannelDiscordID != nil {
		value, err := normalizeDiscordChannelReference(*input.AppealQueueChannelDiscordID)
		if err != nil {
			return err
		}
		settings.AppealQueueChannelDiscordID = value
	}
	if input.AuditMirrorChannelDiscordID != nil {
		value, err := normalizeDiscordChannelReference(*input.AuditMirrorChannelDiscordID)
		if err != nil {
			return err
		}
		settings.AuditMirrorChannelDiscordID = value
	}
	if input.ManagedEvidenceChannelDiscordID != nil {
		return fmt.Errorf("%w: managed evidence channel is maintained by Quack", ErrGuildSettingsValidation)
	}

	if input.NotificationIntroduction != nil {
		value := strings.TrimSpace(*input.NotificationIntroduction)
		if len(value) > maxGuildNotificationBrandingLength {
			return fmt.Errorf("%w: notification introduction exceeds %d characters", ErrGuildSettingsValidation, maxGuildNotificationBrandingLength)
		}
		settings.NotificationIntroduction = value
	}
	if input.NotificationFooter != nil {
		value := strings.TrimSpace(*input.NotificationFooter)
		if len(value) > maxGuildNotificationBrandingLength {
			return fmt.Errorf("%w: notification footer exceeds %d characters", ErrGuildSettingsValidation, maxGuildNotificationBrandingLength)
		}
		settings.NotificationFooter = value
	}
	if input.TicketsEnabled != nil {
		settings.TicketsEnabled = *input.TicketsEnabled
	}
	if input.GeneralLoggingEnabled != nil {
		settings.GeneralLoggingEnabled = *input.GeneralLoggingEnabled
	}
	if input.HoneypotEnabled != nil {
		settings.HoneypotEnabled = *input.HoneypotEnabled
	}
	return nil
}

// normalizeDiscordChannelReference accepts an empty clear operation or a decimal Discord snowflake.
func normalizeDiscordChannelReference(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if len(value) > 20 {
		return "", fmt.Errorf("%w: Discord channel reference exceeds 20 digits", ErrGuildSettingsValidation)
	}
	snowflake, err := strconv.ParseUint(value, 10, 64)
	if err != nil || snowflake == 0 || strconv.FormatUint(snowflake, 10) != value {
		return "", fmt.Errorf("%w: Discord channel reference must be a decimal snowflake", ErrGuildSettingsValidation)
	}
	return value, nil
}

// audit appends immutable settings evidence when validation or authorization prevents the atomic write path.
func (s *GuildSettingsService) audit(ctx context.Context, guildContext *GuildStaffContext, action string, result model.AuditResult, failureReason string) error {
	entry := s.auditEntry(ctx, guildContext, action, result, failureReason)
	if entry == nil {
		return nil
	}
	return recordAudit(ctx, s.store, entry)
}

// auditEntry constructs a settings audit row with request tracing and current Discord permission evidence.
func (s *GuildSettingsService) auditEntry(ctx context.Context, guildContext *GuildStaffContext, action string, result model.AuditResult, failureReason string) *model.AuditLogEntry {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil
	}
	requestID, correlationID := TraceIDsFromContext(ctx)
	return &model.AuditLogEntry{
		GuildID: guildContext.Guild.ID, ActorDiscordUserID: guildContext.Staff.DiscordUserID,
		ActorPermissionBits: guildContext.PermissionBits, Source: AuditSourceFromContext(ctx),
		Action: action, ResourceType: "guild_settings", Result: result, FailureReason: failureReason,
		RequestID: requestID, CorrelationID: correlationID, MetadataJSON: "{}",
	}
}

// guildSettingsResponse maps durable state into the dashboard contract without exposing storage details.
func guildSettingsResponse(settings model.GuildSettings) GuildSettingsResponse {
	return GuildSettingsResponse{
		ID: settings.ID, GuildID: settings.GuildID,
		AppealQueueChannelDiscordID:     settings.AppealQueueChannelDiscordID,
		AuditMirrorChannelDiscordID:     settings.AuditMirrorChannelDiscordID,
		ManagedEvidenceChannelDiscordID: settings.ManagedEvidenceChannelDiscordID,
		NotificationIntroduction:        settings.NotificationIntroduction, NotificationFooter: settings.NotificationFooter,
		TicketsEnabled: settings.TicketsEnabled, GeneralLoggingEnabled: settings.GeneralLoggingEnabled,
		HoneypotEnabled: settings.HoneypotEnabled, StarterPolicyTemplateID: settings.StarterPolicyTemplateID,
		StarterPolicyReviewRequired:       settings.StarterPolicyNoticePending,
		StarterPolicyNoticeAcknowledgedAt: settings.StarterPolicyNoticeAcknowledgedAt,
	}
}
