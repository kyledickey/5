package quack_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
)

func TestSystemHoneypotCaseUsesNormalPathWithoutFabricatedStaff(t *testing.T) {
	ctx := idutil.ContextWithTrace(context.Background(), "req-honeypot", "corr-honeypot")
	repository := newMigratedStore(t)
	guild, err := repository.UpsertGuild(ctx, model.UpsertGuildParams{DiscordGuildID: "111111111111111111", Name: "Guild", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	template, err := repository.CreateCaseTemplate(ctx, model.CreateCaseTemplateParams{
		Template: model.CaseTemplate{GuildID: guild.ID, Slug: "honeypot", Name: "Honeypot", ReasonTemplate: "Trap channel activity", CreatedByDiscordUserID: "admin", UpdatedByDiscordUserID: "admin"},
		Levels: []model.ExpandedCaseTemplateLevel{{
			Level:   model.CaseTemplateLevel{Name: "Default", Position: 1, IsDefault: true, NotifyUser: true},
			Actions: []model.CaseTemplateLevelAction{{ActionType: model.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":60}`, MaxRetries: 2}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &quack.DiscordGuildAuthorization{
		Guild:  quack.DiscordBotGuild{ID: "111111111111111111", Name: "Guild", OwnerID: "owner"},
		Bot:    quack.DiscordMemberAuthorization{DiscordUserID: "quack", PermissionBits: uint64(discordgo.PermissionAdministrator), TopRolePosition: 20, Present: true, Bot: true},
		Target: &quack.DiscordMemberAuthorization{DiscordUserID: "target", Present: true, TopRolePosition: 1},
	}
	services := quack.New(config.Default(), repository, fakeDiscordClient{botGuild: &snapshot.Guild, authorization: snapshot}, nil, nil)
	link := "https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"
	services.Cases.WithEvidenceCapture(quack.NewEvidenceService(&fakeEvidenceClient{message: quack.DiscordMessageSnapshot{
		GuildID: "111111111111111111", ChannelID: "222222222222222222", MessageID: "333333333333333333", AuthorDiscordUserID: "target", URL: link, Content: "evidence", CreatedAt: time.Now().UTC(),
	}}, repository))
	input := quack.CaseInput{
		TemplateID: template.Template.ID, TargetDiscordUserID: "target",
		Source: model.CaseSourceHoneypot, ContextChannelDiscordID: "222222222222222222",
		ContextMessageDiscordID: "333333333333333333", ContextURL: link,
		IdempotencyKey: "honeypot:" + guild.ID + ":333333333333333333",
	}
	created, err := services.Cases.CreateSystemHoneypot(ctx, guild.ID, input)
	if err != nil {
		t.Fatalf("create system honeypot case: %v", err)
	}
	// A process may lose its module receipt after this transaction commits.
	// Read-only reconciliation must find it without fresh permission or effects.
	priorBits := snapshot.Bot.PermissionBits
	snapshot.Bot.PermissionBits = 0
	recovered, lookupErr := services.Cases.FindSystemHoneypot(ctx, guild.ID, input)
	if lookupErr != nil || recovered == nil || recovered.ID != created.ID {
		t.Fatalf("saved incident recovery: %+v %v", recovered, lookupErr)
	}
	snapshot.Bot.PermissionBits = priorBits
	replayed, err := services.Cases.CreateSystemHoneypot(ctx, guild.ID, input)
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("idempotent replay changed result: got=%+v err=%v", replayed, err)
	}
	cases, err := repository.ListCases(ctx, guild.ID)
	if err != nil || len(cases) != 1 {
		t.Fatalf("expected one case: %+v err=%v", cases, err)
	}
	if cases[0].Source != model.CaseSourceHoneypot || cases[0].ModeratorDiscordUserID != "" || cases[0].ContextMessageDiscordID != "333333333333333333" || cases[0].CorrelationID != "corr-honeypot" {
		t.Fatalf("system attribution or context changed: %+v", cases[0])
	}
	events, _ := repository.ListCaseEvents(ctx, created.ID)
	actions, _ := repository.ListCaseActionExecutions(ctx, created.ID)
	evidence, _, _ := repository.ListCaseEvidence(ctx, created.ID)
	notification, _ := repository.GetCaseNotification(ctx, created.ID)
	if len(events) != 1 || events[0].ActorType != "system" || events[0].ActorDiscordUserID != "" || len(actions) != 1 || actions[0].Status != model.ActionExecutionPending || len(evidence) != 1 || notification == nil || notification.Status != model.NotificationPending {
		t.Fatalf("normal path parity missing: events=%+v actions=%+v evidence=%+v notification=%+v", events, actions, evidence, notification)
	}
	audits, _ := repository.ListAuditLogEntries(ctx, guild.ID)
	if len(audits) != 1 || audits[0].Source != model.AuditSourceHoneypot || audits[0].ActorDiscordUserID != "" || audits[0].Action != "case.create" {
		t.Fatalf("case audit fabricated staff identity: %+v", audits)
	}

	if _, err := services.Cases.CreateSystemHoneypot(ctx, guild.ID, quack.CaseInput{TemplateID: template.Template.ID, TargetDiscordUserID: "target", Source: model.CaseSourceDashboard}); !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("non-honeypot system misuse accepted: %v", err)
	}

	snapshot.Bot.PermissionBits = 0
	input.ContextMessageDiscordID = "444444444444444444"
	input.IdempotencyKey = "honeypot:" + guild.ID + ":444444444444444444"
	input.ContextURL = ""
	if _, err := services.Cases.CreateSystemHoneypot(ctx, guild.ID, input); !errors.Is(err, quack.ErrAuthorizationDenied) {
		t.Fatalf("unsafe bot capability accepted: %v", err)
	}
	cases, _ = repository.ListCases(ctx, guild.ID)
	if len(cases) != 1 {
		t.Fatalf("failed system preflight partially committed: %+v", cases)
	}
	audits, _ = repository.ListAuditLogEntries(ctx, guild.ID)
	if len(audits) != 1 || audits[0].Action != "case.create" {
		t.Fatalf("failed preflight added a moderation event: %+v", audits)
	}
}
