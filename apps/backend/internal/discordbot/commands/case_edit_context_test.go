package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestContextModalConfirmsSavedTextDespiteEvidenceFailure exercises private
// feedback after a committed edit, including the unchanged no-link path.
func TestContextModalConfirmsSavedTextDespiteEvidenceFailure(t *testing.T) {
	for _, text := range []string{"Staff notes", "Staff notes: https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"} {
		t.Run(text, func(t *testing.T) {
			ctx := context.Background()
			_, services, templateID := newCaseCommandHarness(t)
			guild := caseCommandGuildContext(t, services)
			created, err := services.Cases.Create(ctx, guild, quack.CaseInput{TemplateID: templateID, TargetDiscordUserID: "target-1"})
			if err != nil {
				t.Fatal(err)
			}
			interaction := caseAddInteraction(templateID, "target-1", uint64(discordgo.PermissionModerateMembers))
			interaction.Type = discordgo.InteractionModalSubmit
			interaction.Data = discordgo.ModalSubmitInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "edit_context_submit", Version: "v1", Payload: created.ID}), Components: []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "context", Value: text})}}
			result := handleEditContextModal(ui.Context{Context: ctx, Services: services, Interaction: interaction})
			if result.Task == nil || (result.Response.Data != nil && result.Response.Data.Flags&discordgo.MessageFlagsEphemeral != 0) {
				t.Fatal("context submission was not private")
			}
			responder := &fakeResponder{}
			if err := result.Task(ctx, responder); err != nil {
				t.Fatal(err)
			}
			if responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "Context saved") {
				t.Fatal("saved edit not confirmed")
			}
			if strings.Contains(text, "https://") != strings.Contains(*responder.edit.Content, "Some evidence could not be saved") {
				t.Fatalf("wrong evidence feedback: %s", *responder.edit.Content)
			}
			detail, err := services.Cases.Get(ctx, guild, created.ID)
			if err != nil || len(detail.ContextValues) != 1 || detail.ContextValues[0].Value != text {
				t.Fatalf("optional failure lost text: %+v %v", detail, err)
			}
		})
	}
}
