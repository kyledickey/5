package commands

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/quackdiscord/bot/internal/quack/model"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestEvidenceNavigationRechecksAuthority prevents a previously authorized
// private evidence message from retaining access after a moderator loses roles.
func TestEvidenceNavigationRechecksAuthority(t *testing.T) {
	_, services, _ := newCaseCommandHarnessWithLivePermissions(t, 0)
	services.Config.ApplicationBaseURL = "https://dashboard.example"
	interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
	interaction.Type = discordgo.InteractionMessageComponent
	interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "evidence_next", Version: "v1", Payload: "1|case-1"})}
	result := pageEvidence(1)(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	if result.Task == nil || result.Response.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatal("evidence navigation did not acknowledge before its live lookup")
	}
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatalf("stale interaction permissions were trusted: %v", err)
	}
	if responder.editCount != 0 || responder.followup.Content != "" {
		t.Fatal("revoked moderator received evidence")
	}
}

// TestEvidenceContextWithoutAttachmentsCanNavigate exercises the real loader and
// button handler so a text-only case never produces an unusable snapshot zero ID.
func TestEvidenceContextWithoutAttachmentsCanNavigate(t *testing.T) {
	repository, services, _ := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	values, _ := json.Marshal([]quack.CaseContextValueResponse{{Label: "Context", Value: strings.Repeat("Saved note. ", 300) + "FINAL NOTE"}})
	item := model.Case{ULIDModel: model.ULIDModel{ID: "context-only"}, GuildID: guild.Guild.ID, CaseNumber: 1, ContextValuesJSON: string(values), TemplateSnapshotJSON: "{}", MetadataJSON: "{}"}
	if err := repository.DB().Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	detail, err := services.Cases.GetEvidencePage(context.Background(), guild, item.ID, 1)
	if err != nil || detail.Position != 1 {
		t.Fatalf("invalid context-only position: %+v %v", detail, err)
	}
	interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
	interaction.Type = discordgo.InteractionMessageComponent
	interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "evidence_next", Version: "v1", Payload: "1:1|" + item.ID})}
	result := pageEvidence(1)(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	responder := &fakeResponder{}
	if result.Task == nil {
		t.Fatal("context navigation rejected")
	}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if responder.updated.Content == nil || !strings.Contains(*responder.updated.Content, "Saved note") {
		t.Fatal("context page missing")
	}
}
