package quack_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// honeypotLookupRepository exposes only the recovery read. Any accidental write,
// evidence read or action scheduling through other repository methods panics.
type honeypotLookupRepository struct {
	quack.CaseRepository
	saved *model.Case
}

// GetCaseByIdempotencyKey returns the stored fixture without any live dependency.
func (r honeypotLookupRepository) GetCaseByIdempotencyKey(context.Context, string, string) (*model.Case, error) {
	return r.saved, nil
}

// TestFindSystemHoneypotRequiresExactIdentity rejects accidental association with
// another request while permitting read-only reconciliation after authority loss.
func TestFindSystemHoneypotRequiresExactIdentity(t *testing.T) {
	template := "template"
	request := quack.CaseInput{Source: model.CaseSourceHoneypot, TemplateID: template, TargetDiscordUserID: "member", ContextChannelDiscordID: "trap", ContextMessageDiscordID: "message", IdempotencyKey: "honeypot:guild:message"}
	saved := model.Case{ULIDModel: model.ULIDModel{ID: "case"}, GuildID: "guild", Source: model.CaseSourceHoneypot, TemplateID: &template, TargetDiscordUserID: "member", ContextChannelDiscordID: "trap", ContextMessageDiscordID: "message"}
	service := quack.NewCaseService(honeypotLookupRepository{saved: &saved}, nil)
	found, err := service.FindSystemHoneypot(context.Background(), "guild", request)
	if err != nil || found == nil || found.ID != "case" {
		t.Fatal("read-only recovery failed", found, err)
	}
	for _, mutate := range []func(*model.Case){
		func(c *model.Case) { c.GuildID = "other" }, func(c *model.Case) { c.Source = model.CaseSourceDashboard }, func(c *model.Case) { c.TargetDiscordUserID = "other" },
		func(c *model.Case) { c.TemplateID = nil }, func(c *model.Case) { c.ContextChannelDiscordID = "other" }, func(c *model.Case) { c.ContextMessageDiscordID = "other" }, func(c *model.Case) { c.ModeratorDiscordUserID = "staff" },
	} {
		changed := saved
		mutate(&changed)
		if result, err := quack.NewCaseService(honeypotLookupRepository{saved: &changed}, nil).FindSystemHoneypot(context.Background(), "guild", request); err == nil || result != nil {
			t.Fatal("mismatched recovery accepted", changed)
		}
	}
	request.Source = model.CaseSourceDashboard
	if _, err := service.FindSystemHoneypot(context.Background(), "guild", request); err == nil {
		t.Fatal("non-system lookup accepted")
	}
}
