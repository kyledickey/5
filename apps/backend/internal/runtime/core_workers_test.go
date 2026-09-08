package runtime

import (
	"context"
	"errors"
	"github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"testing"
	"time"
)

type appealDestinationStore struct{ quack.Repository }

func (appealDestinationStore) GetGuildSettings(context.Context, string) (*model.GuildSettings, error) {
	return &model.GuildSettings{AuditMirrorChannelDiscordID: "audit-channel", AppealQueueChannelDiscordID: "channel"}, nil
}
func (appealDestinationStore) GetGuildByID(context.Context, string) (*model.Guild, error) {
	return &model.Guild{DiscordGuildID: "discord-guild"}, nil
}

type rejectingAppealDestination struct{ guildID, channelID string }

func (v *rejectingAppealDestination) ValidateStaffChannel(_ context.Context, guildID, channelID string) error {
	v.guildID, v.channelID = guildID, channelID
	return errors.New("destination is public")
}

func TestAppealStaffDestinationRevalidatesPrivacy(t *testing.T) {
	validator := &rejectingAppealDestination{}
	resolver := appealStaffChannelResolver{repository: appealDestinationStore{}, validator: validator}
	if channel, err := resolver.AppealStaffChannel(context.Background(), "internal-guild"); err == nil || channel != "" {
		t.Fatalf("unsafe appeal destination accepted: %q, %v", channel, err)
	}
	if validator.guildID != "discord-guild" || validator.channelID != "channel" {
		t.Fatalf("incorrect destination identity: %+v", validator)
	}
}

// changingAppealDestination models configuration edits between worker deliveries.
type changingAppealDestination struct {
	quack.Repository
	queue string
}

func (r *changingAppealDestination) GetGuildSettings(context.Context, string) (*model.GuildSettings, error) {
	return &model.GuildSettings{AppealQueueChannelDiscordID: r.queue, AuditMirrorChannelDiscordID: "audit"}, nil
}
func (r *changingAppealDestination) GetGuildByID(context.Context, string) (*model.Guild, error) {
	return &model.Guild{DiscordGuildID: "guild"}, nil
}

type acceptingAppealDestination struct{}

func (acceptingAppealDestination) ValidateStaffChannel(context.Context, string, string) error {
	return nil
}

// TestAppealQueueConfigurationTakesEffectWithoutRestart checks the live resolver
// never falls back to the audit channel or caches a former queue destination.
func TestAppealQueueConfigurationTakesEffectWithoutRestart(t *testing.T) {
	repository := &changingAppealDestination{queue: "first"}
	resolver := appealStaffChannelResolver{repository: repository, validator: acceptingAppealDestination{}}
	for _, want := range []string{"first", "second", ""} {
		repository.queue = want
		got, err := resolver.AppealStaffChannel(context.Background(), "internal-guild")
		if err != nil || got != want {
			t.Fatalf("queue %q: got %q %v", want, got, err)
		}
	}
}

// blockingCoreOutboxes proves both loops run without an optional module runtime
// and receive cancellation before dependencies are torn down.
type blockingCoreOutboxes struct {
	quack.Repository
	started chan string
}

func (r blockingCoreOutboxes) ClaimPendingAppealNotifications(ctx context.Context, _ int) ([]model.AppealNotification, error) {
	r.started <- "appeals"
	<-ctx.Done()
	return nil, ctx.Err()
}
func (r blockingCoreOutboxes) ListPendingAuditMirrorEntries(ctx context.Context, _ int) ([]model.AuditLogEntry, error) {
	r.started <- "audit"
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestCoreWorkersRunAndCancelWithoutOptionalModules(t *testing.T) {
	repository := blockingCoreOutboxes{started: make(chan string, 2)}
	workers := startCoreWorkers(context.Background(), repository, &discordbot.Bot{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	defer workers.CloseContext(ctx)
	seen := map[string]bool{}
	for range 2 {
		select {
		case name := <-repository.started:
			seen[name] = true
		case <-ctx.Done():
			t.Fatal("core workers did not start independently")
		}
	}
	if !seen["appeals"] || !seen["audit"] {
		t.Fatalf("started: %v", seen)
	}
	if err := workers.CloseContext(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
