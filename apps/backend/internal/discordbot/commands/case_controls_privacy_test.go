package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack/model"
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
					assertRecoveryPrivate(t, result)
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
				assertRecoveryPrivate(t, result)
				responder := &fakeResponder{}
				if result.Task != nil {
					_ = result.Task(context.Background(), responder)
				}
				if responder.followup.Content != "" || responder.updated.Content != nil {
					t.Fatal("failed request touched public source")
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
			retainRecoveryReceipt(context.Background(), responder, ui.Conversation("case_void", "Case #1 was voided.", "", "Removal is queued.", "", false), true)
			if responder.deleted || responder.updated.Content != nil {
				t.Fatal("source or committed receipt removed")
			}
			if failure != "edit" && (responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "was voided")) {
				t.Fatal("committed result lost")
			}
			if failure == "none" && (responder.followup.Content == "" || responder.followup.Ephemeral) {
				t.Fatal("public success missing")
			}
			if failure == "edit" && !responder.followup.Ephemeral {
				t.Fatal("unconfirmed acknowledgement made public")
			}
		})
	}
}

// assertRecoveryPrivate checks Discord's initial visibility, which edits cannot change.
func assertRecoveryPrivate(t *testing.T, result ui.HandlerResult) {
	t.Helper()
	if result.Response == nil || result.Response.Data == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatal("recovery acknowledgement is public")
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
			assertRecoveryPrivate(t, result)
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
