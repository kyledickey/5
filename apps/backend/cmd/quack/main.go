package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/lmittmann/tint"
	quackruntime "github.com/quackdiscord/bot/internal/runtime"
)

func main() {
	slog.SetDefault(
		slog.New(tint.NewTextHandler(os.Stderr, nil)),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := quackruntime.Run(ctx); err != nil {
		slog.Error("Quack stopped", "error", err)
		os.Exit(1)
	}
}
