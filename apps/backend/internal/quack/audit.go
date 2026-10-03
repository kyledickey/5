package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
)

type AuditRepository interface {
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
	ListAuditLogEntriesFiltered(context.Context, model.ListAuditLogEntriesParams) (*model.ListAuditLogEntriesResult, error)
}

var (
	// ErrAuditValidation reports an unparseable or contradictory audit filter.
	ErrAuditValidation = errors.New("audit validation failed")
	// ErrAuditPermissionDenied reports that the caller lacks audit.read authority.
	ErrAuditPermissionDenied = errors.New("audit permission denied")
)

// AuditService provides authorized, filtered access to immutable moderation audit entries.
type AuditService struct {
	store AuditRepository
}

// AuditListInput carries the raw query filters for AuditService.List. Values
// are strings straight from the transport; the service trims and validates
// them. ReadSource records which transport performed the read for the read's
// own audit attribution and defaults to the API source.
type AuditListInput struct {
	Limit               string
	Offset              string
	ActorDiscordUserID  string
	Action              string
	ResourceType        string
	ResourceID          string
	Result              string
	Source              string
	CaseID              string
	MemberDiscordUserID string
	CreatedAfter        string
	CreatedBefore       string
	ReadSource          model.AuditSource
	BeforeID            string
}

// AuditListResponse is one page of audit entries. NextCursor is set when the
// page was full and can be passed back as BeforeID for keyset pagination.
type AuditListResponse struct {
	Entries    []AuditEntryResponse `json:"entries"`
	Total      int64                `json:"total"`
	Limit      int                  `json:"limit"`
	Offset     int                  `json:"offset"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// AuditEntryResponse is one audit row for transport, with the stored metadata
// JSON decoded into a generic value and permission bits rendered as a string.
type AuditEntryResponse struct {
	ID                  string            `json:"id"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	GuildID             string            `json:"guild_id"`
	ActorDiscordUserID  string            `json:"actor_discord_user_id,omitempty"`
	ActorPermissionBits string            `json:"actor_permission_bits"`
	Source              model.AuditSource `json:"source"`
	Action              string            `json:"action"`
	ResourceType        string            `json:"resource_type"`
	ResourceID          string            `json:"resource_id"`
	Result              model.AuditResult `json:"result"`
	FailureReason       string            `json:"failure_reason,omitempty"`
	CorrelationID       string            `json:"correlation_id,omitempty"`
	RequestID           string            `json:"request_id,omitempty"`
	Metadata            any               `json:"metadata"`
}

func NewAuditService(store AuditRepository) *AuditService {
	return &AuditService{store: store}
}

// List returns filtered audit entries for staff with audit.read. Every filter is
// validated before the query runs; limit is capped at 100 and offset cannot be
// combined with a BeforeID cursor. Case and member filters must be exact ids.
func (s *AuditService) List(ctx context.Context, guildContext *GuildStaffContext, input AuditListInput) (*AuditListResponse, error) {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, validationAuditError("missing guild context")
	}
	if !guildContext.Can(model.PermissionActionAuditRead) {
		_ = s.recordRead(ctx, guildContext, input, model.AuditResultDenied, "permission_denied") // best-effort: denial already returned
		return nil, ErrAuditPermissionDenied
	}

	limit, offset, err := auditPagination(input.Limit, input.Offset)
	if err != nil {
		return nil, err
	}
	beforeID := strings.TrimSpace(input.BeforeID)
	if beforeID != "" && len(beforeID) != 26 {
		return nil, validationAuditError("before_id must be an audit entry ID")
	}
	if beforeID != "" && offset != 0 {
		return nil, validationAuditError("offset and before_id cannot be combined")
	}
	resultValue := model.AuditResult(strings.TrimSpace(input.Result))
	if resultValue != "" && !validAuditResult(resultValue) {
		return nil, validationAuditError("result is invalid")
	}
	source := model.AuditSource(strings.TrimSpace(input.Source))
	if source != "" && !validAuditSource(source) {
		return nil, validationAuditError("source is invalid")
	}
	createdAfter, err := normalizeAuditTime(input.CreatedAfter)
	if err != nil {
		return nil, err
	}
	createdBefore, err := normalizeAuditTime(input.CreatedBefore)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(input.CaseID, `%_\\`) || strings.ContainsAny(input.MemberDiscordUserID, `%_\\`) {
		return nil, validationAuditError("case and member filters must be exact identifiers")
	}

	result, err := s.store.ListAuditLogEntriesFiltered(ctx, model.ListAuditLogEntriesParams{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  strings.TrimSpace(input.ActorDiscordUserID),
		Source:              string(source),
		Action:              strings.TrimSpace(input.Action),
		ResourceType:        strings.TrimSpace(input.ResourceType),
		ResourceID:          strings.TrimSpace(input.ResourceID),
		Result:              resultValue,
		CaseID:              strings.TrimSpace(input.CaseID),
		MemberDiscordUserID: strings.TrimSpace(input.MemberDiscordUserID),
		CreatedAfter:        createdAfter,
		CreatedBefore:       createdBefore,
		BeforeID:            beforeID,
		Limit:               limit,
		Offset:              offset,
	})
	if err != nil {
		_ = s.recordRead(ctx, guildContext, input, model.AuditResultFailure, "query_failed") // best-effort: storage error already returned
		return nil, err
	}

	entries := make([]AuditEntryResponse, 0, len(result.Entries))
	for _, entry := range result.Entries {
		entries = append(entries, AuditEntryResponse{
			ID:                  entry.ID,
			CreatedAt:           entry.CreatedAt,
			UpdatedAt:           entry.UpdatedAt,
			GuildID:             entry.GuildID,
			ActorDiscordUserID:  entry.ActorDiscordUserID,
			ActorPermissionBits: PermissionBitsString(entry.ActorPermissionBits),
			Source:              entry.Source,
			Action:              entry.Action,
			ResourceType:        entry.ResourceType,
			ResourceID:          entry.ResourceID,
			Result:              entry.Result,
			FailureReason:       entry.FailureReason,
			CorrelationID:       entry.CorrelationID,
			RequestID:           entry.RequestID,
			Metadata:            parseJSON(entry.MetadataJSON),
		})
	}

	nextCursor := ""
	if len(entries) == limit {
		nextCursor = entries[len(entries)-1].ID
	}
	response := &AuditListResponse{
		Entries:    entries,
		Total:      result.Total,
		Limit:      limit,
		Offset:     offset,
		NextCursor: nextCursor,
	}
	if err := s.recordRead(ctx, guildContext, input, model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return response, nil
}

// recordRead passes the read outcome through recordAudit, which logs it unless
// audit.read is an important audit action. Metadata records only which filters
// were used, never the filter values or the results.
func (s *AuditService) recordRead(
	ctx context.Context, guildContext *GuildStaffContext, input AuditListInput, result model.AuditResult, failure string,
) error {
	if guildContext == nil || guildContext.Guild == nil {
		return nil
	}
	actorID := ""
	permissionBits := uint64(0)
	if guildContext.Staff != nil {
		actorID = guildContext.Staff.DiscordUserID
		permissionBits = guildContext.PermissionBits
	}
	requestID, correlationID := idutil.TraceIDsFromContext(ctx)
	metadata, _ := json.Marshal(map[string]any{
		"actor_filter":    strings.TrimSpace(input.ActorDiscordUserID) != "",
		"source_filter":   strings.TrimSpace(input.Source) != "",
		"action_filter":   strings.TrimSpace(input.Action) != "",
		"resource_filter": strings.TrimSpace(input.ResourceType) != "" || strings.TrimSpace(input.ResourceID) != "",
		"case_filter":     strings.TrimSpace(input.CaseID) != "",
		"member_filter":   strings.TrimSpace(input.MemberDiscordUserID) != "",
		"date_filter":     strings.TrimSpace(input.CreatedAfter) != "" || strings.TrimSpace(input.CreatedBefore) != "",
	})
	readSource := input.ReadSource
	if !validAuditSource(readSource) || readSource == "" {
		readSource = model.AuditSourceAPI
	}
	return recordAudit(ctx, s.store, &model.AuditLogEntry{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  actorID,
		ActorPermissionBits: permissionBits,
		Source:              readSource,
		Action:              string(model.AuditActionAuditRead),
		ResourceType:        "audit_log",
		ResourceID:          "list",
		Result:              result,
		FailureReason:       failure,
		RequestID:           requestID,
		CorrelationID:       correlationID,
		MetadataJSON:        string(metadata),
	})
}

// normalizeAuditTime parses an RFC3339 filter bound and returns it as UTC
// RFC3339Nano for the store; empty input stays empty.
func normalizeAuditTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", validationAuditError("date filter must use RFC3339")
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func validAuditSource(source model.AuditSource) bool {
	switch source {
	case model.AuditSourceAPI, model.AuditSourceWeb, model.AuditSourceDiscord,
		model.AuditSourceSystem, model.AuditSourceImport, model.AuditSourceHoneypot:
		return true
	default:
		return false
	}
}

// auditPagination parses limit (default 50, capped at 100) and offset (default 0).
func auditPagination(limitValue, offsetValue string) (int, int, error) {
	limit := 50
	if strings.TrimSpace(limitValue) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(limitValue))
		if err != nil || parsed <= 0 {
			return 0, 0, validationAuditError("limit must be a positive integer")
		}
		limit = parsed
	}
	if limit > 100 {
		limit = 100
	}

	offset := 0
	if strings.TrimSpace(offsetValue) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(offsetValue))
		if err != nil || parsed < 0 {
			return 0, 0, validationAuditError("offset must be a non-negative integer")
		}
		offset = parsed
	}

	return limit, offset, nil
}

func validAuditResult(result model.AuditResult) bool {
	switch result {
	case model.AuditResultSuccess, model.AuditResultFailure, model.AuditResultDenied:
		return true
	default:
		return false
	}
}

func validationAuditError(message string) error {
	return fmt.Errorf("%w: %s", ErrAuditValidation, message)
}

type auditSourceContextKey struct{}

// ContextWithAuditSource carries the authoritative adapter source across shared business services.
func ContextWithAuditSource(ctx context.Context, source model.AuditSource) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, auditSourceContextKey{}, source)
}

// AuditSourceForModuleAction classifies automated/import module events while
// preserving the caller transport source for staff-driven module operations.
func AuditSourceForModuleAction(ctx context.Context, action string) model.AuditSource {
	action = strings.ToLower(strings.TrimSpace(action))
	if strings.Contains(action, "v4_import") || strings.Contains(action, "v4_settings_import") {
		return model.AuditSourceImport
	}
	if strings.HasPrefix(action, "honeypot.trigger.") || action == "honeypot.case.created" || action == "honeypot.configuration.disabled" {
		return model.AuditSourceHoneypot
	}
	return AuditSourceFromContext(ctx)
}

// AuditSourceFromContext returns the adapter source, defaulting to the backend API boundary.
func AuditSourceFromContext(ctx context.Context) model.AuditSource {
	if ctx != nil {
		if source, ok := ctx.Value(auditSourceContextKey{}).(model.AuditSource); ok && validAuditSource(source) {
			return source
		}
	}
	return model.AuditSourceAPI
}

// AuditSourceForCaseSource maps durable case origin to immutable audit origin.
func AuditSourceForCaseSource(source model.CaseSource) model.AuditSource {
	switch source {
	case model.CaseSourceDiscord:
		return model.AuditSourceDiscord
	case model.CaseSourceHoneypot:
		return model.AuditSourceHoneypot
	case model.CaseSourceV4Import:
		return model.AuditSourceImport
	default:
		return model.AuditSourceWeb
	}
}

type auditWriter interface {
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
}

// recordAudit persists entry when its action is an important audit event and
// otherwise only logs it at debug level, so routine reads never enter staff
// history. A nil entry is a no-op. Storage errors are returned to the caller
// and logged with identifiers only: never the entry body, actor input, or raw
// driver error.
func recordAudit(ctx context.Context, writer auditWriter, entry *model.AuditLogEntry) error {
	if entry == nil {
		return nil
	}
	if !model.IsAuditEvent(entry.Action) {
		slog.DebugContext(ctx, "Service operation", "guild_id", entry.GuildID, "action", entry.Action, "result", entry.Result)
		return nil
	}
	err := writer.CreateAuditLogEntry(ctx, entry)
	if err != nil {
		slog.Default().
			With("guild_id", entry.GuildID, "action", entry.Action, "resource_id", entry.ResourceID).
			ErrorContext(ctx, "Audit entry could not be recorded")
	}
	return err
}

var (
	// ErrStatisticsValidation reports a malformed or oversized time window.
	ErrStatisticsValidation = errors.New("statistics validation failed")
	// ErrStatisticsPermissionDenied reports that the caller lacks audit.read authority.
	ErrStatisticsPermissionDenied = errors.New("statistics permission denied")
)

// StatisticsInput is the requested window as RFC3339 strings: From is
// inclusive, To exclusive. Either may be empty; see statisticsRange.
type StatisticsInput struct {
	From string
	To   string
}

type StatisticsRepository interface {
	DeriveStaffStatistics(context.Context, model.StaffStatisticsParams) (*model.StaffStatistics, error)
}

// StaffStatisticsService derives guild-scoped operational counts without persisting aggregates or rankings.
type StaffStatisticsService struct {
	store StatisticsRepository
}

func NewStaffStatisticsService(store StatisticsRepository) *StaffStatisticsService {
	return &StaffStatisticsService{store: store}
}

// Get returns a guild-scoped derived snapshot for an authorized moderator.
func (s *StaffStatisticsService) Get(
	ctx context.Context, guildContext *GuildStaffContext, input StatisticsInput,
) (*model.StaffStatistics, error) {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(model.PermissionActionAuditRead) {
		return nil, ErrStatisticsPermissionDenied
	}
	from, to, err := statisticsRange(input, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return s.store.DeriveStaffStatistics(ctx, model.StaffStatisticsParams{GuildID: guildContext.Guild.ID, From: from, To: to})
}

// statisticsRange normalizes the requested window to UTC and bounds expensive
// history queries to one year. Omitted bounds select the preceding month.
func statisticsRange(input StatisticsInput, now time.Time) (time.Time, time.Time, error) {
	to := now.UTC()
	var from time.Time
	var err error
	if strings.TrimSpace(input.To) != "" {
		to, err = time.Parse(time.RFC3339, strings.TrimSpace(input.To))
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: to must use RFC3339", ErrStatisticsValidation)
		}
	}
	from = to.AddDate(0, -1, 0)
	if strings.TrimSpace(input.From) != "" {
		from, err = time.Parse(time.RFC3339, strings.TrimSpace(input.From))
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: from must use RFC3339", ErrStatisticsValidation)
		}
	}
	from, to = from.UTC(), to.UTC()
	if !from.Before(to) || to.Sub(from) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: range must be positive and at most 366 days", ErrStatisticsValidation)
	}
	return from, to, nil
}
