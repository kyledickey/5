package store_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack/model"
	storage "github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestCaseTemplateStorageCreateListGetExpanded(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCaseTemplate(ctx, model.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels: []model.ExpandedCaseTemplateLevel{
			{
				Level: model.CaseTemplateLevel{Position: 2, Name: "Second", TriggerCaseCount: 3},
				Actions: []model.CaseTemplateLevelAction{
					{ActionType: model.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":3600}`},
				},
			},
			{
				Level: model.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true, NotifyUser: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	if created.Template.ID == "" {
		t.Fatalf("expected template id")
	}
	if len(created.Levels) != 2 || created.Levels[0].Level.Position != 1 || created.Levels[1].Level.Position != 2 {
		t.Fatalf("expected levels ordered by position, got %+v", created.Levels)
	}
	if len(created.Levels[0].Actions) != 0 || len(created.Levels[1].Actions) != 1 {
		t.Fatalf("expected zero or one action per level, got %+v", created.Levels)
	}

	list, err := store.ListCaseTemplates(ctx, guildID)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(list) != 1 || list[0].Template.ID != created.Template.ID {
		t.Fatalf("expected created template in list, got %+v", list)
	}
}

func TestCaseTemplateStorageUpdateReplacesChildrenAndIncrementsVersion(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCaseTemplate(ctx, model.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels:   templateLevels(),
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	update := templateModel(guildID, "spam-updated")
	update.Name = "Spam Updated"
	update.UpdatedByDiscordUserID = "moderator-2"
	updated, err := store.UpdateCaseTemplate(ctx, model.UpdateCaseTemplateParams{
		GuildID:    guildID,
		TemplateID: created.Template.ID,
		Template:   update,
		Levels: []model.ExpandedCaseTemplateLevel{
			{
				Level: model.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true},
				Actions: []model.CaseTemplateLevelAction{
					{ActionType: model.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":3600}`},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("update template: %v", err)
	}
	if updated.Template.Version != created.Template.Version+1 {
		t.Fatalf("expected version increment, got %d then %d", created.Template.Version, updated.Template.Version)
	}
	if len(updated.Levels) != 1 || len(updated.Levels[0].Actions) != 1 || updated.Levels[0].Actions[0].ActionType != model.ActionTimeoutUser {
		t.Fatalf("expected replaced levels and actions, got %+v", updated.Levels)
	}
}

func TestCaseTemplateStorageArchiveHidesFromListButDetailStillWorks(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCaseTemplate(ctx, model.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels:   templateLevels(),
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	archived, err := store.ArchiveCaseTemplate(ctx, guildID, created.Template.ID, nil)
	if err != nil {
		t.Fatalf("archive template: %v", err)
	}
	if archived.Template.ArchivedAt == nil {
		t.Fatalf("expected archived template")
	}

	list, err := store.ListCaseTemplates(ctx, guildID)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(list) != 1 || list[0].Template.ArchivedAt == nil {
		t.Fatalf("expected archived template retained in authorized list")
	}

	detail, err := store.GetCaseTemplateExpanded(ctx, guildID, created.Template.ID)
	if err != nil {
		t.Fatalf("get archived detail: %v", err)
	}
	if detail == nil || detail.Template.ID != created.Template.ID {
		t.Fatalf("expected archived detail fetch to work")
	}
}

func TestCaseTemplateStorageSlugUniquePerGuild(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	_, err := store.CreateCaseTemplate(ctx, model.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels:   templateLevels(),
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	_, err = store.CreateCaseTemplate(ctx, model.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels:   templateLevels(),
	})
	if err == nil {
		t.Fatalf("expected duplicate slug error")
	}
}

func templateLevels() []model.ExpandedCaseTemplateLevel {
	return []model.ExpandedCaseTemplateLevel{
		{
			Level: model.CaseTemplateLevel{
				Position:  1,
				Name:      "Default",
				IsDefault: true,
			},
			Actions: []model.CaseTemplateLevelAction{
				{ActionType: model.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":3600}`},
			},
		},
	}
}

func templateTestStore(t *testing.T) (*storage.Store, string) {
	t.Helper()

	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	guild, err := store.UpsertGuild(ctx, model.UpsertGuildParams{
		DiscordGuildID:     "guild-1",
		Name:               "Guild",
		OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}

	return store, guild.ID
}

func templateModel(guildID, slug string) model.CaseTemplate {
	return model.CaseTemplate{
		GuildID:                guildID,
		Slug:                   slug,
		Name:                   "Spam",
		Description:            "Spam template",
		ReasonTemplate:         "No spam",
		CreatedByDiscordUserID: "moderator-1",
		UpdatedByDiscordUserID: "moderator-1",
	}
}
