package commands

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// TestCaseAttachmentOptionCreatesCaseWithVisibleCopyFailure exercises the real
// Discord attachment option type and resolved payload rather than calling core directly.
func TestCaseAttachmentOptionCreatesCaseWithVisibleCopyFailure(t *testing.T) {
	repository, services, templateID := newCaseCommandHarness(t)
	command := caseAddInteraction(templateID, "target", uint64(discordgo.PermissionModerateMembers))
	data := command.ApplicationCommandData()
	fileOption := &discordgo.ApplicationCommandInteractionDataOption{Type: discordgo.ApplicationCommandOptionAttachment, Name: "file", Value: "upload"}
	data.Options[0].Options = append(data.Options[0].Options, fileOption)
	data.Resolved = &discordgo.ApplicationCommandInteractionDataResolved{Attachments: map[string]*discordgo.MessageAttachment{"upload": {ID: "upload", Filename: "screenshot.png", ContentType: "image/png", Size: 12, URL: "https://cdn.discordapp.com/attachments/channel/upload/screenshot.png"}}}
	command.Data = data
	result := HandleCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: command})
	if result.Task == nil {
		t.Fatalf("attachment creation was not scheduled: %+v", result)
	}
	if err := result.Task(context.Background(), &fakeResponder{}); err != nil {
		t.Fatal(err)
	}
	cases, err := repository.ListCases(context.Background(), storeGuildID(t, repository, "guild-1"))
	if err != nil || len(cases) != 1 {
		t.Fatalf("attachment blocked creation: %+v err=%v", cases, err)
	}
	snapshots, files, err := repository.ListCaseEvidence(context.Background(), cases[0].ID)
	if err != nil || len(snapshots) != 1 || len(files) != 1 || files[0].Filename != "screenshot.png" || files[0].Warning == "" {
		t.Fatalf("unconfigured preservation did not retain a failure receipt: snapshots=%+v files=%+v err=%v", snapshots, files, err)
	}
	command.ID = "append-upload"
	data.Options = []*discordgo.ApplicationCommandInteractionDataOption{{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "evidence", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Type: discordgo.ApplicationCommandOptionString, Name: "case", Value: cases[0].ID}, fileOption}}}
	command.Data = data
	result = HandleCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: command})
	if result.Task == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("staff evidence response is not private: %+v", result)
	}
	if err := result.Task(context.Background(), &fakeResponder{}); err != nil {
		t.Fatal(err)
	}
	_, files, err = repository.ListCaseEvidence(context.Background(), cases[0].ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("existing-case upload missing: %+v err=%v", files, err)
	}
}
