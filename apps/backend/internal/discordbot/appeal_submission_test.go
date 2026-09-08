package discordbot

import (
	"context"
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"github.com/quackdiscord/bot/internal/testutil"
	"strings"
	"testing"
)

// appealTestResponder captures only the edit used by the deferred submission.
type appealTestResponder struct {
	ui.Responder
	content string
}

func (r *appealTestResponder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	if edit.Content != nil {
		r.content = *edit.Content
	}
	return &discordgo.Message{ID: "response"}, nil
}

// TestAppealDMFormOwnershipAndSingleSubmission runs actual component/modal payloads
// against migrated storage without a guild member object or a live Discord call.
func TestAppealDMFormOwnershipAndSingleSubmission(t *testing.T) {
	ctx := context.Background()
	repository := testutil.NewSQLiteStore(t)
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	guild, err := repository.UpsertGuild(ctx, model.UpsertGuildParams{DiscordGuildID: "guild", Name: "Pond", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: model.Case{GuildID: guild.ID, TemplateVersion: 1, TemplateSnapshotJSON: `{"template":{"appealable":true}}`, TargetDiscordUserID: "target", ModeratorDiscordUserID: "mod", Reason: "Rule", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, MetadataJSON: `{}`, ContextValuesJSON: `[]`}, Event: model.CaseEvent{EventType: model.CaseEventCreated, ActorType: "staff", Body: "Case created", MetadataJSON: `{}`}})
	if err != nil {
		t.Fatal(err)
	}
	appeals := quack.NewAppealService(repository)
	id := ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "submit", Version: "v1", Payload: created.Case.ID})
	click := func(user string) ui.HandlerResult {
		return appealSubmissionHandler(appeals)(ui.Context{Context: ctx, Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, User: &discordgo.User{ID: user}, Data: discordgo.MessageComponentInteractionData{CustomID: id}}}})
	}
	if result := click("other"); result.Response.Type == discordgo.InteractionResponseModal {
		t.Fatal("other member opened case form")
	}
	opened := click("target")
	if opened.Response.Type != discordgo.InteractionResponseModal {
		t.Fatalf("owner could not open form: %+v", opened.Response)
	}
	row := opened.Response.Data.Components[0].(discordgo.ActionsRow)
	if len(row.Components) != 1 {
		t.Fatal("form is not one statement")
	}
	submit := func(user string) string {
		result := appealSubmissionModal(appeals)(ui.Context{Context: ctx, Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionModalSubmit, User: &discordgo.User{ID: user}, Data: discordgo.ModalSubmitInteractionData{CustomID: id, Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: "reason", Value: "I understand the rule and am sorry."}}}}}}}})
		if result.Task == nil {
			t.Fatal("modal has no submission task")
		}
		responder := &appealTestResponder{}
		if err := result.Task(ctx, responder); err != nil {
			t.Fatal(err)
		}
		return responder.content
	}
	if text := submit("other"); !strings.Contains(text, "not available") {
		t.Fatalf("forged modal accepted: %s", text)
	}
	if text := submit("target"); !strings.Contains(text, "submitted") {
		t.Fatalf("submission failed: %s", text)
	}
	if text := submit("target"); !strings.Contains(text, "already submitted") {
		t.Fatalf("duplicate lost feedback: %s", text)
	}
	saved, err := repository.GetAppealByCaseID(ctx, created.Case.ID)
	if err != nil || saved == nil || !strings.Contains(saved.AnswersJSON, "sorry") {
		t.Fatalf("statement not saved: %+v %v", saved, err)
	}
	if result := click("target"); result.Response.Type == discordgo.InteractionResponseModal {
		t.Fatal("duplicate appeal reopened form")
	}
	if _, err := repository.VoidCase(ctx, model.VoidCaseParams{GuildID: guild.ID, CaseID: created.Case.ID, ActorDiscordUserID: "mod", Reason: "Mistake"}); err != nil {
		t.Fatal(err)
	}
	if text := submit("target"); !strings.Contains(text, "cannot be appealed") {
		t.Fatalf("stale form ignored void: %s", text)
	}
}
