package quack_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestCaseProceedsWithoutContextOrWorkingEvidence exercises the moderation
// boundary: evidence failures must not discard the selected enforcement action.
func TestCaseProceedsWithoutContextOrWorkingEvidence(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	guildID := "111111111111111111"
	admin := templateGuildContext(t, repository, guildID, "admin", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, repository, guildID, "mod", uint64(discordgo.PermissionModerateMembers))
	input := validTemplateInput("optional-evidence")
	input.Levels = []quack.TemplateLevelInput{{Name: "Ban", Position: 1, IsDefault: true, NotifyUser: true, Actions: []quack.TemplateActionInput{{ActionType: model.ActionBanUser}}}}
	input.ContextFields = []quack.TemplateContextFieldInput{{Key: "message", Label: "Message", FieldType: model.ContextFieldMessageLink, Position: 1, Required: true}}
	template := createAppTemplate(t, ctx, repository, admin, input)
	for _, scenario := range []struct {
		name   string
		links  []string
		client quack.DiscordEvidenceClient
	}{
		{name: "no context"},
		{name: "missing adapter", links: []string{"https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"}},
		{name: "deleted message", links: []string{"https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"}, client: unavailableEvidenceClient{err: &quack.EvidenceUnavailableError{Outcome: "deleted"}}},
		{name: "transport failure", links: []string{"https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"}, client: unavailableEvidenceClient{err: errors.New("transport secret must not be exposed")}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := quack.NewCaseService(repository, nil).WithEvidenceCapture(quack.NewEvidenceService(scenario.client, repository))
			created, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target", EvidenceLinks: scenario.links})
			if err != nil {
				t.Fatalf("optional evidence blocked case: %v", err)
			}
			detail, err := service.Get(ctx, moderator, created.ID)
			if err != nil || detail.Validity != model.CaseValidityValid || len(detail.Actions) != 1 {
				t.Fatalf("moderation decision lost: detail=%+v err=%v", detail, err)
			}
			if len(scenario.links) != 0 && (!created.EvidenceIncomplete || !detail.EvidenceIncomplete || len(detail.Evidence) != 1 || detail.Evidence[0].CaptureWarning == "") {
				t.Fatalf("capture failure was not visible: created=%+v detail=%+v", created, detail)
			}
		})
	}
}

type unavailableEvidenceClient struct{ err error }

func (f unavailableEvidenceClient) FetchMessageEvidence(context.Context, quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	return nil, f.err
}
func (unavailableEvidenceClient) PreserveEvidenceAttachment(context.Context, string, string, quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	return nil, nil
}
func (unavailableEvidenceClient) EnsureEvidenceChannel(context.Context, string, string) (string, error) {
	return "", nil
}

type evidenceClientFixture struct {
	message   quack.DiscordMessageSnapshot
	preserved quack.PreservedDiscordAttachment
}

func (f evidenceClientFixture) FetchMessageEvidence(context.Context, quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	message := f.message
	return &message, nil
}

func (f evidenceClientFixture) PreserveEvidenceAttachment(context.Context, string, string, quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	preserved := f.preserved
	return &preserved, nil
}

func (evidenceClientFixture) EnsureEvidenceChannel(context.Context, string, string) (string, error) {
	return "evidence-channel", nil
}

func TestParseDiscordMessageLinkRejectsLookalikesAndCrossGuildCapture(t *testing.T) {
	valid := "https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"
	ref, err := quack.ParseDiscordMessageLink(valid)
	if err != nil || ref.MessageID != "333333333333333333" {
		t.Fatalf("valid link: %+v err=%v", ref, err)
	}
	for _, invalid := range []string{"https://discord.example/channels/111111111111111111/222222222222222222/333333333333333333", "https://discord.com/channels/@me/2/3", "javascript:alert(1)"} {
		if _, err := quack.ParseDiscordMessageLink(invalid); !errors.Is(err, quack.ErrEvidenceValidation) {
			t.Fatalf("accepted invalid link %q: %v", invalid, err)
		}
	}
	service := quack.NewEvidenceService(unavailableEvidenceClient{}, nil)
	if _, err := service.Capture(context.Background(), "999999999999999999", "actor", "target", "", []string{valid}); !errors.Is(err, quack.ErrEvidenceValidation) {
		t.Fatalf("cross-guild capture accepted: %v", err)
	}
}

func TestUnavailableEvidenceDoesNotRequireFallbackContext(t *testing.T) {
	link := "https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"
	for _, outcome := range []string{"deleted", "inaccessible"} {
		service := quack.NewEvidenceService(unavailableEvidenceClient{err: &quack.EvidenceUnavailableError{Outcome: outcome, Message: "message " + outcome}}, nil)
		captured, err := service.Capture(context.Background(), "111111111111111111", "actor", "target", "", []string{link})
		if err != nil || len(captured.Snapshots) != 1 || captured.Snapshots[0].CaptureOutcome != outcome || captured.Snapshots[0].MessageCreatedAt.IsZero() {
			t.Fatalf("partial %s capture: %+v err=%v", outcome, captured, err)
		}
	}
}

func TestLiveEvidencePreservesSupportedAndRetainsUnsupportedOrOversizedMetadata(t *testing.T) {
	const (
		guildID  = "111111111111111111"
		channel  = "222222222222222222"
		message  = "333333333333333333"
		targetID = "444444444444444444"
	)
	link := "https://discord.com/channels/" + guildID + "/" + channel + "/" + message
	client := evidenceClientFixture{
		message: quack.DiscordMessageSnapshot{
			GuildID: guildID, ChannelID: channel, MessageID: message,
			AuthorDiscordUserID: targetID, URL: link, Content: "live evidence", CreatedAt: time.Now().UTC(),
			Attachments: []quack.DiscordAttachmentSnapshot{
				{ID: "supported", Filename: "proof.png", ContentType: "image/png", SizeBytes: 1024, URL: "https://cdn.example/proof"},
				{ID: "unsupported", Filename: "archive.zip", ContentType: "application/zip", SizeBytes: 1024, URL: "https://cdn.example/archive"},
				{ID: "oversized", Filename: "large.png", ContentType: "image/png", SizeBytes: 26 << 20, URL: "https://cdn.example/large"},
			},
		},
		preserved: quack.PreservedDiscordAttachment{URL: "https://cdn.example/preserved", MessageID: "copy-message", AttachmentID: "copy-attachment"},
	}
	captured, err := quack.NewEvidenceService(client, nil).Capture(context.Background(), guildID, "actor", targetID, "evidence-channel", []string{link})
	if err != nil {
		t.Fatalf("capture live evidence: %v", err)
	}
	if len(captured.Snapshots) != 1 || captured.Snapshots[0].CaptureOutcome != "captured" || len(captured.Attachments) != 3 {
		t.Fatalf("unexpected live capture: %+v", captured)
	}
	if captured.Attachments[0].CopyOutcome != "preserved" || captured.Attachments[0].PreservedURL == "" {
		t.Fatalf("supported attachment was not preserved: %+v", captured.Attachments[0])
	}
	if captured.Attachments[1].CopyOutcome != "metadata_only" || !strings.Contains(captured.Attachments[1].Warning, "type") {
		t.Fatalf("unsupported attachment lost its warning/metadata: %+v", captured.Attachments[1])
	}
	if captured.Attachments[2].CopyOutcome != "metadata_only" || !strings.Contains(captured.Attachments[2].Warning, "size") {
		t.Fatalf("oversized attachment lost its warning/metadata: %+v", captured.Attachments[2])
	}
}

func FuzzParseDiscordMessageLink(f *testing.F) {
	f.Add("https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333")
	f.Add("not-a-url")
	f.Fuzz(func(t *testing.T, value string) {
		ref, err := quack.ParseDiscordMessageLink(value)
		if err == nil && (ref.GuildID == "" || ref.ChannelID == "" || ref.MessageID == "") {
			t.Fatalf("successful parse returned empty identity: %+v", ref)
		}
	})
}
