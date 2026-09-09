package commands

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestCaseProfileSummarySurvivesEveryNativeEntryPoint checks command, View user
// button, and page navigation against stored history larger than one page.
func TestCaseProfileSummarySurvivesEveryNativeEntryPoint(t *testing.T) {
	ctx := context.Background()
	repository, services, _ := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	services.Config.ApplicationBaseURL = "https://dashboard.example/base"
	for i := 1; i <= 11; i++ {
		item := model.Case{ULIDModel: model.ULIDModel{ID: fmt.Sprintf("profile-case-%d", i)}, GuildID: guild.Guild.ID, CaseNumber: uint64(i), TargetDiscordUserID: "target-1", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, TemplateSnapshotJSON: "{}", MetadataJSON: "{}", ContextValuesJSON: "[]"}
		if i == 1 {
			item.Source = model.CaseSourceV4Import
		}
		if i == 2 {
			item.Validity = model.CaseValidityVoided
		}
		if err := repository.DB().Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []string{"command", "button", "page"} {
		t.Run(entry, func(t *testing.T) {
			interaction := caseAddInteraction("", "target-1", uint64(discordgo.PermissionModerateMembers))
			handler := HandleCaseInteraction
			switch entry {
			case "command":
				interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "case", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "user", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "user", Type: discordgo.ApplicationCommandOptionUser, Value: "target-1"}}}}}
			case "button":
				interaction.Type = discordgo.InteractionMessageComponent
				interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "user_detail", Version: "v1", Payload: "target-1"})}
				handler = handleCaseUserComponent
			case "page":
				interaction.Type = discordgo.InteractionMessageComponent
				interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "user_next", Version: "v1", Payload: "1|target-1"})}
				handler = pageCases(1, true)
			}
			result := handler(ui.Context{Context: ctx, Services: services, Interaction: interaction})
			responder := &fakeResponder{}
			if result.Task == nil {
				t.Fatal("missing history task")
			}
			if err := result.Task(ctx, responder); err != nil {
				t.Fatal(err)
			}
			components := responder.edit.Components
			if entry == "page" {
				components = responder.updated.Components
			}
			if components == nil {
				t.Fatal("missing web navigation")
			}
			found := false
			for _, component := range *components {
				for _, control := range component.(discordgo.ActionsRow).Components {
					if button, ok := control.(discordgo.Button); ok && button.Style == discordgo.LinkButton {
						found = button.URL == "https://dashboard.example/base/guilds/"+guild.Guild.DiscordGuildID+"/members/target-1"
					}
				}
			}
			if !found {
				t.Fatalf("profile web destination absent on %s", entry)
			}
			content := responder.edit.Content
			if entry == "page" {
				content = responder.updated.Content
			}
			if content == nil || !strings.Contains(*content, "11 total · 10 active · 1 voided") {
				t.Fatalf("summary lost on %s: %v", entry, content)
			}
			if entry == "page" && !strings.Contains(*content, "Page 2/2") {
				t.Fatal("user pagination lost")
			}
		})
	}
}
