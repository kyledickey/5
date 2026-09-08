package tickets

import (
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

const componentNamespace = "ticket"

// ComponentHandlers supplies integration-owned identity resolution and presentation for ticket interactions.
type ComponentHandlers struct {
	Open, Queue, View, Close ui.Handler
}

// RegisterComponents installs the complete ticket interaction surface into a caller-owned registry.
func RegisterComponents(registry *interactions.ComponentRegistry, handlers ComponentHandlers) error {
	if registry == nil {
		return errors.New("component registry is required")
	}
	for _, entry := range []struct {
		name    string
		handler ui.Handler
	}{{"open", handlers.Open}, {"queue", handlers.Queue}, {"view", handlers.View}, {"close", handlers.Close}} {
		if entry.handler == nil {
			return errors.New("all ticket component handlers are required")
		}
		if err := registry.RegisterComponent(componentNamespace, entry.name, entry.handler); err != nil {
			return err
		}
	}
	return nil
}

// EntryComponents builds member-open and staff-queue controls with stable routed identifiers.
func EntryComponents() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{ui.Row(
		ui.Button(ui.MustCustomID(ui.CustomID{Namespace: componentNamespace, Action: "open", Version: "v1"}), "Open ticket", discordgo.PrimaryButton, false),
		ui.Button(ui.MustCustomID(ui.CustomID{Namespace: componentNamespace, Action: "queue", Version: "v1"}), "Staff queue", discordgo.SecondaryButton, false),
	)}
}

// TicketComponents keeps ticket navigation and closure beside the native thread conversation.
func TicketComponents(ticketID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{ui.Row(
		ui.Button(ui.MustCustomID(ui.CustomID{Namespace: componentNamespace, Action: "view", Version: "v1", Payload: ticketID}), "View", discordgo.SecondaryButton, false),
		ui.Button(ui.MustCustomID(ui.CustomID{Namespace: componentNamespace, Action: "close", Version: "v1", Payload: ticketID}), "Close", discordgo.DangerButton, false),
	)}
}
