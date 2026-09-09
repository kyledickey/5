package quack

import (
	"context"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// CaseEvidencePageResponse contains one authorized snapshot and navigation counts;
// the ordinary API detail and evidence collection contracts remain unchanged.
type CaseEvidencePageResponse struct {
	CaseDetailResponse
	Position int
	Total    int64
}

// GetEvidencePage checks current staff/guild access before fetching any captured
// content. It preserves read audit behavior while bounding native snapshot reads.
func (s *CaseService) GetEvidencePage(ctx context.Context, guild *GuildStaffContext, caseRef string, position int) (*CaseEvidencePageResponse, error) {
	if err := s.requireCaseRead(guild); err != nil {
		_ = s.audit(ctx, guild, string(model.AuditActionCaseRead), "case", strings.TrimSpace(caseRef), model.AuditResultDenied, "permission_denied")
		return nil, err
	}
	caseRef = strings.TrimSpace(caseRef)
	if caseRef == "" {
		return nil, validationCaseError("case reference is required")
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guild.Guild.ID, caseRef)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	snapshot, attachments, total, err := s.store.GetCaseEvidencePage(ctx, item.ID, position)
	if err != nil {
		return nil, err
	}
	if err := s.audit(ctx, guild, "case.read", "case", item.ID, model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	if position < 1 {
		position = 1
	}
	if int64(position) > total {
		position = max(1, int(total))
	}
	var evidence []CaseEvidenceResponse
	if snapshot != nil {
		evidence = caseEvidenceResponses([]model.CaseEvidenceSnapshot{*snapshot}, attachments, false)
	}
	return &CaseEvidencePageResponse{CaseDetailResponse: CaseDetailResponse{CaseResponse: CaseResponse{ID: item.ID, CaseNumber: item.CaseNumber, ContextValues: parseCaseContextValues(item.ContextValuesJSON)}, Evidence: evidence}, Position: position, Total: total}, nil
}
