package commands

import (
	"context"
	"reflect"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestCaseWebLinkPreservesNativePrivateResponse verifies existing route shapes,
// mount prefixes, and that navigation never replaces controls or captured text.
func TestCaseWebLinkPreservesNativePrivateResponse(t *testing.T) {
	original := ui.Message{Content: "private content", Ephemeral: true, Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.Button{Label: "Next", CustomID: "next", Style: discordgo.SecondaryButton}}}}}
	for _, test := range []struct{ resource, id, suffix string }{{"cases", "", "cases"}, {"cases", "01J40000000000000000000001", "cases/01J40000000000000000000001"}, {"members", "12345", "members/12345"}} {
		got := caseWebLink(original, "https://example.com/app/", "3001", test.resource, test.id)
		if got.Content != original.Content || !got.Ephemeral || len(got.Components) != 2 || !reflect.DeepEqual(got.Components[0], original.Components[0]) {
			t.Fatalf("native response changed: %+v", got)
		}
		button := got.Components[1].(discordgo.ActionsRow).Components[0].(discordgo.Button)
		if button.URL != "https://example.com/app/guilds/3001/"+test.suffix || button.Style != discordgo.LinkButton || button.CustomID != "" {
			t.Fatalf("wrong destination: %+v", button)
		}
	}
	for _, base := range []string{"", "http://example.com", "https://user:password@example.com", "https://example.com?token=secret", "https://example.com#fragment"} {
		if got := caseWebLink(original, base, "3001", "cases", "1"); !reflect.DeepEqual(got, original) {
			t.Fatalf("unsafe/unset base accepted: %q", base)
		}
	}
	for _, id := range []string{"../staff", "case?token=secret", "a/b", "#fragment"} {
		if got := caseWebLink(original, "https://example.com", "3001", "cases", id); !reflect.DeepEqual(got, original) {
			t.Fatalf("unsafe record accepted: %q", id)
		}
	}
}

// TestCaseRecordWebLinksSurviveNativeNavigation checks authorized detail,
// evidence and list commands plus their component updates against real services.
func TestCaseRecordWebLinksSurviveNativeNavigation(t *testing.T) {
	repository, services, _ := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	services.Config.ApplicationBaseURL = "https://dashboard.example/base"
	item := model.Case{ULIDModel: model.ULIDModel{ID: "case-web-1"}, GuildID: guild.Guild.ID, CaseNumber: 1, TargetDiscordUserID: "target-1", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, TemplateSnapshotJSON: "{}", MetadataJSON: "{}", ContextValuesJSON: "[]"}
	if err := repository.DB().Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"view", "list", "evidence_detail", "evidence_next", "detail_next", "list_next"} {
		t.Run(entry, func(t *testing.T) {
			interaction := caseAddInteraction("", "target-1", uint64(discordgo.PermissionModerateMembers))
			handler := HandleCaseInteraction
			record := "/case-web-1"
			update := false
			switch entry {
			case "view", "list":
				interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "case", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: entry, Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "case", Type: discordgo.ApplicationCommandOptionString, Value: item.ID}}}}}
				if entry == "list" {
					record = ""
				}
			default:
				interaction.Type = discordgo.InteractionMessageComponent
				payload := "1|" + item.ID
				switch entry {
				case "evidence_detail":
					handler = handleCaseEvidenceComponent
					payload = item.ID
				case "evidence_next":
					handler = pageEvidence(1)
					update = true
				case "detail_next":
					handler = pageCaseRecord(1, views.CaseDetailPage)
					update = true
				case "list_next":
					handler = pageCases(1, false)
					payload = "1"
					record = ""
					update = true
				}
				interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: entry, Version: "v1", Payload: payload})}
			}
			result := handler(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
			responder := &fakeResponder{}
			if result.Task == nil {
				t.Fatal("missing task")
			}
			if err := result.Task(context.Background(), responder); err != nil {
				t.Fatal(err)
			}
			components := responder.edit.Components
			if update {
				components = responder.updated.Components
			}
			if components == nil {
				t.Fatal("missing components")
			}
			found := false
			for _, row := range *components {
				for _, component := range row.(discordgo.ActionsRow).Components {
					if button, ok := component.(discordgo.Button); ok && button.Style == discordgo.LinkButton {
						found = button.URL == "https://dashboard.example/base/guilds/"+guild.Guild.DiscordGuildID+"/cases"+record
					}
				}
			}
			if !found {
				t.Fatal("missing correct web equivalent")
			}
		})
	}
}
