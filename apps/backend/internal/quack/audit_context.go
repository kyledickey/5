package quack

import (
	"context"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// auditSourceContextKey is the private context key for the adapter audit source.
type auditSourceContextKey struct{}

// ContextWithAuditSource carries the authoritative adapter source across shared business services.
func ContextWithAuditSource(ctx context.Context, source model.AuditSource) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, auditSourceContextKey{}, source)
}

// AuditSourceForModuleAction classifies automated/import module events while
// preserving the caller transport source for staff-driven module operations.
func AuditSourceForModuleAction(ctx context.Context, action string) model.AuditSource {
	action = strings.ToLower(strings.TrimSpace(action))
	if strings.Contains(action, "v4_import") || strings.Contains(action, "v4_settings_import") {
		return model.AuditSourceImport
	}
	if strings.HasPrefix(action, "honeypot.trigger.") || action == "honeypot.case.created" || action == "honeypot.configuration.disabled" {
		return model.AuditSourceHoneypot
	}
	return AuditSourceFromContext(ctx)
}

// AuditSourceFromContext returns the adapter source, defaulting to the backend API boundary.
func AuditSourceFromContext(ctx context.Context) model.AuditSource {
	if ctx != nil {
		if source, ok := ctx.Value(auditSourceContextKey{}).(model.AuditSource); ok && validAuditSource(source) {
			return source
		}
	}
	return model.AuditSourceAPI
}

// AuditSourceForCaseSource maps durable case origin to immutable audit origin.
func AuditSourceForCaseSource(source model.CaseSource) model.AuditSource {
	switch source {
	case model.CaseSourceDiscord:
		return model.AuditSourceDiscord
	case model.CaseSourceHoneypot:
		return model.AuditSourceHoneypot
	case model.CaseSourceV4Import:
		return model.AuditSourceImport
	default:
		return model.AuditSourceWeb
	}
}
