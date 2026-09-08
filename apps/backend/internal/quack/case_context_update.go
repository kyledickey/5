package quack

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// UpdateContext replaces staff context without changing the snapshotted rule,
// escalation count, validity, or enforcement. An empty value clears context.
func (s *CaseService) UpdateContext(ctx context.Context, guild *GuildStaffContext, caseRef, text string) (*CaseDetailResponse, error) {
	if s == nil || s.store == nil || guild == nil || guild.Guild == nil || !guild.Can(model.PermissionActionCaseCreate) {
		return nil, ErrCasePermissionDenied
	}
	text = strings.TrimSpace(text)
	if len([]rune(text)) > 4000 {
		return nil, validationCaseError("context must be 4,000 characters or fewer")
	}
	values := []CaseContextValueResponse{}
	if text != "" {
		values = append(values, CaseContextValueResponse{Key: "context", Label: "Context", FieldType: model.ContextFieldLongText, Value: text})
	}
	body, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	audit := s.auditEntry(ctx, guild, string(model.AuditActionCaseUpdate), "case", "", model.AuditResultSuccess, "")
	item, err := s.store.UpdateCaseContext(ctx, guild.Guild.ID, strings.TrimSpace(caseRef), string(body), audit)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	return s.Get(ctx, guild, item.ID)
}
