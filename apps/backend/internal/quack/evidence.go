package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// EvidenceRepository is the persistence EvidenceService needs to find and
// atomically record the managed evidence channel.
type EvidenceRepository interface {
	GetGuildByDiscordID(context.Context, string) (*model.Guild, error)
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	CompareAndSetEvidenceChannel(context.Context, string, string, string) (string, error)
}

const (
	maxEvidenceContentRunes     = 4000
	maxEvidenceEmbeds           = 10
	maxEvidenceAttachments      = 10
	maxEvidenceMessages         = 10
	maxEvidenceTotalAttachments = 20
	// MaxPreservedAttachmentBytes bounds both advertised sizes and actual downloads.
	MaxPreservedAttachmentBytes int64 = 25 << 20
)

var discordMessageLinkPattern = regexp.MustCompile(`^/channels/([0-9]{2,32})/([0-9]{2,32})/([0-9]{2,32})$`)

// ErrEvidenceValidation marks evidence that cannot safely be associated with the requested guild and target.
var ErrEvidenceValidation = errors.New("evidence validation failed")

type DiscordMessageReference struct {
	GuildID, ChannelID, MessageID, URL string
	ActorDiscordUserID                 string
	// SystemCapture is set only by trusted system case creation, never by request input.
	SystemCapture bool
}

type DiscordAttachmentSnapshot struct {
	ID, Filename, ContentType, URL string
	SizeBytes                      int64
}

// DiscordMessageSnapshot is bounded live message data returned before the case transaction begins.
type DiscordMessageSnapshot struct {
	GuildID, ChannelID, MessageID, AuthorDiscordUserID, URL, Content string
	CreatedAt                                                        time.Time
	EditedAt                                                         *time.Time
	Embeds                                                           []map[string]any
	Attachments                                                      []DiscordAttachmentSnapshot
}

type PreservedDiscordAttachment struct{ URL, MessageID, AttachmentID string }

// DiscordEvidenceClient is the Discord access the evidence service needs: a
// live message read, a copy of one attachment into the managed evidence
// channel, and creation of that channel when it is missing.
type DiscordEvidenceClient interface {
	FetchMessageEvidence(context.Context, DiscordMessageReference) (*DiscordMessageSnapshot, error)
	PreserveEvidenceAttachment(context.Context, string, string, DiscordAttachmentSnapshot) (*PreservedDiscordAttachment, error)
	EnsureEvidenceChannel(context.Context, string, string) (string, error)
}

// EvidenceUnavailableError classifies a link that was valid but could not be
// captured. Outcome is the stored capture outcome (for example "deleted" or
// "inaccessible"); Message is the staff-facing warning.
type EvidenceUnavailableError struct{ Outcome, Message string }

// Error returns the staff-facing message, falling back to the outcome code.
func (e *EvidenceUnavailableError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Outcome
}

// CapturedEvidence is the set of snapshot and attachment rows staged for one
// case transaction plus the warnings explaining anything that was not captured.
type CapturedEvidence struct {
	Snapshots   []model.CaseEvidenceSnapshot
	Attachments []model.CaseEvidenceAttachment
	Warnings    []string
}

// EvidenceService captures Discord message links and staff uploads as
// immutable evidence rows before a case commits, and repairs the guild's
// managed evidence channel when attachments need a home. Both collaborators
// are optional: without a client every link is recorded as unavailable and
// attachments keep metadata only; without a store no channel repair happens.
type EvidenceService struct {
	client       DiscordEvidenceClient
	store        EvidenceRepository
	storageLocks evidenceStorageLocks
}

func NewEvidenceService(client DiscordEvidenceClient, store EvidenceRepository) *EvidenceService {
	return &EvidenceService{client: client, store: store}
}

// EnsureGuildEvidenceChannel creates missing storage and saves only its receipt.
// The lifecycle snapshot is intentionally ignored: settings are reloaded after
// acquiring the guild creation lock so overlapping repairs use the winning ID.
// It fails when the service has no Discord client or store to work with.
func (s *EvidenceService) EnsureGuildEvidenceChannel(ctx context.Context, guild model.Guild, _ model.GuildSettings) (string, error) {
	if s.client == nil || s.store == nil {
		return "", errors.New("evidence channel repair requires a discord client and a store")
	}
	release, err := s.storageLocks.acquire(ctx, guild.ID)
	if err != nil {
		return "", err
	}
	defer release()
	current, err := s.store.GetGuildSettings(ctx, guild.ID)
	if err != nil {
		return "", err
	}
	if current == nil {
		return "", errors.New("evidence settings unavailable")
	}
	channelID, err := s.client.EnsureEvidenceChannel(ctx, guild.DiscordGuildID, current.ManagedEvidenceChannelDiscordID)
	if err != nil {
		return "", err
	}
	return s.store.CompareAndSetEvidenceChannel(ctx, guild.ID, current.ManagedEvidenceChannelDiscordID, channelID)
}

// RepairDiscordGuildEvidenceChannel reloads durable channel state and recreates
// storage after Discord deletes the channel. An unknown guild or missing store
// returns ("", nil) so gateway callers treat it as nothing to repair.
func (s *EvidenceService) RepairDiscordGuildEvidenceChannel(ctx context.Context, discordGuildID string) (string, error) {
	if s.store == nil {
		return "", nil
	}
	guild, err := s.store.GetGuildByDiscordID(ctx, discordGuildID)
	if err != nil || guild == nil {
		return "", err
	}
	settings, err := s.store.GetGuildSettings(ctx, guild.ID)
	if err != nil || settings == nil {
		return "", err
	}
	return s.EnsureGuildEvidenceChannel(ctx, *guild, *settings)
}

// ParseDiscordMessageLink validates a canonical Discord message URL without accepting cross-origin lookalikes.
func ParseDiscordMessageLink(raw string) (DiscordMessageReference, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || !discordMessageHost(parsed.Host) {
		return DiscordMessageReference{}, fmt.Errorf("%w: invalid Discord message link", ErrEvidenceValidation)
	}
	match := discordMessageLinkPattern.FindStringSubmatch(parsed.EscapedPath())
	if len(match) != 4 {
		return DiscordMessageReference{}, fmt.Errorf("%w: invalid Discord message link path", ErrEvidenceValidation)
	}
	return DiscordMessageReference{GuildID: match[1], ChannelID: match[2], MessageID: match[3], URL: parsed.String()}, nil
}

func discordMessageHost(host string) bool {
	switch host {
	case "discord.com", "www.discord.com", "ptb.discord.com", "canary.discord.com":
		return true
	default:
		return false
	}
}

// Capture snapshots each unique message before case commit and preserves supported attachments when possible.
func (s *EvidenceService) Capture(
	ctx context.Context,
	guildID, actorDiscordUserID, targetDiscordUserID, evidenceChannelID string,
	links []string,
) (*CapturedEvidence, error) {
	if actorDiscordUserID == "" && len(links) > 0 {
		return nil, fmt.Errorf("%w: evidence actor is required", ErrEvidenceValidation)
	}
	return s.capture(ctx, guildID, actorDiscordUserID, targetDiscordUserID, evidenceChannelID, links)
}

// capture permits an empty actor only for the trusted system case path. Links
// are deduplicated by message ID; a message whose author is not the case target
// or whose identity Discord reports differently is a validation error, while a
// message Discord cannot return becomes an "unavailable" snapshot with a warning.
func (s *EvidenceService) capture(
	ctx context.Context,
	guildID, actorDiscordUserID, targetDiscordUserID, evidenceChannelID string,
	links []string,
) (*CapturedEvidence, error) {
	if len(links) > maxEvidenceMessages {
		return nil, fmt.Errorf("%w: at most %d message links can be captured", ErrEvidenceValidation, maxEvidenceMessages)
	}
	result := &CapturedEvidence{}
	storageChecked := false
	seen := map[string]struct{}{}
	totalAttachments := 0
	for _, raw := range links {
		ref, err := ParseDiscordMessageLink(raw)
		if err != nil {
			return nil, err
		}
		if ref.GuildID != guildID {
			return nil, fmt.Errorf("%w: message belongs to another guild", ErrEvidenceValidation)
		}
		if _, ok := seen[ref.MessageID]; ok {
			continue
		}
		seen[ref.MessageID] = struct{}{}
		if s.client == nil {
			return nil, fmt.Errorf("%w: Discord evidence capture is unavailable", ErrEvidenceValidation)
		}
		ref.ActorDiscordUserID = actorDiscordUserID
		ref.SystemCapture = actorDiscordUserID == ""
		message, err := s.client.FetchMessageEvidence(ctx, ref)
		if err != nil {
			var unavailable *EvidenceUnavailableError
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// best-effort: an unclassified failure keeps the generic "unavailable" warning
			_ = errors.As(err, &unavailable)
			warning := "Message could not be saved. You can add evidence later."
			outcome := "unavailable"
			if unavailable != nil {
				outcome = unavailable.Outcome
				warning = unavailable.Error()
			}
			id := idutil.NewULID()
			result.Snapshots = append(result.Snapshots, model.CaseEvidenceSnapshot{
				ULIDModel:        model.ULIDModel{ID: id},
				GuildID:          guildID,
				ChannelDiscordID: ref.ChannelID,
				MessageDiscordID: ref.MessageID,
				MessageURL:       ref.URL,
				MessageCreatedAt: time.Now().UTC(),
				CaptureOutcome:   outcome,
				CaptureWarning:   warning,
				EmbedsJSON:       "[]",
			})
			result.Warnings = append(result.Warnings, warning)
			continue
		}
		if message == nil || message.GuildID != guildID || message.MessageID != ref.MessageID || message.ChannelID != ref.ChannelID {
			return nil, fmt.Errorf("%w: Discord returned mismatched message identity", ErrEvidenceValidation)
		}
		if strings.TrimSpace(targetDiscordUserID) != "" && message.AuthorDiscordUserID != targetDiscordUserID {
			return nil, fmt.Errorf("%w: captured message author does not match case target", ErrEvidenceValidation)
		}
		if len(message.Attachments) > 0 && !storageChecked {
			evidenceChannelID = s.captureStorage(ctx, guildID, evidenceChannelID)
			storageChecked = true
		}
		content := truncateRunes(message.Content, maxEvidenceContentRunes)
		snapshotWarnings := []string{}
		if len([]rune(message.Content)) > maxEvidenceContentRunes {
			result.Warnings = append(result.Warnings, "message content snapshot was truncated")
			snapshotWarnings = append(snapshotWarnings, "message content snapshot was truncated")
		}
		embeds := message.Embeds
		if len(embeds) > maxEvidenceEmbeds {
			embeds = embeds[:maxEvidenceEmbeds]
			result.Warnings = append(result.Warnings, "embed snapshot was truncated")
			snapshotWarnings = append(snapshotWarnings, "embed snapshot was truncated")
		}
		// best-effort: embeds are plain decoded JSON from Discord, so encoding cannot fail in practice
		embedJSON, _ := json.Marshal(embeds)
		evidenceID := idutil.NewULID()
		snapshot := model.CaseEvidenceSnapshot{
			ULIDModel:           model.ULIDModel{ID: evidenceID},
			GuildID:             guildID,
			ChannelDiscordID:    message.ChannelID,
			MessageDiscordID:    message.MessageID,
			AuthorDiscordUserID: message.AuthorDiscordUserID,
			MessageURL:          message.URL,
			Content:             content,
			MessageCreatedAt:    message.CreatedAt,
			MessageEditedAt:     message.EditedAt,
			EmbedsJSON:          string(embedJSON),
			CaptureOutcome:      "captured",
		}
		evidenceIndex := len(result.Snapshots)
		result.Snapshots = append(result.Snapshots, snapshot)
		attachments := message.Attachments
		if len(attachments) > maxEvidenceAttachments {
			attachments = attachments[:maxEvidenceAttachments]
			result.Warnings = append(result.Warnings, "attachment snapshot was truncated")
			snapshotWarnings = append(snapshotWarnings, "attachment snapshot was truncated")
		}
		remaining := maxEvidenceTotalAttachments - totalAttachments
		if remaining <= 0 {
			attachments = nil
			result.Warnings = append(result.Warnings, "total attachment snapshot limit reached")
			snapshotWarnings = append(snapshotWarnings, "total attachment snapshot limit reached")
		} else if len(attachments) > remaining {
			attachments = attachments[:remaining]
			result.Warnings = append(result.Warnings, "total attachment snapshot was truncated")
			snapshotWarnings = append(snapshotWarnings, "total attachment snapshot was truncated")
		}
		totalAttachments += len(attachments)
		for _, attachment := range attachments {
			record := s.preserveAttachment(ctx, guildID, evidenceChannelID, evidenceID, attachment)
			if record.Warning != "" {
				result.Warnings = append(result.Warnings, record.Warning)
				snapshotWarnings = append(snapshotWarnings, record.Warning)
			}
			result.Attachments = append(result.Attachments, record)
		}
		result.Snapshots[evidenceIndex].CaptureWarning = strings.Join(snapshotWarnings, "; ")
	}
	if len(result.Warnings) > 0 {
		slog.WarnContext(ctx, "Evidence capture incomplete",
			"discord_guild_id", guildID,
			"messages", len(result.Snapshots),
			"attachments", len(result.Attachments),
			"warnings", len(result.Warnings))
	}
	return result, nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

// supportedEvidenceContentType limits managed copies to bounded, displayable staff evidence.
func supportedEvidenceContentType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	return strings.HasPrefix(value, "image/") ||
		strings.HasPrefix(value, "video/") ||
		strings.HasPrefix(value, "audio/") ||
		value == "text/plain" ||
		value == "application/pdf"
}

// preserveAttachment records original metadata even when a managed copy fails.
// Both message capture and direct uploads share these size and type limits.
func (s *EvidenceService) preserveAttachment(
	ctx context.Context,
	guildID, evidenceChannelID, evidenceID string,
	attachment DiscordAttachmentSnapshot,
) model.CaseEvidenceAttachment {
	record := model.CaseEvidenceAttachment{
		EvidenceID:  evidenceID,
		Filename:    truncateRunes(attachment.Filename, 255),
		ContentType: truncateRunes(attachment.ContentType, 191),
		SizeBytes:   attachment.SizeBytes,
		OriginalURL: attachment.URL,
		CopyOutcome: "metadata_only",
	}
	if s.client == nil || evidenceChannelID == "" {
		record.Warning = "managed evidence channel is unavailable"
	} else if attachment.SizeBytes < 0 || attachment.SizeBytes > MaxPreservedAttachmentBytes {
		record.Warning = "attachment exceeds the managed copy size limit"
	} else if !supportedEvidenceContentType(attachment.ContentType) {
		record.Warning = "attachment type is not eligible for managed copying"
	} else if preserved, copyErr := s.client.PreserveEvidenceAttachment(ctx, guildID, evidenceChannelID, attachment); copyErr != nil {
		record.Warning = "attachment copy failed; original metadata retained"
	} else if preserved != nil && preserved.URL != "" && preserved.AttachmentID != "" && preserved.MessageID != "" {
		record.CopyOutcome = "preserved"
		record.PreservedURL = preserved.URL
		record.PreservedMessageDiscordID = preserved.MessageID
		record.PreservedAttachmentDiscordID = preserved.AttachmentID
	} else {
		record.Warning = "attachment copy could not be confirmed; original metadata retained"
	}
	return record
}

// captureStorage makes one bounded repair attempt per attachment batch. Failure
// leaves metadata-only evidence with the existing explicit warning, never a new
// requirement to preserve files before moderation can proceed. Without a store
// the caller's channel ID is returned unchanged.
func (s *EvidenceService) captureStorage(ctx context.Context, discordGuildID, fallback string) string {
	if s.store == nil {
		return fallback
	}
	repairCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	channelID, err := s.RepairDiscordGuildEvidenceChannel(repairCtx, discordGuildID)
	if err != nil {
		return ""
	}
	return channelID
}

// evidenceStorageLocks serializes lookup/create/receipt for each guild in this
// process. Idle entries are removed; uploads never hold this creation lock.
type evidenceStorageLocks struct {
	mu      sync.Mutex
	entries map[string]*evidenceStorageGate
}

// evidenceStorageGate counts callers so cancelled waiters cannot detach a gate
// still held by another repair.
type evidenceStorageGate struct {
	token      chan struct{}
	references int
}

// acquire returns a cancellation-aware release function for one guild repair.
func (l *evidenceStorageLocks) acquire(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[string]*evidenceStorageGate)
	}
	gate := l.entries[key]
	if gate == nil {
		gate = &evidenceStorageGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		l.entries[key] = gate
	}
	gate.references++
	l.mu.Unlock()
	drop := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		gate.references--
		if gate.references == 0 {
			delete(l.entries, key)
		}
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-gate.token:
		if err := ctx.Err(); err != nil {
			gate.token <- struct{}{}
			drop()
			return nil, err
		}
		return func() { gate.token <- struct{}{}; drop() }, nil
	}
}

// CaptureUploads preserves files supplied by staff, without pretending they are
// messages authored by the case target. Failure leaves a visible metadata record.
// Only the first ten files are kept; the first snapshot carries a warning when
// more were supplied.
func (s *EvidenceService) CaptureUploads(
	ctx context.Context,
	guildID, actorID, channelID string,
	files []DiscordAttachmentSnapshot,
) (*CapturedEvidence, error) {
	result := &CapturedEvidence{}
	truncated := len(files) > maxEvidenceAttachments
	if truncated {
		files = files[:maxEvidenceAttachments]
	}
	if len(files) > 0 {
		channelID = s.captureStorage(ctx, guildID, channelID)
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := idutil.NewULID()
		record := s.preserveAttachment(ctx, guildID, channelID, id, file)
		snapshot := model.CaseEvidenceSnapshot{
			ULIDModel:           model.ULIDModel{ID: id},
			GuildID:             guildID,
			AuthorDiscordUserID: actorID,
			MessageDiscordID:    file.ID,
			MessageCreatedAt:    time.Now().UTC(),
			EmbedsJSON:          "[]",
			CaptureOutcome:      "uploaded",
			CaptureWarning:      record.Warning,
		}
		result.Snapshots = append(result.Snapshots, snapshot)
		result.Attachments = append(result.Attachments, record)
		if record.Warning != "" {
			result.Warnings = append(result.Warnings, record.Warning)
		}
	}
	if truncated && len(result.Snapshots) > 0 {
		warning := "Only the first ten files were saved. Add the remaining files separately."
		result.Snapshots[0].CaptureWarning = strings.TrimSpace(result.Snapshots[0].CaptureWarning + " " + warning)
		result.Warnings = append(result.Warnings, warning)
	}
	return result, nil
}

// AddEvidence attaches files and message snapshots to an existing case. It never
// selects another level or schedules enforcement, including for a voided case.
// Requires the case create permission; caseRef may be a case ID or number.
func (s *CaseService) AddEvidence(
	ctx context.Context,
	guild *GuildStaffContext,
	caseRef string,
	links []string,
	files []DiscordAttachmentSnapshot,
) (*CaseDetailResponse, error) {
	if guild == nil || guild.Guild == nil || !guild.Can(model.PermissionActionCaseCreate) {
		return nil, ErrCasePermissionDenied
	}
	if len(links) == 0 && len(files) == 0 {
		return nil, validationCaseError("attach a file or provide a message link")
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guild.Guild.ID, strings.TrimSpace(caseRef))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	settings, err := s.store.GetGuildSettings(ctx, guild.Guild.ID)
	if err != nil {
		return nil, err
	}
	channelID := ""
	if settings != nil {
		channelID = settings.ManagedEvidenceChannelDiscordID
	}
	evidence := s.evidenceCapture()
	captured, err := evidence.Capture(ctx, guild.Guild.DiscordGuildID, guild.ActorDiscordUserID, item.TargetDiscordUserID, channelID, links)
	if err != nil {
		return nil, err
	}
	uploads, err := evidence.CaptureUploads(ctx, guild.Guild.DiscordGuildID, guild.ActorDiscordUserID, channelID, files)
	if err != nil {
		return nil, err
	}
	captured.Snapshots = append(captured.Snapshots, uploads.Snapshots...)
	captured.Attachments = append(captured.Attachments, uploads.Attachments...)
	audit := s.auditEntry(ctx, guild, string(model.AuditActionCaseUpdate), "case", item.ID, model.AuditResultSuccess, "")
	if err := s.store.AppendCaseEvidence(ctx, guild.Guild.ID, item.ID, captured.Snapshots, captured.Attachments, audit); err != nil {
		return nil, err
	}
	return s.Get(ctx, guild, item.ID)
}
