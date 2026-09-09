package commands

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// TestCommandSyncPublishesMentionIDs exercises each sync exit, including the
// cached no-write path used on restarts, without additional Discord requests.
func TestCommandSyncPublishesMentionIDs(t *testing.T) {
	for _, mode := range []string{"create", "edit", "unchanged", "cached"} {
		t.Run(mode, func(t *testing.T) {
			local := CommandDefinition()
			client := &fakeCommandClient{}
			cache := newMemoryCommandCache()
			wantID := "created-case"
			if mode != "create" {
				remote := cloneCommand(local)
				remote.ID = "remote-case"
				wantID = remote.ID
				if mode == "edit" {
					remote.Description = "Old description"
				}
				client.commands = []*discordgo.ApplicationCommand{remote}
				if mode == "cached" {
					hash, _, err := commandFingerprintForScope(local, "")
					if err != nil {
						t.Fatal(err)
					}
					cache.entries["global|case"] = commandCacheEntry{DiscordCommandID: remote.ID, Hash: hash}
				}
			}
			syncer := testCommandSyncer(client, cache)
			syncer.AppID = "sync-mentions-" + mode
			if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: local, Handler: noopHandler}}); err != nil {
				t.Fatal(err)
			}
			if got := ui.ResolveCommandMentions("`/case add`", syncer.AppID); got != "</case add:"+wantID+">" {
				t.Fatal("actual Discord ID was not registered", got)
			}
			if (mode == "unchanged" || mode == "cached") && (len(client.created) != 0 || len(client.edited) != 0) {
				t.Fatal("read-only sync unexpectedly wrote commands")
			}
		})
	}
}
