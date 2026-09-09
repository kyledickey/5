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

// TestNativeDetailReadsPreservePages verifies slash reads and native navigation
// retain their output without loading action attempts.
func TestNativeDetailReadsPreservePages(t *testing.T) {
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
	if err := repository.DB().Callback().Query().Before("gorm:query").Register("reject_native_attempt_reads", func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "case_action_attempts":
			tx.AddError(errors.New("native detail attempt query"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	for _, page := range []int{1, 2} {
		interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
		interaction.Type = discordgo.InteractionMessageComponent
		handler := pageCaseRecord(0, views.CaseDetailPage)
		payload := "1|" + item.ID
		if page == 2 {
			handler = pageCaseRecord(1, views.CaseDetailPage)
			payload = "1|" + item.ID
		}
		interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "evidence", Version: "v1", Payload: payload})}
		ctx := ui.Context{Context: context.Background(), Services: services, Interaction: interaction}
		result := handler(ctx)
		if page == 1 {
			result = handleCaseStaffSubcommand(ctx, discordgo.ApplicationCommandInteractionData{Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "view", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "case", Value: item.ID}}}}})
		}
		responder := &fakeResponder{}
		if err := result.Task(context.Background(), responder); err != nil {
			t.Fatal(err)
		}
		actual := responder.updated
		if page == 1 {
			actual = responder.edit
		}
		want := ui.EditMessage(views.CaseDetailPage(full, page, ""))
		if page == 1 {
			want = ui.EditMessage(views.PublicCaseDetail(full))
			if actual.Content != nil && strings.Contains(*actual.Content, "Original evidence") {
				t.Fatal("public case command leaked evidence")
			}
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("page %d output changed: %+v %+v", page, actual, want)
		}
	}
}
