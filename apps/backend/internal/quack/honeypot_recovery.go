package quack

import (
	"context"
	"errors"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// FindSystemHoneypot resolves only a system-owned incident's exact persisted
// request. This internal recovery boundary performs no live authorization,
// evidence reads or action scheduling; it must not be exposed as a user lookup.
func (s *CaseService) FindSystemHoneypot(ctx context.Context, guildID string, input CaseInput) (*CaseResponse, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("case service is not configured")
	}
	guildID = strings.TrimSpace(guildID)
	key := strings.TrimSpace(input.IdempotencyKey)
	if input.Source != model.CaseSourceHoneypot || guildID == "" || key != "honeypot:"+guildID+":"+input.ContextMessageDiscordID || input.TemplateID == "" || input.TargetDiscordUserID == "" || input.ContextChannelDiscordID == "" || input.ContextMessageDiscordID == "" {
		return nil, validationCaseError("invalid system honeypot recovery identity")
	}
	saved, err := s.store.GetCaseByIdempotencyKey(ctx, guildID, key)
	if err != nil || saved == nil {
		return nil, err
	}
	if saved.GuildID != guildID || saved.Source != model.CaseSourceHoneypot || saved.ModeratorDiscordUserID != "" || saved.TargetDiscordUserID != input.TargetDiscordUserID || saved.TemplateID == nil || *saved.TemplateID != input.TemplateID || saved.ContextChannelDiscordID != input.ContextChannelDiscordID || saved.ContextMessageDiscordID != input.ContextMessageDiscordID || (input.ContextURL != "" && saved.ContextURL != input.ContextURL) {
		return nil, validationCaseError("saved case does not match system honeypot incident")
	}
	response := caseResponseFromModel(*saved, nil)
	return &response, nil
}
