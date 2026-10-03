package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRegisterComponentsInstallsTicketControls(t *testing.T) {
	registry := interactions.NewComponentRegistry()
	runtime := &Runtime{Tickets: &tickets.Service{}, TicketDiscord: &tickets.DiscordAdapter{}}
	if err := runtime.RegisterComponents(registry); err != nil {
		t.Fatalf("register ticket components: %v", err)
	}
	for _, action := range []string{"open", "queue", "view", "close", "repair"} {
		customID := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: action, Version: "v1", Payload: "ticket-id"})
		if _, ok, err := registry.LookupComponent(customID); err != nil || !ok {
			t.Fatalf("action %s ok=%v err=%v", action, ok, err)
		}
	}
	if len(ticketCloseComponents("ticket-id")) != 1 {
		t.Fatal("missing ticket close control")
	}
	replyID := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "reply", Version: "v1", Payload: "ticket-id"})
	if _, ok, err := registry.LookupComponent(replyID); err != nil || ok {
		t.Fatalf("reply modal control remains: ok=%v err=%v", ok, err)
	}
}

func TestRegisterComponentsRequiresTicketRuntime(t *testing.T) {
	if err := (&Runtime{}).RegisterComponents(interactions.NewComponentRegistry()); err == nil {
		t.Fatal("unconfigured ticket runtime registered components")
	}
}

// TestTicketCloseNoticeReconcilesOnlyBotTranscript ensures an ambiguous retry
// cannot send again or trust a member-authored lookalike as a delivery receipt.
func TestTicketCloseNoticeReconcilesOnlyBotTranscript(t *testing.T) {
	session, _ := discordgo.New("Bot test")
	session.State.User = &discordgo.User{ID: "bot"}
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/users/@me/channels"):
			body = `{"id":"dm"}`
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/dm/messages"):
			messages := []*discordgo.Message{
				{ID: "spoof", Content: "Ticket ticket", Author: &discordgo.User{ID: "member"}, Attachments: []*discordgo.MessageAttachment{{Filename: "ticket-ticket.txt"}}},
				{ID: "receipt", Content: "Ticket ticket", Author: &discordgo.User{ID: "bot"}, Attachments: []*discordgo.MessageAttachment{{Filename: "ticket-ticket.txt"}}},
			}
			encoded, _ := json.Marshal(messages)
			body = string(encoded)
		default:
			t.Fatalf("unexpected DM send during reconciliation: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	receipt, err := (ticketDiscordClient{session: session}).DeliverTicketCloseNotice(context.Background(), &tickets.Ticket{ID: "ticket", OwnerDiscordUserID: "member"}, &tickets.Transcript{Content: "private"}, true)
	if err != nil || receipt != "receipt" {
		t.Fatalf("wrong reconciliation: %q %v", receipt, err)
	}
}

// TestTicketCloseNoticeIncludesMemberTranscript checks the actual multipart DM:
// the server and ticket identify the conversation, and staff links never leak.
func TestTicketCloseNoticeIncludesMemberTranscript(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Guild{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	session, _ := discordgo.New("Bot test")
	session.State.User = &discordgo.User{ID: "bot"}
	if err := session.State.GuildAdd(&discordgo.Guild{ID: "guild", Name: "Duck Club"}); err != nil {
		t.Fatal(err)
	}
	sent := 0
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/users/@me/channels"):
			body = `{"id":"dm"}`
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/dm/messages"):
			sent++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			var payload discordgo.MessageSend
			if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &payload); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(payload.Content, "Duck Club") || !strings.Contains(payload.Content, "Ticket ticket") || strings.Contains(payload.Content, "staff-secret") {
				t.Fatalf("bad close notice: %s", payload.Content)
			}
			if payload.Flags&discordgo.MessageFlagsEphemeral != 0 {
				t.Fatal("DM has ephemeral flag")
			}
			files := r.MultipartForm.File["files[0]"]
			if len(files) != 1 || files[0].Filename != "ticket-ticket.txt" {
				t.Fatal("missing transcript")
			}
			file, err := files[0].Open()
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			content, err := io.ReadAll(file)
			if err != nil || string(content) != "member conversation" {
				t.Fatal("wrong transcript", err)
			}
			body = `{"id":"notice","attachments":[{"filename":"ticket-ticket.txt"}]}`
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	receipt, err := (ticketDiscordClient{session: session, resolver: guildResolver{db: db}}).DeliverTicketCloseNotice(context.Background(), &tickets.Ticket{ID: "ticket", GuildID: "internal", OwnerDiscordUserID: "member", TranscriptURL: "staff-secret"}, &tickets.Transcript{Content: "member conversation"}, false)
	if err != nil || receipt != "notice" || sent != 1 {
		t.Fatalf("DM failed: %q %v", receipt, err)
	}
}

// progressResponder emulates Discord rejecting edits after its channel disappears.
type progressResponder struct {
	ui.Responder
	deleted  bool
	inThread bool
	messages []string
}

// EditOriginal records only still-deliverable feedback.
func (r *progressResponder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	if r.deleted && r.inThread {
		return nil, errors.New("unknown channel")
	}
	if edit.Content != nil {
		r.messages = append(r.messages, *edit.Content)
	}
	return &discordgo.Message{}, nil
}

// progressCloser invokes progress before deletion and can inject deletion failure.
type progressCloser struct {
	responder  *progressResponder
	failDelete bool
}

// CloseWithProgress preserves the tested adapter callback contract.
func (c progressCloser) CloseWithProgress(_ context.Context, _ tickets.Actor, _ string, progress func(*tickets.Ticket) error) (*tickets.Ticket, error) {
	ticket := &tickets.Ticket{ID: "ticket", Status: tickets.StatusResolved, ThreadDiscordChannelID: "thread", TranscriptURL: "saved"}
	if err := progress(ticket); err != nil {
		return ticket, err
	}
	if c.failDelete {
		return ticket, errors.New("cannot delete")
	}
	c.responder.deleted = true
	return ticket, nil
}

// TestCloseFeedbackSurvivesOriginDeletion proves no impossible final edit is made,
// while outside-thread confirmation and failed-deletion retry remain available.
func TestCloseFeedbackSurvivesOriginDeletion(t *testing.T) {
	for _, scenario := range []struct {
		name, origin string
		failure      bool
		count        int
		want         string
	}{
		{"inside", "thread", false, 1, "is closing"},
		{"outside", "entry", false, 1, "Ticket closed"},
		{"delete_failure", "thread", true, 2, "cleanup did not finish"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			responder := &progressResponder{inThread: scenario.origin == "thread"}
			err := closeTicketWithFeedback(context.Background(), responder, progressCloser{responder: responder, failDelete: scenario.failure}, tickets.Actor{}, "ticket", scenario.origin)
			if err != nil {
				t.Fatal(err)
			}
			if len(responder.messages) != scenario.count || !strings.Contains(responder.messages[len(responder.messages)-1], scenario.want) {
				t.Fatal(responder.messages)
			}
			if scenario.failure {
				for _, message := range responder.messages {
					if strings.Contains(message, "Ticket closed.") {
						t.Fatal("false completion", message)
					}
				}
			}
		})
	}
}

// TestTicketDetailLifecycle prevents stale thread links and impossible closed controls.
func TestTicketDetailLifecycle(t *testing.T) {
	ticket := &tickets.Ticket{CloseNoticeDelivered: true, ID: "ticket", OwnerDiscordUserID: "owner", ThreadDiscordChannelID: "deleted-thread", Status: tickets.StatusResolved, TranscriptURL: "https://discord.com/channels/guild/staff/message"}
	transcript := &tickets.Transcript{Content: "private retained content"}
	for _, pending := range []bool{false, true} {
		message := ticketDetailMessage(ticket, nil, tickets.Actor{DiscordUserID: "owner", CanManage: true}, pending, transcript, 0)
		if strings.Contains(message.Content, "deleted-thread") || strings.Contains(message.Content, "https://discord.com") {
			t.Fatal("owner received unusable navigation", message.Content)
		}
		if len(message.Components) != map[bool]int{false: 0, true: 1}[pending] {
			t.Fatal("incorrect cleanup controls", message.Components)
		}
		for _, row := range message.Components {
			for _, component := range row.(discordgo.ActionsRow).Components {
				if component.(discordgo.Button).Label != "Finish closing" {
					t.Fatal("impossible closed action", component)
				}
			}
		}
		if len(message.Files) != 1 {
			t.Fatal("owner transcript missing")
		}
		body, err := io.ReadAll(message.Files[0].Reader)
		if err != nil || string(body) != transcript.Content {
			t.Fatal("transcript changed", err)
		}
	}
	staff := ticketDetailMessage(ticket, nil, tickets.Actor{CanModerate: true}, false, nil, 0)
	if !strings.Contains(staff.Content, ticket.TranscriptURL) {
		t.Fatal("staff queue navigation missing")
	}
	ticket.Status = tickets.StatusOpen
	open := ticketDetailMessage(ticket, nil, tickets.Actor{CanManage: true}, false, nil, 0)
	if !strings.Contains(open.Content, "<#deleted-thread>") || len(open.Components[0].(discordgo.ActionsRow).Components) != 2 {
		t.Fatal("open lifecycle controls missing")
	}
	ticket.Status = tickets.StatusCancelled
	cancelled := ticketDetailMessage(ticket, nil, tickets.Actor{}, false, nil, 0)
	if len(cancelled.Components) != 0 || strings.Contains(cancelled.Content, "<#") {
		t.Fatal("imported closed ticket has active controls")
	}
}

// TestTicketDetailHistoryPages preserves long native history under Discord's limit.
func TestTicketDetailHistoryPages(t *testing.T) {
	events := []tickets.Event{{Body: strings.Repeat("🙂*\n", 2000)}, {Body: "last historical entry"}}
	pages := ticketHistoryPages(events)
	if len(pages) < 2 || !strings.Contains(pages[len(pages)-1], "last historical entry") {
		t.Fatal("history lost")
	}
	ticket := &tickets.Ticket{ID: "00000000-0000-0000-0000-000000000000", OwnerDiscordUserID: "12345678901234567890", Status: tickets.StatusResolved}
	for page := range pages {
		message := ticketDetailMessage(ticket, events, tickets.Actor{}, true, nil, page)
		if n := len(utf16.Encode([]rune(message.Content))); n > 2000 {
			t.Fatalf("page %d has %d units", page, n)
		}
		if len(message.Files) != 0 {
			t.Fatal("native history unexpectedly attached")
		}
	}
	id, page := ticketDetailPayload(ticket.ID + "~3")
	if id != ticket.ID || page != 3 {
		t.Fatal("page payload failed")
	}
	id, page = ticketDetailPayload(ticket.ID)
	if id != ticket.ID || page != 0 {
		t.Fatal("legacy view button failed")
	}
}

// TestTicketHistoryUpdatesOnlyPrivateViews refreshes stale entry receipts and
// pages without replacing public controls with private ticket contents.
func TestTicketHistoryUpdatesOnlyPrivateViews(t *testing.T) {
	for _, scenario := range []struct {
		payload  string
		flags    discordgo.MessageFlags
		response discordgo.InteractionResponseType
	}{
		{"ticket", discordgo.MessageFlagsEphemeral, discordgo.InteractionResponseDeferredMessageUpdate},
		{"ticket", 0, discordgo.InteractionResponseDeferredChannelMessageWithSource},
		{"ticket~1", discordgo.MessageFlagsEphemeral, discordgo.InteractionResponseDeferredMessageUpdate},
		{"ticket~1", 0, discordgo.InteractionResponseDeferredChannelMessageWithSource},
	} {
		ctx := ui.Context{Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
			GuildID: "guild", Type: discordgo.InteractionMessageComponent,
			Message: &discordgo.Message{Flags: scenario.flags},
			Data:    discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "view", Version: "v1", Payload: scenario.payload})},
		}}}
		result := (&Runtime{}).viewTicketComponent(ctx)
		if result.Response.Type != scenario.response {
			t.Fatalf("payload %s flags %d: got %d", scenario.payload, scenario.flags, result.Response.Type)
		}
	}
}

// TestTicketMemberDMRecoveryIsManagerOnly keeps delivery repair off ordinary member views.
func TestTicketMemberDMRecoveryIsManagerOnly(t *testing.T) {
	ticket := &tickets.Ticket{ID: "ticket", Status: tickets.StatusResolved}
	owner := ticketDetailMessage(ticket, nil, tickets.Actor{}, false, nil, 0)
	admin := ticketDetailMessage(ticket, nil, tickets.Actor{CanManage: true}, false, nil, 0)
	if len(owner.Components) != 0 || len(admin.Components) != 1 || admin.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button).Label != "Retry member DM" {
		t.Fatal("incorrect DM recovery visibility")
	}
}

// TestExistingTicketFeedbackProvidesRecovery checks each member reservation state
// offers a useful next step without linking a possibly deleted closing thread.
func TestExistingTicketFeedbackProvidesRecovery(t *testing.T) {
	opening := existingTicketMessage(nil)
	if !strings.Contains(opening.Content, "still opening") || len(opening.Components) != 0 {
		t.Fatal("invalid provisional feedback")
	}
	open := existingTicketMessage(&tickets.Ticket{ID: "ticket", Status: tickets.StatusOpen, ThreadDiscordChannelID: "thread"})
	if !strings.Contains(open.Content, "<#thread>") {
		t.Fatal("open ticket lacks link")
	}
	closing := existingTicketMessage(&tickets.Ticket{ID: "ticket", Status: tickets.StatusResolved, ThreadDiscordChannelID: "thread"})
	if strings.Contains(closing.Content, "<#thread>") || len(closing.Components) != 1 {
		t.Fatal("invalid cleanup feedback")
	}
	button := closing.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	id, err := ui.DecodeCustomID(button.CustomID)
	if err != nil || id.Namespace != "ticket" || id.Action != "close" || id.Payload != "ticket" {
		t.Fatalf("invalid recovery control: %+v %v", id, err)
	}
}

// TestCloseFailurePreservesUnknownDelivery keeps wrapped uncertainty visible even
// with a returned ticket, without claiming nondelivery or suggesting blind retry.
func TestCloseFailurePreservesUnknownDelivery(t *testing.T) {
	for _, ticket := range []*tickets.Ticket{
		nil,
		{ID: "ticket", Status: tickets.StatusOpen},
		{ID: "ticket", Status: tickets.StatusResolved},
		{ID: "ticket", Status: tickets.StatusResolved, TranscriptURL: "saved"},
	} {
		message := ticketCloseFailureMessage(ticket, fmt.Errorf("publish queue: %w", tickets.ErrQueueDeliveryUnknown))
		if !strings.Contains(message.Content, "could not be confirmed") || !strings.Contains(message.Content, "duplicate") || !strings.Contains(message.Content, "administrator") {
			t.Fatalf("delivery uncertainty was masked: %+v", message)
		}
		if strings.Contains(message.Content, "Try again") || strings.Contains(message.Content, "could not be saved") {
			t.Fatalf("uncertain delivery offered blind retry or claimed failure: %+v", message)
		}
		if ticket == nil && len(message.Components) != 0 {
			t.Fatal("missing ticket exposed controls")
		}
		if ticket != nil {
			if len(message.Components) != 1 {
				t.Fatal("missing recovery navigation")
			}
			button := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
			id, err := ui.DecodeCustomID(button.CustomID)
			if err != nil || id.Action != "view" || id.Payload != ticket.ID {
				t.Fatal("uncertainty offered a mutation instead of private detail")
			}
		}
		if !message.Ephemeral {
			t.Fatal("uncertain delivery feedback was not private")
		}
	}
}

// TestCloseFailureFeedbackDistinguishesConfirmedProgress avoids claiming an
// upload succeeded before its receipt, while withholding controls on denied access.
func TestCloseFailureFeedbackDistinguishesConfirmedProgress(t *testing.T) {
	for _, scenario := range []struct {
		ticket  *tickets.Ticket
		want    string
		buttons int
	}{
		{nil, "permission", 0},
		{&tickets.Ticket{ID: "ticket", Status: tickets.StatusOpen}, "could not finish closing", 1},
		{&tickets.Ticket{ID: "ticket", Status: tickets.StatusResolved}, "thread has been kept", 1},
		{&tickets.Ticket{ID: "ticket", Status: tickets.StatusResolved, TranscriptURL: "saved"}, "cleanup did not finish", 1},
	} {
		message := ticketCloseFailureMessage(scenario.ticket, tickets.ErrPermissionDenied)
		if !strings.Contains(message.Content, scenario.want) || len(message.Components) != scenario.buttons {
			t.Fatalf("incorrect progress message: %+v", message)
		}
	}
}

// TestTicketJournalCaptureWithoutLogging proves gateway text retention needs no
// logging service or integration guild lookup, including ordinary bot replies.
func TestTicketJournalCaptureWithoutLogging(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tickets.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(modules.SchemaTypes()...); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(tickets.SchemaTypes()...); err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), tickets.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	service := tickets.NewService(registry, tickets.NewStore(db), nil)
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID = "entry"
	settings.QueueChannelDiscordID = "queue"
	actor := tickets.Actor{GuildID: "guild", DiscordUserID: "owner", CanManage: true}
	if _, err := service.UpdateSettings(context.Background(), actor, true, settings); err != nil {
		t.Fatal(err)
	}
	ticket, err := service.Open(context.Background(), actor, "thread")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{Tickets: service}
	runtime.recordTicketMessage(&discordgo.MessageCreate{Message: &discordgo.Message{ID: "original", GuildID: "discord-guild", ChannelID: "thread", Content: "deleted bot reply", Author: &discordgo.User{ID: "bot", Bot: true}}})
	runtime.recordTicketMessage(&discordgo.MessageCreate{Message: &discordgo.Message{ID: "unrelated", GuildID: "discord-guild", ChannelID: "other", Content: "private unrelated", Author: &discordgo.User{ID: "member"}}})
	if _, err := service.ResolveNativeTranscript(context.Background(), actor, ticket.ID, nil); err != nil {
		t.Fatal(err)
	}
	transcript, err := service.Transcript(context.Background(), actor, ticket.ID)
	if err != nil || !strings.Contains(transcript.Content, "deleted bot reply") || strings.Contains(transcript.Content, "private unrelated") {
		t.Fatal(transcript, err)
	}
}
