package quack

import (
	"context"
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// FindSystemHoneypot resolves only a system-owned incident's exact persisted
// request. This internal recovery boundary performs no live authorization,
// evidence reads or action scheduling; it must not be exposed as a user lookup.
// It returns (nil, nil) when no case carries the incident's idempotency key and
// a validation error when a stored case does not match the request exactly.
func (s *CaseService) FindSystemHoneypot(ctx context.Context, guildID string, input CaseInput) (*CaseResponse, error) {
	guildID = strings.TrimSpace(guildID)
	key := strings.TrimSpace(input.IdempotencyKey)
	if input.Source != model.CaseSourceHoneypot ||
		guildID == "" ||
		key != "honeypot:"+guildID+":"+input.ContextMessageDiscordID ||
		input.TemplateID == "" ||
		input.TargetDiscordUserID == "" ||
		input.ContextChannelDiscordID == "" ||
		input.ContextMessageDiscordID == "" {
		return nil, validationCaseError("invalid system honeypot recovery identity")
	}
	saved, err := s.store.GetCaseByIdempotencyKey(ctx, guildID, key)
	if err != nil || saved == nil {
		return nil, err
	}
	if saved.GuildID != guildID ||
		saved.Source != model.CaseSourceHoneypot ||
		saved.ModeratorDiscordUserID != "" ||
		saved.TargetDiscordUserID != input.TargetDiscordUserID ||
		saved.TemplateID == nil ||
		*saved.TemplateID != input.TemplateID ||
		saved.ContextChannelDiscordID != input.ContextChannelDiscordID ||
		saved.ContextMessageDiscordID != input.ContextMessageDiscordID ||
		(input.ContextURL != "" && saved.ContextURL != input.ContextURL) {
		return nil, validationCaseError("saved case does not match system honeypot incident")
	}
	response := caseResponseFromModel(*saved, nil)
	return &response, nil
}

// EnsureHoneypotTemplate creates the initial ban policy through normal template
// validation and auditing. Repeated setup preserves the administrator's edits.
// Archived policies require an explicit restore rather than being silently revived.
func (s *TemplateService) EnsureHoneypotTemplate(ctx context.Context, guild *GuildStaffContext) (*TemplateResponse, error) {
	if err := s.requireWrite(ctx, guild, "case_template.create", ""); err != nil {
		return nil, err
	}
	existing, err := s.store.GetCaseTemplateBySlug(ctx, guild.Guild.ID, "honeypot")
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.ArchivedAt != nil {
			return nil, fmt.Errorf("%w: restore the archived honeypot template before setup", ErrTemplateValidation)
		}
		return s.Get(ctx, guild, existing.ID)
	}
	return s.Create(ctx, guild, TemplateInput{
		Slug:           "honeypot",
		Name:           "Honeypot",
		Description:    "Applied when a member posts in the honeypot channel.",
		ReasonTemplate: "Posted in the honeypot channel despite the warning.",
		Appealable:     true,
		Levels: []TemplateLevelInput{{
			Name:       "Default",
			Position:   1,
			IsDefault:  true,
			NotifyUser: true,
			Actions:    []TemplateActionInput{{ActionType: model.ActionBanUser}},
		}},
	})
}
