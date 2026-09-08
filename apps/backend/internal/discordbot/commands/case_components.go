package commands

import (
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// RegisterCaseComponents installs case browsing, context editing and recovery controls.
func RegisterCaseComponents(registry *interactions.ComponentRegistry) error {
	components := map[string]ui.Handler{
		"list_prev": pageCases(-1, false), "list_next": pageCases(1, false),
		"user_prev": pageCases(-1, true), "user_next": pageCases(1, true),
		"failures_prev": pageFailures(-1), "failures_next": pageFailures(1),
		"retry": handleRetryComponent, "dismiss": handleDismissComponent,
		"void": handleVoidComponent, "reverse": handleReverseComponent,
		"message_template": handleMessageTemplateComponent,
		"user_template":    handleUserTemplateComponent,
		"edit_context":     handleEditContextComponent,
		"evidence":         handleCaseEvidenceComponent,
		"user_detail":      handleCaseUserComponent,
	}
	for action, handler := range components {
		if err := registry.RegisterComponent("case", action, handler); err != nil {
			return err
		}
	}
	for action, handler := range map[string]ui.Handler{"void_submit": handleVoidModal, "reverse_submit": handleReverseModal, "edit_context_submit": handleEditContextModal} {
		if err := registry.RegisterModal("case", action, handler); err != nil {
			return err
		}
	}
	return nil
}
