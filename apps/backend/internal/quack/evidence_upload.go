package quack

import (
	"context"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
)

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
		id, err := idutil.NewULID()
		if err != nil {
			return nil, err
		}
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
