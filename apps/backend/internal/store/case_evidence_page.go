package store

import (
	"context"
	"errors"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// GetCaseEvidencePage counts a case's evidence and loads one oldest-first snapshot
// plus only its attachments. ID breaks timestamp ties for stable navigation.
func (s *Store) GetCaseEvidencePage(ctx context.Context, caseID string, position int) (*model.CaseEvidenceSnapshot, []model.CaseEvidenceAttachment, int64, error) {
	if s == nil || s.db == nil {
		return nil, nil, 0, errors.New("database not connected")
	}
	var total int64
	if err := s.db.WithContext(ctx).Model(&model.CaseEvidenceSnapshot{}).Where("case_id = ?", caseID).Count(&total).Error; err != nil {
		return nil, nil, 0, err
	}
	if total == 0 {
		return nil, nil, 0, nil
	}
	if position < 1 {
		position = 1
	}
	if int64(position) > total {
		position = int(total)
	}
	var snapshot model.CaseEvidenceSnapshot
	if err := s.db.WithContext(ctx).Where("case_id = ?", caseID).Order("created_at ASC, id ASC").Offset(position - 1).Limit(1).First(&snapshot).Error; err != nil {
		return nil, nil, 0, err
	}
	var attachments []model.CaseEvidenceAttachment
	err := s.db.WithContext(ctx).Where("evidence_id = ?", snapshot.ID).Order("created_at ASC, id ASC").Find(&attachments).Error
	return &snapshot, attachments, total, err
}
