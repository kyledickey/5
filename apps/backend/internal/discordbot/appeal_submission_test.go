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
	content   string
	edits     int
	followups int
	lastEdit  ui.Edit
}

// EditOriginal captures the source message update for lifecycle assertions.
func (r *appealTestResponder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	r.edits++
	r.lastEdit = edit
	if edit.Content != nil {
		r.content = *edit.Content
	}
	return &discordgo.Message{ID: "response"}, nil
}

// Followup captures private errors without replacing the shared queue entry.
func (r *appealTestResponder) Followup(message ui.Message) (*discordgo.Message, error) {
	r.content = message.Content
	r.followups++
	if !message.Ephemeral {
		panic("appeal errors must stay private")
	}
	return &discordgo.Message{ID: "error"}, nil
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

// appealReviewAuthorization supplies fresh permissions on each simulated click.
type appealReviewAuthorization struct {
	quack.DiscordClient
	permissions uint64
}

func (a *appealReviewAuthorization) GuildAuthorization(_ context.Context, guild, actor, target string) (*quack.DiscordGuildAuthorization, error) {
	return &quack.DiscordGuildAuthorization{Guild: quack.DiscordBotGuild{ID: guild, Name: "Pond", OwnerID: "owner"}, Actor: quack.DiscordMemberAuthorization{DiscordUserID: actor, Present: true, PermissionBits: a.permissions}, Bot: quack.DiscordMemberAuthorization{DiscordUserID: "bot", Present: true}}, nil
}

// TestAppealQueueDecisionChecksLivePermissions covers denial followed by approval,
// then a competing button click without changing the committed acceptance.
func TestAppealQueueDecisionChecksLivePermissions(t *testing.T) {
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
	appeal, err := appeals.Submit(ctx, created.Case.ID, "target", quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}})
	if err != nil {
		t.Fatal(err)
	}
	auth := &appealReviewAuthorization{}
	services := &quack.Services{Guilds: quack.NewGuildService(repository, auth)}
	click := func(action string) string {
		id := ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: action, Version: "v1", Payload: appeal.ID})
		result := appealDecisionHandler(services, appeals, action)(ui.Context{Context: ctx, Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "guild", Member: &discordgo.Member{User: &discordgo.User{ID: "mod"}}, Data: discordgo.MessageComponentInteractionData{CustomID: id}}}})
		if result.Task == nil {
			t.Fatal("missing decision task")
		}
		if result.Response.Type != discordgo.InteractionResponseDeferredMessageUpdate {
			t.Fatalf("decision created a separate acknowledgement: %+v", result.Response)
		}
		r := &appealTestResponder{}
		if err := result.Task(ctx, r); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(r.content, "Appeal accepted") {
			if r.edits != 1 || r.followups != 0 {
				t.Fatal("decision did not update original exactly once", r)
			}
		} else if r.edits != 0 || r.followups != 1 {
			t.Fatal("error replaced shared queue", r)
		}
		return r.content
	}
	if text := click("accept"); !strings.Contains(text, "Moderate Members") {
		t.Fatalf("permission denial: %s", text)
	}
	pending, err := repository.GetAppealByID(ctx, appeal.ID)
	if err != nil || pending.Status != model.AppealStatusPending {
		t.Fatalf("denial changed appeal: %+v %v", pending, err)
	}
	auth.permissions = uint64(discordgo.PermissionModerateMembers)
	if text := click("accept"); !strings.Contains(text, "accepted") {
		t.Fatalf("acceptance: %s", text)
	}
	if text := click("reject"); !strings.Contains(text, "already been decided") {
		t.Fatalf("competing decision: %s", text)
	}
	item, err := repository.GetCaseByID(ctx, created.Case.ID)
	if err != nil || item.Validity != model.CaseValidityVoided {
		t.Fatalf("acceptance did not void: %+v %v", item, err)
	}
	member, err := appeals.GetMember(ctx, appeal.ID, "target")
	if err != nil || member.ReviewedByDiscordUserID != "" {
		t.Fatalf("member reviewer privacy: %+v %v", member, err)
	}
}
