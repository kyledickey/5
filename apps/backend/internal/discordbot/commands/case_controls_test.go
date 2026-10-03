package commands

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// TestRecoveryControlFeedbackPreservesSource exercises real mutations while
// preserving public audit messages and refreshing existing ephemeral views.
func TestRecoveryControlFeedbackPreservesSource(t *testing.T) {
	for _, operation := range []string{"retry", "dismiss"} {
		for _, source := range []string{"public", "private", "private_update_failed"} {
			private := source != "public"
			t.Run(operation+source, func(t *testing.T) {
				repository, services, _ := newCaseCommandHarness(t)
				guild := caseCommandGuildContext(t, services)
				item := model.Case{ULIDModel: model.ULIDModel{ID: "case"}, GuildID: guild.Guild.ID, CaseNumber: 99, TargetDiscordUserID: "target", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, TemplateSnapshotJSON: "{}"}
				if err := repository.DB().Create(&item).Error; err != nil {
					t.Fatal(err)
				}
				action := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "action"}, CaseID: item.ID, ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionFailed, ConfigSnapshotJSON: `{"duration_seconds":60}`, LastErrorCode: "missing_permission"}
				if err := repository.DB().Create(&action).Error; err != nil {
					t.Fatal(err)
				}
				interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
				interaction.Type = discordgo.InteractionMessageComponent
				interaction.Message = &discordgo.Message{Content: "original audit or case", Flags: 0}
				if private {
					interaction.Message.Flags = discordgo.MessageFlagsEphemeral
				}
				interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: operation, Version: "v1", Payload: action.ID})}
				result := actionControlComponent(ui.Context{Context: context.Background(), Services: services, Interaction: interaction}, operation)
				if private {
					if result.Response.Type != discordgo.InteractionResponseDeferredMessageUpdate {
						t.Fatal("private source not updated in place")
					}
				} else {
					assertRecoveryPublic(t, result)
				}
				responder := &recoveryUpdateResponder{failUpdate: source == "private_update_failed"}
				if err := result.Task(context.Background(), responder); err != nil {
					t.Fatal(err)
				}
				receipt := responder.edit.Content
				if private {
					receipt = responder.updated.Content
				} else if responder.updated.Content != nil {
					t.Fatal("public source changed")
				}
				if source == "private_update_failed" {
					receipt = &responder.followup.Content
					if !responder.followup.Ephemeral {
						t.Fatal("fallback was public")
					}
				} else if responder.followup.Content != "" {
					t.Fatal("extra receipt")
				}
				if responder.deleted || receipt == nil {
					t.Fatalf("unexpected receipt: %+v", responder)
				}
				if !strings.Contains(*receipt, map[string]string{"retry": "retry is queued", "dismiss": "was dismissed"}[operation]) {
					t.Fatal(*receipt)
				}
			})
		}
	}
}

// TestRecoveryModalsKeepFailuresPrivate checks permissions refreshed after the
// original control was displayed, missing cases, and invalid correction input.
func TestRecoveryModalsKeepFailuresPrivate(t *testing.T) {
	for _, operation := range []string{"void", "reverse"} {
		for _, scenario := range []string{"revoked", "missing", "invalid"} {
			t.Run(operation+scenario, func(t *testing.T) {
				bits := uint64(discordgo.PermissionModerateMembers)
				if scenario == "revoked" {
					bits = 0
				}
				repository, services, _ := newCaseCommandHarnessWithLivePermissions(t, bits)
				guild := caseCommandGuildContext(t, services)
				if scenario != "missing" {
					item := model.Case{ULIDModel: model.ULIDModel{ID: "missing"}, GuildID: guild.Guild.ID, CaseNumber: 1, TargetDiscordUserID: "target", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord}
					if err := repository.DB().Create(&item).Error; err != nil {
						t.Fatal(err)
					}
				}
				interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
				payload, value, field := "missing", "valid reason", "reason"
				handler := handleVoidModal
				if operation == "reverse" {
					payload = "missing|action|remove_timeout"
					value = "REVERSE"
					field = "confirm"
					handler = handleReverseModal
				}
				if scenario == "invalid" {
					value = ""
				}
				interaction.Type = discordgo.InteractionModalSubmit
				interaction.Data = discordgo.ModalSubmitInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: operation + "_submit", Version: "v1", Payload: payload}), Components: []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: field, Value: value})}}
				result := handler(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
				if result.Task == nil && (result.Response.Data == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0) {
					t.Fatal("validation error should be private")
				}
				responder := &fakeResponder{}
				if result.Task != nil {
					_ = result.Task(context.Background(), responder)
				}
				if responder.updated.Content != nil || responder.edit.Content != nil {
					t.Fatal("failed request exposed a public result")
				}
				if result.Task != nil && (!responder.deleted || !responder.followup.Ephemeral || responder.followup.Content == "") {
					t.Fatal("failure was not delivered privately")
				}
			})
		}
	}
}

// TestCommittedRecoveryPublicationRetainsReceipt protects successful moderation
// from public-send and redundant-cleanup failures after its transaction commits.
func TestCommittedRecoveryPublicationRetainsReceipt(t *testing.T) {
	for _, failure := range []string{"none", "publish", "cleanup", "edit"} {
		t.Run(failure, func(t *testing.T) {
			responder := &failingCasePublication{failPublish: failure == "publish", failCleanup: failure == "cleanup"}
			if failure == "edit" {
				responder.failEditCount = 3
			}
			retainRecoveryReceipt(context.Background(), responder, ui.Conversation("case_void", "Case #1 was voided.", "", "Removal is queued.", "", false))
			if responder.deleted || responder.updated.Content != nil {
				t.Fatal("source or committed receipt removed")
			}
			if failure != "edit" && (responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "was voided")) {
				t.Fatal("committed result lost")
			}
			if failure == "none" && (responder.followup.Content != "" || responder.channelPublishes != 0) {
				t.Fatal("duplicate success response")
			}
			if failure == "edit" && !responder.followup.Ephemeral {
				t.Fatal("unconfirmed acknowledgement made public")
			}
		})
	}
}

// assertRecoveryPublic checks Discord's initial visibility, which edits cannot change.
func assertRecoveryPublic(t *testing.T, result ui.HandlerResult) {
	t.Helper()
	if result.Response == nil || (result.Response.Data != nil && result.Response.Data.Flags&discordgo.MessageFlagsEphemeral != 0) {
		t.Fatal("recovery acknowledgement was hidden")
	}
}

// TestRecoveryModalSuccessSurvivesPublicationFailure verifies real void/reversal
// transactions retain their exact successful receipt even if Discord send fails.
func TestRecoveryModalSuccessSurvivesPublicationFailure(t *testing.T) {
	for _, operation := range []string{"void", "reverse"} {
		t.Run(operation, func(t *testing.T) {
			repository, services, _ := newCaseCommandHarness(t)
			guild := caseCommandGuildContext(t, services)
			item := model.Case{ULIDModel: model.ULIDModel{ID: "case"}, GuildID: guild.Guild.ID, CaseNumber: 1, TargetDiscordUserID: "target", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, TemplateSnapshotJSON: "{}"}
			if operation == "reverse" {
				item.Validity = model.CaseValidityVoided
			}
			if err := repository.DB().Create(&item).Error; err != nil {
				t.Fatal(err)
			}
			original := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "action"}, CaseID: item.ID, ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded, ConfigSnapshotJSON: "{}"}
			if err := repository.DB().Create(&original).Error; err != nil {
				t.Fatal(err)
			}
			interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
			payload, value, field := "case", "Correction", "reason"
			handler := handleVoidModal
			if operation == "reverse" {
				payload = "case|action|remove_timeout"
				value = "REVERSE"
				field = "confirm"
				handler = handleReverseModal
			}
			interaction.Type = discordgo.InteractionModalSubmit
			interaction.Data = discordgo.ModalSubmitInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: operation + "_submit", Version: "v1", Payload: payload}), Components: []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: field, Value: value})}}
			result := handler(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
			if result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || (result.Response.Data != nil && result.Response.Data.Flags&discordgo.MessageFlagsEphemeral != 0) {
				t.Fatal("successful command should defer publicly")
			}
			responder := &failingCasePublication{failPublish: true}
			if err := result.Task(context.Background(), responder); err != nil {
				t.Fatal(err)
			}
			if responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, map[string]string{"void": "was voided", "reverse": "reversal is queued"}[operation]) {
				t.Fatalf("committed success lost: %+v", responder.edit.Content)
			}
			actions, err := repository.ListCaseActionExecutions(context.Background(), item.ID)
			if err != nil || len(actions) != 2 || actions[1].ActionType != model.ActionRemoveTimeout {
				t.Fatalf("reversal was not queued: %+v %v", actions, err)
			}
			if responder.updated.Content != nil || responder.deleted {
				t.Fatal("source changed")
			}
		})
	}
}

// recoveryUpdateResponder simulates a failed in-place refresh after commit.
type recoveryUpdateResponder struct {
	fakeResponder
	failUpdate bool
}

// UpdateMessage leaves the prior source intact when delivery fails.
func (r *recoveryUpdateResponder) UpdateMessage(edit ui.Edit) (*discordgo.Message, error) {
	if r.failUpdate {
		return nil, errors.New("Discord unavailable")
	}
	return r.fakeResponder.UpdateMessage(edit)
}

// TestContextModalConfirmsSavedTextDespiteEvidenceFailure exercises private
// feedback after a committed edit, including the unchanged no-link path.
func TestContextModalConfirmsSavedTextDespiteEvidenceFailure(t *testing.T) {
	for _, text := range []string{"Staff notes", "Staff notes: https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"} {
		t.Run(text, func(t *testing.T) {
			ctx := context.Background()
			_, services, templateID := newCaseCommandHarness(t)
			guild := caseCommandGuildContext(t, services)
			created, err := services.Cases.Create(ctx, guild, quack.CaseInput{TemplateID: templateID, TargetDiscordUserID: "target-1"})
			if err != nil {
				t.Fatal(err)
			}
			interaction := caseAddInteraction(templateID, "target-1", uint64(discordgo.PermissionModerateMembers))
			interaction.Type = discordgo.InteractionModalSubmit
			interaction.Data = discordgo.ModalSubmitInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "edit_context_submit", Version: "v1", Payload: created.ID}), Components: []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "context", Value: text})}}
			result := handleEditContextModal(ui.Context{Context: ctx, Services: services, Interaction: interaction})
			if result.Task == nil || (result.Response.Data != nil && result.Response.Data.Flags&discordgo.MessageFlagsEphemeral != 0) {
				t.Fatal("context submission was not private")
			}
			responder := &fakeResponder{}
			if err := result.Task(ctx, responder); err != nil {
				t.Fatal(err)
			}
			if responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "Context saved") {
				t.Fatal("saved edit not confirmed")
			}
			if strings.Contains(text, "https://") != strings.Contains(*responder.edit.Content, "Some evidence could not be saved") {
				t.Fatalf("wrong evidence feedback: %s", *responder.edit.Content)
			}
			detail, err := services.Cases.Get(ctx, guild, created.ID)
			if err != nil || len(detail.ContextValues) != 1 || detail.ContextValues[0].Value != text {
				t.Fatalf("optional failure lost text: %+v %v", detail, err)
			}
		})
	}
}

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

// TestNativeEvidenceReadsPreservePages verifies initial and subsequent evidence
// pages match the bounded snapshot renderer without unrelated case histories.
func TestNativeEvidenceReadsPreservePages(t *testing.T) {
	repository, services, _ := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	item := model.Case{ULIDModel: model.ULIDModel{ID: "native-evidence-read"}, GuildID: guild.Guild.ID, CaseNumber: 1, Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, TemplateSnapshotJSON: "{}", MetadataJSON: "{}", ContextValuesJSON: `[{"key":"context","label":"Context","value":"Moderator saved context"}]`}
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
		want := ui.EditMessage(views.CaseEvidenceSnapshotPage(&quack.CaseEvidencePageResponse{CaseDetailResponse: *full, Position: 1, Total: 1}, page, ""))
		if page == 1 && (actual.Content == nil || !strings.Contains(*actual.Content, "Moderator saved context")) {
			t.Fatal("evidence view omitted saved context")
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("page %d output changed: %+v %+v", page, actual, want)
		}
	}
	second := model.CaseEvidenceSnapshot{ULIDModel: model.ULIDModel{ID: "second-snapshot"}, CaseID: item.ID, GuildID: item.GuildID, CaptureOutcome: "uploaded", EmbedsJSON: "[]"}
	if err := repository.DB().Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		payload               string
		delta, position, page int
	}{{"1:1000000|" + item.ID, 1, 2, 1}, {"2:1|" + item.ID, -1, 1, 1000000}} {
		interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
		interaction.Type = discordgo.InteractionMessageComponent
		interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "evidence", Version: "v1", Payload: scenario.payload})}
		result := pageEvidence(scenario.delta)(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
		responder := &fakeResponder{}
		if err := result.Task(context.Background(), responder); err != nil {
			t.Fatal(err)
		}
		selected, err := services.Cases.GetEvidencePage(context.Background(), guild, item.ID, scenario.position)
		if err != nil {
			t.Fatal(err)
		}
		want := ui.EditMessage(views.CaseEvidenceSnapshotPage(selected, scenario.page, ""))
		if !reflect.DeepEqual(responder.updated, want) {
			t.Fatalf("cross-snapshot navigation changed content: %+v", scenario)
		}
	}

}
