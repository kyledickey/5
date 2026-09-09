package moduleintegration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// TestQueueRecoveryStaleFeedback explains how to refresh expired recovery
// controls without offering a blind retry or claiming a delivery decision.
func TestQueueRecoveryStaleFeedback(t *testing.T) {
	for _, err := range []error{tickets.ErrInvalidTransition, fmt.Errorf("wrapped: %w", tickets.ErrQueueDeliveryUnknown)} {
		message := ticketQueueRecoveryError(err)
		if !strings.Contains(message, "View ticket again") || !strings.Contains(message, "No replacement was sent") {
			t.Fatalf("missing recovery guidance: %s", message)
		}
	}
	if ticketQueueRecoveryError(tickets.ErrPermissionDenied) != ticketErrorMessage(tickets.ErrPermissionDenied) {
		t.Fatal("permission denial was hidden by stale-state guidance")
	}
}

// TestQueueRecoveryIsExceptionalAndManagerOnly keeps ordinary ticket controls
// simple and prevents owners or moderators from receiving recovery authority.
func TestQueueRecoveryIsExceptionalAndManagerOnly(t *testing.T) {
	for _, scenario := range []struct {
		manager, pending, want bool
		status                 tickets.Status
		channel, message       string
	}{
		{true, false, true, tickets.StatusOpen, "queue", ""},
		{false, false, false, tickets.StatusOpen, "queue", ""},
		{true, false, false, tickets.StatusOpen, "queue", "sent"},
		{true, false, false, tickets.StatusOpen, "", ""},
		{true, true, true, tickets.StatusResolved, "queue", ""},
		{true, false, false, tickets.StatusResolved, "queue", ""},
	} {
		ticket := &tickets.Ticket{ID: "ticket", Status: scenario.status, LogChannelDiscordID: scenario.channel, LogMessageDiscordID: scenario.message}
		message := ticketDetailMessage(ticket, nil, tickets.Actor{CanManage: scenario.manager, CanModerate: true}, scenario.pending, nil, 0)
		found := false
		for _, row := range message.Components {
			for _, component := range row.(discordgo.ActionsRow).Components {
				button := component.(discordgo.Button)
				id, err := ui.DecodeCustomID(button.CustomID)
				if err != nil {
					t.Fatal(err)
				}
				if id.Action == "queuefix" {
					found = true
				}
			}
		}
		if found != scenario.want || !message.Ephemeral {
			t.Fatalf("wrong recovery visibility: %+v", scenario)
		}
	}
}

// TestQueueRecoveryConfirmationAndAuthorization binds confirmation/modal state
// to one attempt and verifies final submissions still require fresh authority.
func TestQueueRecoveryConfirmationAndAuthorization(t *testing.T) {
	r := &Runtime{}
	registry := interactions.NewComponentRegistry()
	if err := r.registerTicketQueueRecovery(registry); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("T", 26) + "~" + strings.Repeat("A", 26)
	ctx := ui.Context{Context: context.Background(), Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "guild", Type: discordgo.InteractionMessageComponent, Member: &discordgo.Member{User: &discordgo.User{ID: "former-admin"}}, Data: discordgo.MessageComponentInteractionData{CustomID: queueRecoveryButton("queueretry", payload, "test", discordgo.SecondaryButton).CustomID}}}}
	confirmation := r.ticketQueueRetryComponent(ctx)
	if confirmation.Task != nil || confirmation.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || !strings.Contains(confirmation.Response.Data.Content, "duplicate") {
		t.Fatal("confirmation was not explicit and private")
	}
	button := confirmation.Response.Data.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	id, err := ui.DecodeCustomID(button.CustomID)
	if err != nil || id.Payload != payload || id.Action != "queueretryok" {
		t.Fatal("confirmation lost its attempt")
	}
	handler, ok, err := registry.LookupComponent(button.CustomID)
	if err != nil || !ok {
		t.Fatal("confirmation is not registered")
	}
	ctx.Interaction.Data = discordgo.MessageComponentInteractionData{CustomID: button.CustomID}
	result := handler(ctx)
	if result.Task == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatal("mutation did not defer for live authorization")
	}
	responder := &progressResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if len(responder.messages) != 1 || !strings.Contains(responder.messages[0], "could not verify") {
		t.Fatal("revoked/unavailable authority reached recovery")
	}
	ctx.Interaction.Data = discordgo.MessageComponentInteractionData{CustomID: queueRecoveryButton("queueadopt", payload, "test", discordgo.SecondaryButton).CustomID}
	modal := r.ticketQueueAdoptComponent(ctx)
	if modal.Response.Type != discordgo.InteractionResponseModal {
		t.Fatal("missing adoption form")
	}
	if _, ok, err := registry.LookupModal(modal.Response.Data.CustomID); err != nil || !ok {
		t.Fatal("adoption submit is not registered")
	}
}
