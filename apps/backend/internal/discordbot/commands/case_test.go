package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
	"gorm.io/gorm"
)

type fakeDiscordClient struct {
	botGuild                *quack.DiscordBotGuild
	liveActorPermissionBits *uint64
}

func (f fakeDiscordClient) UserGuilds(ctx context.Context, accessToken string) ([]quack.DiscordUserGuild, error) {
	return nil, nil
}

func (f fakeDiscordClient) BotGuilds(ctx context.Context) ([]quack.DiscordBotGuild, error) {
	if f.botGuild == nil {
		return nil, nil
	}
	return []quack.DiscordBotGuild{*f.botGuild}, nil
}

func (f fakeDiscordClient) BotGuild(ctx context.Context, discordGuildID string) (*quack.DiscordBotGuild, error) {
	return f.botGuild, nil
}

func (f fakeDiscordClient) GuildAuthorization(ctx context.Context, guildID, actorID, targetID string) (*quack.DiscordGuildAuthorization, error) {
	permissionBits := uint64(discordgo.PermissionModerateMembers)
	if f.liveActorPermissionBits != nil {
		permissionBits = *f.liveActorPermissionBits
	}
	target := &quack.DiscordMemberAuthorization{DiscordUserID: targetID, Present: targetID != "", TopRolePosition: 1}
	return &quack.DiscordGuildAuthorization{
		Guild:  *f.botGuild,
		Actor:  quack.DiscordMemberAuthorization{DiscordUserID: actorID, Present: true, PermissionBits: permissionBits, TopRolePosition: 10},
		Bot:    quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 20, Bot: true},
		Target: target,
	}, nil
}

func TestCommandDefinitionDefinesCaseAdd(t *testing.T) {
	command := CaseCommandDefinition()
	if command.Name != "case" || command.DMPermission == nil || *command.DMPermission {
		t.Fatalf("unexpected command definition: %+v", command)
	}
	if command.DefaultMemberPermissions == nil || *command.DefaultMemberPermissions != int64(discordgo.PermissionModerateMembers) {
		t.Fatalf("expected moderate members default permission, got %+v", command.DefaultMemberPermissions)
	}
	if len(command.Options) < 9 || command.Options[0].Name != "add" {
		t.Fatalf("expected add subcommand, got %+v", command.Options)
	}

	add := command.Options[0]
	if len(add.Options) != 4 {
		t.Fatalf("expected template/user/message/file options, got %+v", add.Options)
	}
	if !add.Options[0].Autocomplete {
		t.Fatalf("expected template option to support autocomplete")
	}
	for position, option := range add.Options {
		if position < 2 && !option.Required {
			t.Fatalf("expected required options first, got %+v", add.Options)
		}
		if position >= 2 && option.Required {
			t.Fatalf("required option follows optional options: %+v", add.Options)
		}
	}
}

func TestHandleCaseInteractionCreatesCase(t *testing.T) {
	ctx := context.Background()
	store, services, templateID := newCaseCommandHarness(t)

	result := HandleCaseInteraction(ui.Context{
		Context:     ctx,
		Services:    services,
		Interaction: caseAddInteraction(templateID, "target-1", uint64(discordgo.PermissionModerateMembers)),
	})
	response := result.Response
	if response == nil {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred success response, got %v", response.Type)
	}
	if response.Data != nil && response.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Fatalf("expected public acknowledgement, got %+v", response.Data)
	}
	if result.Task == nil {
		t.Fatalf("expected deferred case creation task")
	}
	responder := &fakeResponder{}
	if err := result.Task(ctx, responder); err != nil {
		t.Fatalf("run deferred task: %v", err)
	}
	if responder.channelPublishes != 0 || responder.webhookFollowups != 0 || responder.deleted || responder.followup.Content != "" || responder.edit.Content == nil || responder.edit.Embeds == nil || len(*responder.edit.Embeds) != 0 || responder.editCount != 1 {
		t.Fatalf("expected original response to become the result: %+v", responder)
	}
	for _, want := range []string{"Case #1", "<@target-1>", "Spam", "Warning"} {
		if !strings.Contains(*responder.edit.Content, want) {
			t.Fatalf("missing %q in %q", want, *responder.edit.Content)
		}
	}

	cases, err := store.ListCases(ctx, storeGuildID(t, store, "guild-1"))
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("expected one case, got %+v", cases)
	}
	if cases[0].Source != model.CaseSourceDiscord || cases[0].TargetDiscordUserID != "target-1" {
		t.Fatalf("unexpected case: %+v", cases[0])
	}
	if cases[0].Reason != "Spam" {
		t.Fatalf("expected immutable template reason, got %q", cases[0].Reason)
	}
	var receipts []model.CasePublication
	if err := store.DB().Find(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].CaseID != cases[0].ID || receipts[0].MessageID != "message-1" || receipts[0].ChannelID != "channel-1" {
		t.Fatalf("original result not tracked: %+v", receipts)
	}
}

func TestHandleCaseInteractionDoesNotTrustStaleInteractionPermissionBits(t *testing.T) {
	_, services, templateID := newCaseCommandHarness(t)

	result := HandleCaseInteraction(ui.Context{
		Context:     context.Background(),
		Services:    services,
		Interaction: caseAddInteraction(templateID, "target-1", 0),
	})
	response := result.Response
	if response == nil || response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("unexpected response: %+v", response)
	}
	if result.Task == nil {
		t.Fatalf("expected live Discord permission to authorize despite stale interaction bits")
	}
}

func TestHandleCaseInteractionDeniesRevokedLivePermissionDespiteInteractionSnapshot(t *testing.T) {
	_, services, templateID := newCaseCommandHarnessWithLivePermissions(t, 0)
	result := HandleCaseInteraction(ui.Context{
		Context: context.Background(), Services: services,
		Interaction: caseAddInteraction(templateID, "target-1", ^uint64(0)),
	})
	response := result.Response
	if response == nil || response.Data == nil || len(response.Data.Embeds) != 0 || !strings.Contains(response.Data.Content, "No case was created.") || !strings.Contains(response.Data.Content, "Moderate Members") {
		t.Fatalf("unexpected live permission denial: %+v", response)
	}
	if result.Task != nil || response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected immediate private denial, result=%+v", result)
	}
}

func TestHandleTemplateAutocompleteReturnsUsableTemplates(t *testing.T) {
	_, services, templateID := newCaseCommandHarness(t)

	result := HandleCaseInteraction(ui.Context{
		Context:     context.Background(),
		Services:    services,
		Interaction: templateAutocompleteInteraction("repeated", uint64(discordgo.PermissionModerateMembers)),
	})
	response := result.Response
	if response == nil || response.Data == nil || len(response.Data.Choices) != 1 {
		t.Fatalf("unexpected autocomplete response: %+v", response)
	}
	if result.Task != nil {
		t.Fatalf("expected autocomplete to be immediate")
	}
	if response.Data.Choices[0].Value != templateID {
		t.Fatalf("expected template id choice, got %+v", response.Data.Choices[0])
	}
	if response.Data.Choices[0].Name != "Spam - Unwanted repeated messages" {
		t.Fatalf("expected name and description choice label, got %q", response.Data.Choices[0].Name)
	}
}

func TestHandleTemplateAutocompleteFiltersArchivedTemplates(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	ctx := context.Background()
	guildContext := caseCommandGuildContext(t, services)
	template := createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{
		Slug:           "ghost",
		Name:           "Ghost",
		Description:    "Hidden moderation workflow",
		ReasonTemplate: "Hidden",
		Levels: []quack.TemplateLevelInput{
			{
				Name:      "Default",
				Position:  1,
				IsDefault: true,
			},
		},
	})
	if _, err := services.Templates.Archive(ctx, guildContext, template.ID); err != nil {
		t.Fatalf("archive template: %v", err)
	}

	result := HandleCaseInteraction(ui.Context{
		Context:     ctx,
		Services:    services,
		Interaction: templateAutocompleteInteraction("hidden", uint64(discordgo.PermissionModerateMembers)),
	})
	response := result.Response
	if response == nil || response.Data == nil {
		t.Fatalf("unexpected autocomplete response: %+v", response)
	}
	if len(response.Data.Choices) != 0 {
		t.Fatalf("expected disabled template to be filtered, got %+v", response.Data.Choices)
	}
}

func TestTemplateAutocompleteLabelTruncatesToDiscordLimit(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	ctx := context.Background()
	guildContext := caseCommandGuildContext(t, services)
	longTemplate := createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{
		Slug:           "longdesc",
		Name:           "Long Description",
		Description:    strings.Repeat("description ", 20),
		ReasonTemplate: "Long description",
		Levels: []quack.TemplateLevelInput{
			{
				Name:      "Default",
				Position:  1,
				IsDefault: true,
			},
		},
	})

	result := HandleCaseInteraction(ui.Context{
		Context:     ctx,
		Services:    services,
		Interaction: templateAutocompleteInteraction("longdesc", uint64(discordgo.PermissionModerateMembers)),
	})
	response := result.Response
	if response == nil || response.Data == nil || len(response.Data.Choices) != 1 {
		t.Fatalf("unexpected autocomplete response: %+v", response)
	}
	if response.Data.Choices[0].Value != longTemplate.ID {
		t.Fatalf("expected long template id choice, got %+v", response.Data.Choices[0])
	}
	if len([]rune(response.Data.Choices[0].Name)) != 100 {
		t.Fatalf("expected label length 100, got %d: %q", len([]rune(response.Data.Choices[0].Name)), response.Data.Choices[0].Name)
	}
}

type fakeResponder struct {
	channelPublishes int
	webhookFollowups int
	edit             ui.Edit
	followup         ui.Message
	deleted          bool
	updated          ui.Edit
	editCount        int
}

func (f *fakeResponder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	f.edit = edit
	f.editCount++
	return &discordgo.Message{ID: "message-1", ChannelID: "channel-1"}, nil
}

func (f *fakeResponder) Followup(message ui.Message) (*discordgo.Message, error) {
	f.webhookFollowups++
	f.followup = message
	return &discordgo.Message{ID: "followup-1", ChannelID: "channel-1"}, nil
}

func (f *fakeResponder) EditFollowup(messageID string, edit ui.Edit) (*discordgo.Message, error) {
	f.updated = edit
	return &discordgo.Message{ID: messageID}, nil
}

func (f *fakeResponder) DeleteOriginal() error {
	f.deleted = true
	return nil
}

func (f *fakeResponder) UpdateMessage(edit ui.Edit) (*discordgo.Message, error) {
	f.updated = edit
	return &discordgo.Message{ID: "message-1", ChannelID: "channel-1"}, nil
}

func newCaseCommandHarness(t *testing.T) (*store.Store, *quack.Services, string) {
	return newCaseCommandHarnessWithLivePermissions(t, uint64(discordgo.PermissionModerateMembers))
}

func newCaseCommandHarnessWithLivePermissions(t *testing.T, permissionBits uint64) (*store.Store, *quack.Services, string) {
	t.Helper()

	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	services := quack.New(config.Default(), store, fakeDiscordClient{
		botGuild: &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"}, liveActorPermissionBits: &permissionBits,
	}, nil, nil)
	guildContext, err := services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
		DiscordGuildID: "guild-1",
		DiscordUserID:  "owner-1",
		DisplayName:    "Owner",
	})
	if err != nil {
		t.Fatalf("resolve guild context: %v", err)
	}

	created := createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{
		Slug:           "spam",
		Name:           "Spam",
		Description:    "Unwanted repeated messages",
		ReasonTemplate: "Spam",
		Levels: []quack.TemplateLevelInput{
			{
				Name:       "Default",
				Position:   1,
				IsDefault:  true,
				NotifyUser: true,
			},
		},
	})

	return store, services, created.ID
}

func createCaseCommandTemplate(t *testing.T, services *quack.Services, guildContext *quack.GuildStaffContext, input quack.TemplateInput) *quack.TemplateResponse {
	t.Helper()

	created, err := services.Templates.Create(context.Background(), guildContext, input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return created
}

func caseCommandGuildContext(t *testing.T, services *quack.Services) *quack.GuildStaffContext {
	t.Helper()

	guildContext, err := services.Guilds.ResolveDiscordStaffContext(context.Background(), quack.DiscordStaffContextInput{
		DiscordGuildID: "guild-1",
		DiscordUserID:  "owner-1",
		DisplayName:    "Owner",
	})
	if err != nil {
		t.Fatalf("resolve guild context: %v", err)
	}
	return guildContext
}

func caseAddInteraction(templateID, targetID string, permissions uint64) *discordgo.InteractionCreate {
	options := []*discordgo.ApplicationCommandInteractionDataOption{
		{
			Name:  "template",
			Type:  discordgo.ApplicationCommandOptionString,
			Value: templateID,
		},
		{
			Name:  "user",
			Type:  discordgo.ApplicationCommandOptionUser,
			Value: targetID,
		},
	}

	return interaction(discordgo.InteractionApplicationCommand, permissions, []*discordgo.ApplicationCommandInteractionDataOption{
		{
			Name:    "add",
			Type:    discordgo.ApplicationCommandOptionSubCommand,
			Options: options,
		},
	})
}

func templateAutocompleteInteraction(query string, permissions uint64) *discordgo.InteractionCreate {
	return interaction(discordgo.InteractionApplicationCommandAutocomplete, permissions, []*discordgo.ApplicationCommandInteractionDataOption{
		{
			Name: "add",
			Type: discordgo.ApplicationCommandOptionSubCommand,
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{
					Name:    "template",
					Type:    discordgo.ApplicationCommandOptionString,
					Value:   query,
					Focused: true,
				},
			},
		},
	})
}

func interaction(interactionType discordgo.InteractionType, permissions uint64, options []*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "interaction-1",
			AppID:     "app-1",
			Type:      interactionType,
			GuildID:   "guild-1",
			ChannelID: "channel-1",
			Member: &discordgo.Member{
				User:        &discordgo.User{ID: "mod-1", Username: "mod", GlobalName: "Moderator"},
				Permissions: int64(permissions),
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name:    "case",
				Options: options,
			},
		},
	}
}

func storeGuildID(t *testing.T, store *store.Store, discordGuildID string) string {
	t.Helper()

	guild, err := store.GetGuildByDiscordID(context.Background(), discordGuildID)
	if err != nil {
		t.Fatalf("get guild: %v", err)
	}
	if guild == nil {
		t.Fatalf("expected guild %s", discordGuildID)
	}
	return guild.ID
}

// PublishChannel records a standalone public notice independently of webhook replies.
func (f *fakeResponder) PublishChannel(_ context.Context, message ui.Message) (*discordgo.Message, error) {
	f.channelPublishes++
	f.followup = message
	return &discordgo.Message{ID: "channel-message", ChannelID: "channel-1"}, nil
}

// EditChannel records bot-token refreshes of standalone notices.
func (f *fakeResponder) EditChannel(_ context.Context, messageID string, edit ui.Edit) (*discordgo.Message, error) {
	f.updated = edit
	return &discordgo.Message{ID: messageID, ChannelID: "channel-1"}, nil
}

// TestCaseAddFailureStaysPrivate removes the public defer before explaining a
// pre-creation failure privately, without producing a case or public error copy.
func TestCaseAddFailureStaysPrivate(t *testing.T) {
	repository, services, _ := newCaseCommandHarness(t)
	result := HandleCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: caseAddInteraction("missing-template", "target", uint64(discordgo.PermissionModerateMembers))})
	if result.Task == nil {
		t.Fatal("expected deferred creation")
	}
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if !responder.deleted || responder.editCount != 0 || responder.channelPublishes != 0 || responder.webhookFollowups != 1 || !responder.followup.Ephemeral || responder.followup.Content == "" {
		t.Fatalf("creation failure was not private: %+v", responder)
	}
	cases, err := repository.ListCases(context.Background(), storeGuildID(t, repository, "guild-1"))
	if err != nil || len(cases) != 0 {
		t.Fatalf("failed request created a case: %+v %v", cases, err)
	}
}

// TestCaseCreatePermissionGuidance verifies safe recovery copy survives wrapped
// denials without misidentifying target safety failures as missing permissions.
func TestCaseCreatePermissionGuidance(t *testing.T) {
	tests := []struct {
		name, reason string
		permission   uint64
		want         string
	}{
		{"missing basic authority", "permission_required", uint64(discordgo.PermissionModerateMembers), "You need Moderate Members"},
		{"missing kick", "permission_required", uint64(discordgo.PermissionKickMembers), "You need Kick Members"},
		{"missing ban", "permission_required", uint64(discordgo.PermissionBanMembers), "You need Ban Members"},
		{"bot missing ban", "bot_permission_required", uint64(discordgo.PermissionBanMembers), "Quack needs Ban Members"},
		{"self", "self_target", 0, "cannot create a case against yourself"},
		{"actor hierarchy", "actor_hierarchy", 0, "equal to or above yours"},
		{"bot hierarchy", "bot_hierarchy", 0, "equal to or above Quack's"},
		{"owner", "guild_owner_target", 0, "cannot target the server owner"},
		{"unknown", "private-internal-reason", 0, "could not confirm authority"},
		{"unknown permission", "permission_required", 12345, "could not confirm authority"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("private adapter failure: %w", &quack.AuthorizationError{Reason: tt.reason, RequiredPermission: tt.permission, MetadataJSON: "private-metadata"})
			got := caseCreateErrorMessage(err)
			if !strings.HasPrefix(got, "No case was created.") || !strings.Contains(got, tt.want) {
				t.Fatalf("unexpected guidance: %s", got)
			}
			if strings.Contains(got, "private") || strings.Contains(got, "12345") {
				t.Fatalf("internal details leaked: %s", got)
			}
			if tt.reason == "permission_required" && tt.permission != 12345 && !strings.Contains(got, "Ask a staff member with that permission") {
				t.Fatalf("missing recovery guidance: %s", got)
			}
			if tt.permission == 0 && strings.Contains(got, "Ban Members") {
				t.Fatalf("invented missing permission: %s", got)
			}
		})
	}
}

// TestExistingCasePermissionErrorsDoNotClaimCreation keeps reads, edits and
// reversals from claiming that no case exists when a shared error is mapped.
func TestExistingCasePermissionErrorsDoNotClaimCreation(t *testing.T) {
	got := caseCommandErrorMessage(&quack.AuthorizationError{Reason: "permission_required", RequiredPermission: uint64(discordgo.PermissionBanMembers)})
	if got != "You don’t have permission to do that. Ask a moderator with the required permission." {
		t.Fatalf("unexpected existing-case error: %s", got)
	}
}

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
	for _, private := range []string{"screenshot.png", "cdn.discordapp.com", "The evidence channel is unavailable"} {
		if !strings.Contains(string(encoded), private) {
			t.Fatalf("staff evidence result omitted %q: %s", private, encoded)
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

// TestNativeDetailReadsPreservePages verifies slash reads and native navigation
// retain their output without loading action attempts.
func TestNativeDetailReadsPreservePages(t *testing.T) {
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
			want = ui.EditMessage(views.CaseDetailPage(full, 1, ""))
		}
		if page == 1 && (actual.Content == nil || !strings.Contains(*actual.Content, "Moderator saved context")) {
			t.Fatal("case view omitted saved context")
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("page %d output changed: %+v %+v", page, actual, want)
		}
	}
}

func TestCaseComponentRegistrarInstallsRealRecoveryAndPaginationHandlers(t *testing.T) {
	registry := interactions.NewComponentRegistry()
	if err := RegisterCaseComponents(registry); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"list_prev", "list_next", "user_prev", "user_next", "failures_prev", "failures_next", "retry", "dismiss", "void", "reverse", "message_template", "user_template", "template_page"} {
		if _, ok, err := registry.LookupComponent(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: action, Version: "v1", Payload: "payload"})); err != nil || !ok {
			t.Fatalf("component %s not registered: ok=%v err=%v", action, ok, err)
		}
	}
	for _, action := range []string{"void_submit", "reverse_submit", "edit_context_submit"} {
		if _, ok, err := registry.LookupModal(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: action, Version: "v1", Payload: "payload"})); err != nil || !ok {
			t.Fatalf("modal %s not registered: ok=%v err=%v", action, ok, err)
		}
	}
}

func TestCaseAddActsImmediatelyWithOptionalContext(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	guildContext := caseCommandGuildContext(t, services)
	template := createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{Slug: "abuse", Name: "Abuse", ReasonTemplate: "Abusive behavior", ContextFields: []quack.TemplateContextFieldInput{{Key: "details", Label: "What happened?", FieldType: model.ContextFieldLongText, Position: 1, Required: true}}, Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}}})

	result := HandleCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: caseAddInteraction(template.ID, "target-2", uint64(discordgo.PermissionModerateMembers))})
	if result.Response == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Task == nil {
		t.Fatalf("case creation must not wait for context: %+v", result)
	}
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if responder.deleted || responder.channelPublishes != 0 || responder.webhookFollowups != 0 || responder.followup.Content != "" || responder.edit.Content == nil || responder.edit.Embeds == nil || len(*responder.edit.Embeds) != 0 || responder.editCount != 1 {
		t.Fatalf("expected one in-place public result, got %+v", responder)
	}
	for _, want := range []string{"Case #1", "<@target-2>", "Abuse"} {
		if !strings.Contains(*responder.edit.Content, want) {
			t.Fatalf("missing %q: %+v", want, responder.followup)
		}
	}
	for _, hidden := range []string{"Matching Cases", "Visible context", "Evidence", "Repeated abusive replies"} {
		if strings.Contains(*responder.edit.Content, hidden) {
			t.Fatalf("public result leaked %s", hidden)
		}
	}

}

func TestMessageContextActionOffersActiveTemplateSelection(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	guildContext := caseCommandGuildContext(t, services)
	createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{Slug: "other", Name: "Other", ReasonTemplate: "Other reason", Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}}})
	interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{ID: "message-command", Type: discordgo.InteractionApplicationCommand, GuildID: "guild-1", ChannelID: "channel-1", Member: &discordgo.Member{User: &discordgo.User{ID: "mod-1", Username: "mod"}, Permissions: int64(discordgo.PermissionModerateMembers)}, Data: discordgo.ApplicationCommandInteractionData{Name: messageCaseCommandName, TargetID: "message-1", Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Messages: map[string]*discordgo.Message{"message-1": {ID: "message-1", ChannelID: "channel-1", Author: &discordgo.User{ID: "target-1"}}}}}}}
	result := HandleMessageCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	if result.Response == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || result.Task == nil {
		t.Fatal("expected private acknowledgement")
	}
	picker := &fakeResponder{}
	if err := result.Task(context.Background(), picker); err != nil {
		t.Fatal(err)
	}
	if picker.edit.Components == nil || len(*picker.edit.Components) != 1 {
		t.Fatal("missing template picker")
	}
	row := (*picker.edit.Components)[0].(discordgo.ActionsRow)
	menu := row.Components[0].(discordgo.SelectMenu)
	if len(menu.Options) != 2 || !strings.Contains(menu.CustomID, "case:message_template:v1:") {
		t.Fatalf("unexpected active-template selector: %+v", menu)
	}
}

// TestCaseCreationDoesNotDependOnContextFieldCount protects immediate creation
// for imported policies containing multiple formerly required fields.
func TestCaseCreationDoesNotDependOnContextFieldCount(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	fields := []quack.TemplateContextFieldInput{}
	for i := 1; i <= 6; i++ {
		fields = append(fields, quack.TemplateContextFieldInput{Key: fmt.Sprintf("field_%d", i), Label: fmt.Sprintf("Field %d", i), FieldType: model.ContextFieldShortText, Position: i, Required: true})
	}
	template := createCaseCommandTemplate(t, services, guild, quack.TemplateInput{Slug: "many-fields", Name: "Many fields", ReasonTemplate: "Rule", ContextFields: fields, Levels: []quack.TemplateLevelInput{{Name: "Warning", Position: 1, IsDefault: true}}})
	result := HandleCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: caseAddInteraction(template.ID, "target-many", uint64(discordgo.PermissionModerateMembers))})
	if result.Task == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("unexpected form: %+v", result)
	}
	if err := result.Task(context.Background(), &fakeResponder{}); err != nil {
		t.Fatal(err)
	}
}

func TestCaseCommandHasNoLegacyDirectPunishmentCommands(t *testing.T) {
	definition := CaseCommandDefinition()
	for _, option := range definition.Options {
		switch option.Name {
		case "warn", "timeout", "kick", "ban":
			t.Fatalf("legacy direct punishment command remains: %s", option.Name)
		}
	}
}
