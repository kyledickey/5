package runtime

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/quack"
)

// coreWorkers owns durable moderation delivery independently of optional modules.
// Workers start after Discord connects and stop before its session or storage closes.
type coreWorkers struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startCoreWorkers starts the appeal outbox and semantic audit mirror together.
func startCoreWorkers(ctx context.Context, repository quack.Repository, bot *discordbot.Bot) *coreWorkers {
	ctx, cancel := context.WithCancel(ctx)
	workers := &coreWorkers{cancel: cancel, done: make(chan struct{})}
	adapter := &discordbot.AppealNotificationAdapter{Session: bot.Session, Resolver: appealStaffChannelResolver{repository: repository, validator: bot}}
	dispatcher := quack.NewAppealNotificationDispatcher(repository, adapter)
	mirror := quack.NewAuditMirrorWorker(repository, bot, 0)
	var group sync.WaitGroup
	group.Add(2)
	go func() { defer group.Done(); mirror.Run(ctx) }()
	go func() { defer group.Done(); runAppealNotifications(ctx, dispatcher) }()
	go func() { group.Wait(); close(workers.done) }()
	return workers
}

// CloseContext cancels delivery polling and respects the process shutdown deadline.
func (w *coreWorkers) CloseContext(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.cancel()
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runAppealNotifications retries durable outbox deliveries in bounded batches.
func runAppealNotifications(ctx context.Context, dispatcher *quack.AppealNotificationDispatcher) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := dispatcher.DispatchPending(ctx, 50); err != nil && !errors.Is(err, context.Canceled) {
			slog.ErrorContext(ctx, "Failed to dispatch appeal notifications", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type appealStaffChannelResolver struct {
	repository quack.Repository
	validator  interface {
		ValidateStaffChannel(context.Context, string, string) error
	}
}

// AppealStaffChannel resolves the current staff-only destination for a guild.
// It reads settings on every delivery so a queue change takes effect without a
// restart, and revalidates the channel so a destination that has since become
// public is never used.
func (r appealStaffChannelResolver) AppealStaffChannel(ctx context.Context, guildID string) (string, error) {
	settings, err := r.repository.GetGuildSettings(ctx, guildID)
	if err != nil || settings == nil {
		return "", err
	}
	guild, err := r.repository.GetGuildByID(ctx, guildID)
	if err != nil || guild == nil {
		return "", errors.New("appeal guild is unavailable")
	}
	if err := r.validator.ValidateStaffChannel(ctx, guild.DiscordGuildID, settings.AppealQueueChannelDiscordID); err != nil {
		return "", err
	}
	return settings.AppealQueueChannelDiscordID, nil
}
