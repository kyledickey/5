package commands

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestAppealsCommandFindsUndeliveredSubmissions verifies notification delivery
// is not a prerequisite to review and pagination tolerates intervening decisions.
func TestAppealsCommandFindsUndeliveredSubmissions(t *testing.T) {
	ctx := context.Background()
	repository, services, _ := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	for i := 0; i < 2; i++ {
		created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: model.Case{GuildID: guild.Guild.ID, TemplateSnapshotJSON: `{"template":{"appealable":true,"name":"Spam"}}`, TargetDiscordUserID: "target", ModeratorDiscordUserID: "mod", Reason: "Spam", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, ContextValuesJSON: `[]`, MetadataJSON: `{}`}, Event: model.CaseEvent{EventType: model.CaseEventCreated, ActorType: "staff", Body: "Case created", MetadataJSON: `{}`}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := services.Appeals.Submit(ctx, created.Case.ID, "target", quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: fmt.Sprintf("Statement %d", i)}}}); err != nil {
			t.Fatal(err)
		}
	}
	interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: "guild-1", Member: &discordgo.Member{User: &discordgo.User{ID: "mod-1"}}, Data: discordgo.ApplicationCommandInteractionData{Name: "appeals"}}}
	context := ui.Context{Context: ctx, Services: services, Interaction: interaction}
	show := func(page int, update bool) *fakeResponder {
		result := appealQueuePage(context, page, update)
		if result.Task == nil {
			t.Fatal("missing page task")
		}
		r := &fakeResponder{}
		if err := result.Task(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := AppealsCommandSpec().Handler(context)
	if first.Response.Data != nil && first.Response.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Fatal("staff appeal queue was hidden")
	}
	page := show(1, false)
	if page.edit.Content == nil || !strings.Contains(*page.edit.Content, "Statement") || !strings.Contains(*page.edit.Content, "Pending appeal 1 of 2") {
		t.Fatalf("undelivered submission missing: %+v", page.edit)
	}
	pending, err := services.Appeals.ListStaff(ctx, guild, model.AppealStatusPending, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services.Appeals.Reject(ctx, guild, pending.Appeals[0].ID, "The case stands."); err != nil {
		t.Fatal(err)
	}
	page = show(2, true)
	if !strings.Contains(*page.edit.Content, "Pending appeal 1 of 1") {
		t.Fatalf("queue shift did not recover: %s", *page.edit.Content)
	}
	revoked := uint64(0)
	services.Guilds = quack.NewGuildService(repository, fakeDiscordClient{botGuild: &quack.DiscordBotGuild{ID: "guild-1", OwnerID: "owner-1"}, liveActorPermissionBits: &revoked})
	page = show(1, true)
	if strings.Contains(*page.edit.Content, "Statement") || !strings.Contains(*page.edit.Content, "Moderate Members") {
		t.Fatalf("navigation leaked after permission loss: %s", *page.edit.Content)
	}
}
