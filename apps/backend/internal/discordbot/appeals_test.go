package discordbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestRegisterAppealComponentsRequiresCompleteDependencies(t *testing.T) {
	registry := interactions.NewComponentRegistry()
	if err := RegisterAppealComponents(registry, &quack.Services{}, quack.NewAppealService(nil)); err == nil {
		t.Fatal("incomplete appeal component dependencies were accepted")
	}
	if _, found, err := registry.LookupComponent(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "reverse", Version: "v1", Payload: "appeal,execution,unban_user"})); err != nil || found {
		t.Fatalf("failed registration mutated component registry: found=%v err=%v", found, err)
	}
}

func TestRegisterAppealComponentsExposesReversalHandler(t *testing.T) {
	registry := interactions.NewComponentRegistry()
	services := quack.New(config.Default(), nil, nil, nil, nil)
	if err := RegisterAppealComponents(registry, services, quack.NewAppealService(nil)); err != nil {
		t.Fatalf("register appeal component: %v", err)
	}
	customID := ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "reverse", Version: "v1", Payload: "appeal,execution,unban_user"})
	if _, found, err := registry.LookupComponent(customID); err != nil || !found {
		t.Fatalf("appeal reversal handler was not exposed: found=%v err=%v", found, err)
	}
	for _, action := range []string{"accept_reason", "reject_reason"} {
		customID := ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: action, Version: "v1", Payload: "appeal"})
		if _, found, err := registry.LookupModal(customID); err != nil || !found {
			t.Fatalf("appeal decision modal %s was not exposed: found=%v err=%v", action, found, err)
		}
	}
}

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

// TestAppealQueueDecisionRequiresReason exercises the required-reason form with
// whitespace validation, fresh submission authority, and a stale competing form.
func TestAppealQueueDecisionRequiresReason(t *testing.T) {
	ctx := context.Background()
	repository := testutil.NewSQLiteStore(t)
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := repository.BootstrapGuild(ctx, model.BootstrapGuildParams{DiscordGuildID: "reason-guild", Name: "Pond", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	guild := &bootstrap.Guild
	settings, err := repository.GetGuildSettings(ctx, guild.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings.AppealReviewReasonRequired = true
	if _, err := repository.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{Settings: *settings}); err != nil {
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

	open := func(action string) *discordgo.InteractionResponse {
		t.Helper()
		customID := ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: action, Version: "v1", Payload: appeal.ID})
		result := appealDecisionHandler(services, appeals, action)(ui.Context{Context: ctx, Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "reason-guild", Member: &discordgo.Member{User: &discordgo.User{ID: "mod"}}, Data: discordgo.MessageComponentInteractionData{CustomID: customID}}}})
		if result.Task != nil || result.Response == nil || result.Response.Type != discordgo.InteractionResponseModal {
			t.Fatalf("%s did not open a reason modal: %+v", action, result)
		}
		row, ok := result.Response.Data.Components[0].(discordgo.ActionsRow)
		if !ok || len(row.Components) != 1 {
			t.Fatalf("%s reason modal has invalid components: %+v", action, result.Response.Data.Components)
		}
		input, ok := row.Components[0].(discordgo.TextInput)
		if !ok || !input.Required || input.MaxLength != 2000 || !strings.Contains(input.Label, "member") {
			t.Fatalf("%s reason input is invalid: %+v", action, row.Components[0])
		}
		return result.Response
	}
	acceptForm := open("accept")
	rejectForm := open("reject")

	submit := func(action, customID, reason string) ui.HandlerResult {
		t.Helper()
		interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionModalSubmit, GuildID: "reason-guild", Member: &discordgo.Member{User: &discordgo.User{ID: "mod"}}, Data: discordgo.ModalSubmitInteractionData{CustomID: customID, Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: "reason", Value: reason}}}}}}}
		return appealDecisionModal(services, appeals, action)(ui.Context{Context: ctx, Interaction: interaction})
	}
	if result := submit("accept", acceptForm.Data.CustomID, "   "); result.Task != nil || result.Response == nil || !strings.Contains(result.Response.Data.Content, "Write a reason") {
		t.Fatalf("whitespace reason was not rejected immediately: %+v", result)
	}

	denied := submit("accept", acceptForm.Data.CustomID, "The evidence supports voiding this case.")
	if denied.Task == nil || denied.Response.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("valid reason did not defer message update: %+v", denied)
	}
	deniedResponder := &appealTestResponder{}
	if err := denied.Task(ctx, deniedResponder); err != nil || !strings.Contains(deniedResponder.content, "Moderate Members") {
		t.Fatalf("submission did not refresh denied authority: %q %v", deniedResponder.content, err)
	}

	auth.permissions = uint64(discordgo.PermissionModerateMembers)
	accepted := submit("accept", acceptForm.Data.CustomID, "  The evidence supports voiding this case.  ")
	acceptedResponder := &appealTestResponder{}
	if err := accepted.Task(ctx, acceptedResponder); err != nil || !strings.Contains(acceptedResponder.content, "accepted") {
		t.Fatalf("reasoned acceptance failed: %q %v", acceptedResponder.content, err)
	}
	stored, err := repository.GetAppealByID(ctx, appeal.ID)
	if err != nil || stored.DecisionReason != "The evidence supports voiding this case." {
		t.Fatalf("moderator reason was not stored: %+v %v", stored, err)
	}

	competing := submit("reject", rejectForm.Data.CustomID, "This was opened before acceptance.")
	competingResponder := &appealTestResponder{}
	if err := competing.Task(ctx, competingResponder); err != nil || !strings.Contains(competingResponder.content, "already been decided") {
		t.Fatalf("stale competing form changed the decision: %q %v", competingResponder.content, err)
	}
}

// TestStatementBrowsingIsPublicAndRechecksAuthority verifies shared queue clicks
// open publicly and old private pages cannot retain revoked staff permissions.
func TestStatementBrowsingIsPublicAndRechecksAuthority(t *testing.T) {
	repository := testutil.NewSQLiteStore(t)
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	services := &quack.Services{Guilds: quack.NewGuildService(repository, &appealReviewAuthorization{})}
	appeals := quack.NewAppealService(repository)
	for _, private := range []bool{false, true} {
		message := &discordgo.Message{ID: "queue", Content: "PRIVATE STATEMENT"}
		if private {
			message.Flags = discordgo.MessageFlagsEphemeral
		}
		interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, GuildID: "guild", Member: &discordgo.Member{User: &discordgo.User{ID: "mod"}, Permissions: int64(discordgo.PermissionModerateMembers)}, Message: message, Data: discordgo.MessageComponentInteractionData{CustomID: "appeal:statement_next:v1:1|appeal"}}}
		result := appealStatementPage(services, appeals, 1)(ui.Context{Context: context.Background(), Interaction: interaction})
		if result.Task == nil {
			t.Fatal("missing deferred statement read")
		}
		if private {
			if result.Response.Type != discordgo.InteractionResponseDeferredMessageUpdate {
				t.Fatal("private reading position was not updated")
			}
		} else if result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || (result.Response.Data != nil && result.Response.Data.Flags&discordgo.MessageFlagsEphemeral != 0) {
			t.Fatal("shared queue browsing was hidden")
		}
		responder := &appealTestResponder{}
		if err := result.Task(context.Background(), responder); err != nil {
			t.Fatalf("permission error was not rendered: %v", err)
		}
		if !responder.lastEdit.PrivateError || responder.edits != 1 || !strings.Contains(responder.content, "Moderate Members") {
			t.Fatalf("missing private permission feedback: %+v", responder)
		}
		if strings.Contains(responder.content, "PRIVATE STATEMENT") || strings.Contains(responder.content, "Received an appeal") || (responder.lastEdit.Components != nil && len(*responder.lastEdit.Components) != 0) {
			t.Fatal("revoked moderator received statement content or controls")
		}
		if responder.followups != 0 || message.Content != "PRIVATE STATEMENT" {
			t.Fatal("statement browsing changed the shared queue")
		}
	}
}

// TestAppealSendErrorRetriesOnlyKnownRejections keeps ambiguous transport failures
// out of automatic retry while allowing channel/permission/rate-limit repair.
func TestAppealSendErrorRetriesOnlyKnownRejections(t *testing.T) {
	for _, status := range []int{403, 404, 429, 500} {
		err := appealSendError(&discordgo.RESTError{Response: &http.Response{StatusCode: status}})
		if errors.Is(err, quack.ErrAppealDeliveryDeferred) != (status != 500) {
			t.Fatalf("incorrect retry classification for %d: %v", status, err)
		}
	}
	if err := appealSendError(errors.New("connection reset after writing request")); errors.Is(err, quack.ErrAppealDeliveryDeferred) {
		t.Fatal("uncertain send was retried")
	}
}

// appealQueueResolverStub supplies a validated destination for transport tests.
type appealQueueResolverStub struct{}

func (appealQueueResolverStub) AppealStaffChannel(context.Context, string) (string, error) {
	return "queue", nil
}

// TestAppealQueueRefreshEditsOrRecreatesOnlyMissingMessages prevents duplicate
// posts on transient edits and preserves the final decision without stale controls.
func TestAppealQueueRefreshEditsOrRecreatesOnlyMissingMessages(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			edits, posts := 0, 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				code, body := 200, `{"id":"replacement"}`
				if request.Method == http.MethodPatch {
					edits++
					raw, _ := io.ReadAll(request.Body)
					if strings.Contains(string(raw), "appeal:accept") || strings.Contains(string(raw), "appeal:reject") {
						t.Fatal("decided message still has decision controls")
					}
					code = status
					if code != 200 {
						body = `{"code":10008,"message":"Unknown Message"}`
						if code == 500 {
							body = `{"code":0,"message":"Server error"}`
						}
					}
				} else if request.Method == http.MethodPost {
					posts++
				} else {
					t.Fatalf("unexpected request %s", request.Method)
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			adapter := &AppealNotificationAdapter{Session: session, Resolver: appealQueueResolverStub{}}
			receipt, err := adapter.SendAppealStaffNotification(context.Background(), "guild", &quack.AppealResponse{ID: "appeal", Status: model.AppealStatusRejected}, quack.AppealQueueReceipt{ChannelID: "queue", MessageID: "original"})
			if edits != 1 || posts != map[int]int{200: 0, 404: 1, 500: 0}[status] {
				t.Fatalf("unexpected requests: %d edits, %d posts", edits, posts)
			}
			if status == 500 {
				if !errors.Is(err, quack.ErrAppealDeliveryDeferred) {
					t.Fatalf("edit not retryable: %v", err)
				}
				return
			}
			if err != nil || receipt.ChannelID != "queue" || receipt.MessageID != map[int]string{200: "original", 404: "replacement"}[status] {
				t.Fatalf("receipt: %+v %v", receipt, err)
			}
		})
	}
}

// TestAppealDecisionCopyPreservesSnapshots keeps version-one wording and literal
// reviewer-supplied reason text equivalent to the former stored rendered bodies.
func TestAppealDecisionCopyPreservesSnapshots(t *testing.T) {
	for _, item := range []struct {
		status           model.AppealStatus
		icon, lead, next string
	}{
		{model.AppealStatusAccepted, "accept", "Your appeal was accepted.", "Your case was voided. Quack will try to remove any ban or timeout from it."},
		{model.AppealStatusRejected, "decline", "Your appeal was rejected.", ""},
		{model.AppealStatusNeedsInformation, "reply", "Staff need a little more information to review your appeal.", "You can reply from your Quack dashboard."},
	} {
		intent := &model.AppealDecisionIntent{Version: 1, Status: item.status, Reason: "**Reason** @everyone"}
		want := discordtext.Conversation(item.icon, item.lead, discordtext.Plain(intent.Reason), item.next, "")
		if item.status == model.AppealStatusAccepted {
			intent.RejoinURL = "https://discord.gg/original"
			want += "\n\nIf you left or were banned, you can rejoin once any ban has been removed: " + intent.RejoinURL
		}
		if got := appealMemberNotificationBody(quack.AppealMemberNotification{Intent: intent}); got != want {
			t.Fatal(got, want)
		}
	}
	legacy := "legacy preserved copy"
	if got := appealMemberNotificationBody(quack.AppealMemberNotification{LegacyBody: legacy}); got != legacy {
		t.Fatal("legacy body changed", got)
	}
}

// TestAppealRejoinButtonDelivery verifies accepted typed intent alone produces
// the native link button, and the actual REST payload retains existing copy.
func TestAppealRejoinButtonDelivery(t *testing.T) {
	for _, kind := range []string{"accepted", "unconfigured", "rejected", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			notice := quack.AppealMemberNotification{Intent: &model.AppealDecisionIntent{Version: 1, Status: model.AppealStatusAccepted, Reason: "Reviewed reason", RejoinURL: "https://discord.gg/saved-invite"}}
			switch kind {
			case "unconfigured":
				notice.Intent.RejoinURL = ""
			case "rejected":
				notice.Intent.Status = model.AppealStatusRejected
			case "legacy":
				notice.Intent = nil
				notice.LegacyBody = "Exact legacy text https://discord.gg/legacy"
			}
			message := appealMemberNotificationMessage(notice)
			if kind == "accepted" {
				if len(message.Components) != 1 {
					t.Fatal("button missing")
				}
				button := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
				if button.Style != discordgo.LinkButton || button.Label != "Rejoin Server" || button.URL != notice.Intent.RejoinURL || button.CustomID != "" {
					t.Fatalf("wrong rejoin button %+v", button)
				}
				if !strings.Contains(message.Content, "once any ban has been removed") {
					t.Fatal("removal uncertainty lost")
				}
			} else if len(message.Components) != 0 {
				t.Fatal("unexpected rejoin control")
			}
			if kind == "legacy" && appealMemberNotificationBody(notice) != notice.LegacyBody {
				t.Fatal("legacy body changed")
			}
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			sends := 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				response := `{"id":"dm"}`
				if strings.HasSuffix(request.URL.Path, "/messages") {
					sends++
					var payload struct {
						Flags           discordgo.MessageFlags
						Content         string
						Components      []json.RawMessage
						AllowedMentions *discordgo.MessageAllowedMentions `json:"allowed_mentions"`
					}
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if payload.Flags&discordgo.MessageFlagsEphemeral != 0 {
						t.Fatalf("DM carried interaction flags: %v", payload.Flags)
					}
					if payload.Content != message.ForApplication("").Content || len(payload.Components) != len(message.Components) || payload.AllowedMentions == nil || len(payload.AllowedMentions.Parse) != 0 {
						t.Fatalf("REST presentation changed %+v", payload)
					}
					if kind == "accepted" && !strings.Contains(string(payload.Components[0]), "https://discord.gg/saved-invite") {
						t.Fatal("saved invite lost in transport")
					}
					response = `{"id":"sent"}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: request}, nil
			})}
			if _, err := (&AppealNotificationAdapter{Session: session}).SendAppealMemberNotification(context.Background(), "member", notice); err != nil {
				t.Fatal(err)
			}
			if sends != 1 {
				t.Fatal("expected one DM", sends)
			}
		})
	}
}

// TestAppealMemberDecisionContext identifies the case and server without exposing staff.
func TestAppealMemberDecisionContext(t *testing.T) {
	for _, status := range []model.AppealStatus{model.AppealStatusAccepted, model.AppealStatusRejected} {
		notice := quack.AppealMemberNotification{Intent: &model.AppealDecisionIntent{Version: 1, Status: status, Reason: "Thanks for explaining.", CaseNumber: 42, CaseID: "case-id", GuildName: "Duck Pond"}}
		message := appealMemberNotificationMessage(notice)
		for _, want := range []string{"Your appeal was " + string(status), "Case #42", "Duck Pond", "Thanks for explaining."} {
			if !strings.Contains(message.Content, want) {
				t.Fatalf("missing %q: %s", want, message.Content)
			}
		}
		if message.Ephemeral {
			t.Fatal("member DM is ephemeral")
		}
		notice.Intent.CaseNumber = 0
		if !strings.Contains(appealMemberNotificationBody(notice), "case-id") {
			t.Fatal("case ID fallback missing")
		}
	}
}
