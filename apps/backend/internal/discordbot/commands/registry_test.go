package commands

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

func TestRegistryRejectsDuplicateCommandNames(t *testing.T) {
	registry := NewRegistry()
	spec := CommandSpec{
		Definition: &discordgo.ApplicationCommand{Name: "case", Description: "one"},
		Handler:    noopHandler,
	}
	if err := registry.Register(spec); err != nil {
		t.Fatalf("register command: %v", err)
	}

	if err := registry.Register(spec); err == nil {
		t.Fatalf("expected duplicate command registration to fail")
	}
}

func TestDefaultRegistryIncludesCaseCommand(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(CaseCommandSpec()); err != nil {
		t.Fatalf("register case command: %v", err)
	}
	handler, ok := registry.LookupCommand("case")
	if !ok || handler == nil {
		t.Fatalf("expected case command to be registered")
	}
	specs := registry.Specs()
	if len(specs) != 1 || specs[0].Definition == nil || specs[0].Handler == nil {
		t.Fatalf("expected complete case command spec, got %+v", specs)
	}
}

func noopHandler(ctx ui.Context) ui.HandlerResult {
	_ = context.Background()
	return ui.HandlerResult{}
}
