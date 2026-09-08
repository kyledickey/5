package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
)

// unattendedTemplateCheck lets the adapter tests supply only the narrow use case.
type unattendedTemplateCheck func(context.Context, string, string) error

// ValidateUnattendedTemplate delegates to the test's scoped policy response.
func (f unattendedTemplateCheck) ValidateUnattendedTemplate(ctx context.Context, guildID, templateID string) error {
	return f(ctx, guildID, templateID)
}

// TestHoneypotTemplateValidatorErrorMapping ensures transient failures do not
// masquerade as policy drift and trigger unavailable-template recovery.
func TestHoneypotTemplateValidatorErrorMapping(t *testing.T) {
	storageErr := errors.New("database unavailable")
	for _, tt := range []struct {
		name         string
		result, want error
	}{
		{name: "compatible"},
		{name: "policy drift", result: fmt.Errorf("%w: required context", quack.ErrUnattendedTemplateUnavailable), want: honeypot.ErrTemplateUnavailable},
		{name: "storage failure", result: storageErr, want: storageErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			validator := honeypotTemplateValidator{templates: unattendedTemplateCheck(func(_ context.Context, guildID, templateID string) error {
				if guildID != "guild" || templateID != "template" {
					t.Fatalf("scope = %q/%q", guildID, templateID)
				}
				return tt.result
			})}
			err := validator.ValidateHoneypotTemplate(context.Background(), "guild", "template")
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if tt.result == storageErr && errors.Is(err, honeypot.ErrTemplateUnavailable) {
				t.Fatal("storage failure classified as policy drift")
			}
		})
	}
	if err := (honeypotTemplateValidator{}).ValidateHoneypotTemplate(context.Background(), "guild", "template"); err == nil || errors.Is(err, honeypot.ErrTemplateUnavailable) {
		t.Fatalf("missing dependency error = %v", err)
	}
}
