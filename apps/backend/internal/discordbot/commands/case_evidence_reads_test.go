package commands

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// TestNativeEvidenceReadsPreservePages verifies initial and subsequent evidence
// pages match the existing renderer without loading unrelated case histories.
func TestNativeEvidenceReadsPreservePages(t *testing.T) {
	repository, services, _ := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	item := model.Case{ULIDModel: model.ULIDModel{ID: "native-evidence-read"}, GuildID: guild.Guild.ID, CaseNumber: 1, Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, TemplateSnapshotJSON: "{}", MetadataJSON: "{}", ContextValuesJSON: "[]"}
	if err := repository.DB().Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := model.CaseEvidenceSnapshot{ULIDModel: model.ULIDModel{ID: "native-snapshot"}, CaseID: item.ID, GuildID: item.GuildID, Content: strings.Repeat("Original evidence text. ", 180), EmbedsJSON: "[]", CaptureWarning: "Copy unavailable"}
	if err := repository.DB().Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	full, err := services.Cases.Get(context.Background(), guild, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Callback().Query().Before("gorm:query").Register("reject_unrelated_evidence_reads", func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "case_events", "case_action_executions", "case_action_attempts", "case_notifications":
			tx.AddError(errors.New("unrelated evidence page query"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	for _, page := range []int{1, 2} {
		interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
		interaction.Type = discordgo.InteractionMessageComponent
		handler := handleCaseEvidenceComponent
		payload := item.ID
		if page == 2 {
			handler = pageEvidence(1)
			payload = "1|" + item.ID
		}
		interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "evidence", Version: "v1", Payload: payload})}
		result := handler(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
		responder := &fakeResponder{}
		if err := result.Task(context.Background(), responder); err != nil {
			t.Fatal(err)
		}
		actual := responder.edit
		if page == 2 {
			actual = responder.updated
		}
		want := ui.EditMessage(views.CaseEvidencePage(full, page, ""))
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("page %d output changed: %+v %+v", page, actual, want)
		}
	}
}
