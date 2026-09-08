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
	"time"

	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
)

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

// DiscordMessageReference is the validated identity parsed from a Discord message URL.
type DiscordMessageReference struct {
	GuildID, ChannelID, MessageID, URL string
	ActorDiscordUserID                 string
	// SystemCapture is set only by trusted system case creation, never by request input.
	SystemCapture bool
}

// DiscordAttachmentSnapshot is transport-neutral metadata returned by the Discord evidence adapter.
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

// PreservedDiscordAttachment identifies a stable managed-channel copy.
type PreservedDiscordAttachment struct{ URL, MessageID, AttachmentID string }

// DiscordEvidenceClient defines live Discord reads and managed evidence-channel operations.
type DiscordEvidenceClient interface {
	FetchMessageEvidence(context.Context, DiscordMessageReference) (*DiscordMessageSnapshot, error)
	PreserveEvidenceAttachment(context.Context, string, string, DiscordAttachmentSnapshot) (*PreservedDiscordAttachment, error)
	EnsureEvidenceChannel(context.Context, string, string) (string, error)
}

// EvidenceUnavailableError classifies a link that was valid but could not be captured.
type EvidenceUnavailableError struct{ Outcome, Message string }

func (e *EvidenceUnavailableError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Outcome
}

// CapturedEvidence groups the immutable records staged for the atomic case transaction.
type CapturedEvidence struct {
	Snapshots   []model.CaseEvidenceSnapshot
	Attachments []model.CaseEvidenceAttachment
	Warnings    []string
}

// EvidenceService implements shared HTTP and Discord message-link capture.
type EvidenceService struct {
	client DiscordEvidenceClient
	store  EvidenceRepository
}

// NewEvidenceService constructs the shared capture boundary.
func NewEvidenceService(client DiscordEvidenceClient, stores ...EvidenceRepository) *EvidenceService {
	service := &EvidenceService{client: client}
	if len(stores) > 0 {
		service.store = stores[0]
	}
	return service
}

// EnsureGuildEvidenceChannel creates missing evidence storage and persists its current reference.
func (s *EvidenceService) EnsureGuildEvidenceChannel(ctx context.Context, guild model.Guild, settings model.GuildSettings) (string, error) {
	if s == nil || s.client == nil || s.store == nil {
		return "", errors.New("evidence service is not configured")
	}
	channelID, err := s.client.EnsureEvidenceChannel(ctx, guild.DiscordGuildID, settings.ManagedEvidenceChannelDiscordID)
	if err != nil {
		return "", err
	}
	if channelID == settings.ManagedEvidenceChannelDiscordID {
		return channelID, nil
	}
	settings.ManagedEvidenceChannelDiscordID = channelID
	_, err = s.store.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{Settings: settings, Audit: &model.AuditLogEntry{GuildID: guild.ID, Source: model.AuditSourceSystem, Action: "evidence_channel.ensure", ResourceType: "guild_settings", ResourceID: settings.ID, Result: model.AuditResultSuccess, MetadataJSON: "{}"}})
	return channelID, err
}

// RepairDiscordGuildEvidenceChannel reloads durable channel state and recreates storage after Discord deletes the channel.
func (s *EvidenceService) RepairDiscordGuildEvidenceChannel(ctx context.Context, discordGuildID string) (string, error) {
	if s == nil || s.store == nil {
		return "", errors.New("evidence service is not configured")
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
	if err != nil || parsed.Scheme != "https" || (parsed.Host != "discord.com" && parsed.Host != "www.discord.com" && parsed.Host != "ptb.discord.com" && parsed.Host != "canary.discord.com") {
		return DiscordMessageReference{}, fmt.Errorf("%w: invalid Discord message link", ErrEvidenceValidation)
	}
	match := discordMessageLinkPattern.FindStringSubmatch(parsed.EscapedPath())
	if len(match) != 4 {
		return DiscordMessageReference{}, fmt.Errorf("%w: invalid Discord message link path", ErrEvidenceValidation)
	}
	return DiscordMessageReference{GuildID: match[1], ChannelID: match[2], MessageID: match[3], URL: parsed.String()}, nil
}

// Capture snapshots each unique message before case commit and preserves supported attachments when possible.
func (s *EvidenceService) Capture(ctx context.Context, guildID, actorDiscordUserID, targetDiscordUserID, evidenceChannelID string, links []string) (*CapturedEvidence, error) {
	if actorDiscordUserID == "" && len(links) > 0 {
		return nil, fmt.Errorf("%w: evidence actor is required", ErrEvidenceValidation)
	}
	return s.capture(ctx, guildID, actorDiscordUserID, targetDiscordUserID, evidenceChannelID, links)
}

// capture permits an empty actor only for the trusted system case path.
func (s *EvidenceService) capture(ctx context.Context, guildID, actorDiscordUserID, targetDiscordUserID, evidenceChannelID string, links []string) (*CapturedEvidence, error) {
	if len(links) > maxEvidenceMessages {
		return nil, fmt.Errorf("%w: at most %d message links can be captured", ErrEvidenceValidation, maxEvidenceMessages)
	}
	result := &CapturedEvidence{}
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
		if s == nil || s.client == nil {
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
			_ = errors.As(err, &unavailable)
			warning := "Message could not be saved. You can add evidence later."
			outcome := "unavailable"
			if unavailable != nil {
				outcome = unavailable.Outcome
				warning = unavailable.Error()
			}
			id, idErr := idutil.NewULID()
			if idErr != nil {
				return nil, idErr
			}
			result.Snapshots = append(result.Snapshots, model.CaseEvidenceSnapshot{
				ULIDModel: model.ULIDModel{ID: id}, GuildID: guildID,
				ChannelDiscordID: ref.ChannelID, MessageDiscordID: ref.MessageID,
				MessageURL: ref.URL, MessageCreatedAt: time.Now().UTC(),
				CaptureOutcome: outcome, CaptureWarning: warning, EmbedsJSON: "[]",
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
		embedJSON, _ := json.Marshal(embeds)
		evidenceID, idErr := idutil.NewULID()
		if idErr != nil {
			return nil, idErr
		}
		snapshot := model.CaseEvidenceSnapshot{ULIDModel: model.ULIDModel{ID: evidenceID}, GuildID: guildID, ChannelDiscordID: message.ChannelID, MessageDiscordID: message.MessageID, AuthorDiscordUserID: message.AuthorDiscordUserID, MessageURL: message.URL, Content: content, MessageCreatedAt: message.CreatedAt, MessageEditedAt: message.EditedAt, EmbedsJSON: string(embedJSON), CaptureOutcome: "captured"}
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
		slog.WarnContext(ctx, "Evidence capture incomplete", "discord_guild_id", guildID, "messages", len(result.Snapshots), "attachments", len(result.Attachments), "warnings", len(result.Warnings))
	}
	return result, nil
}

// truncateRunes applies stable Unicode-aware evidence limits.
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
	return strings.HasPrefix(value, "image/") || strings.HasPrefix(value, "video/") || strings.HasPrefix(value, "audio/") || value == "text/plain" || value == "application/pdf"
}

// preserveAttachment records original metadata even when a managed copy fails.
// Both message capture and direct uploads share these size and type limits.
func (s *EvidenceService) preserveAttachment(ctx context.Context, guildID, evidenceChannelID, evidenceID string, attachment DiscordAttachmentSnapshot) model.CaseEvidenceAttachment {
	record := model.CaseEvidenceAttachment{EvidenceID: evidenceID, Filename: truncateRunes(attachment.Filename, 255), ContentType: truncateRunes(attachment.ContentType, 191), SizeBytes: attachment.SizeBytes, OriginalURL: attachment.URL, CopyOutcome: "metadata_only"}
	if s == nil || s.client == nil || evidenceChannelID == "" {
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
