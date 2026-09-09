package commands

import (
	"context"
	"encoding/json"
	"strings"
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
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
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
	if result.Task == nil || result.Response == nil || (result.Response.Data != nil && result.Response.Data.Flags&discordgo.MessageFlagsEphemeral != 0) {
		t.Fatalf("evidence command did not acknowledge publicly: %+v", result)
	}
	responder = &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(responder.edit)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"screenshot.png", "cdn.discordapp.com", files[0].Warning} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("public evidence result leaked %q: %s", private, encoded)
		}
	}
	if responder.channelPublishes != 0 || responder.webhookFollowups != 0 || responder.editCount != 1 {
		t.Fatal("evidence result duplicated", responder)
	}
	_, files, err = repository.ListCaseEvidence(context.Background(), cases[0].ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("existing-case upload missing: %+v err=%v", files, err)
	}
}
