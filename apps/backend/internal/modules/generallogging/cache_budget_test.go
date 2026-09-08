package generallogging

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// budgetMessage creates distinct identities with equally sized retained content.
func budgetMessage(guild, id string) CachedMessage {
	return CachedMessage{GuildID: guild, MessageDiscordID: id, ChannelDiscordID: "channel", AuthorDiscordUserID: "author", Content: strings.Repeat("x", 100)}
}

// assertCacheAccounting independently totals all indexed entries/configurations.
func assertCacheAccounting(t *testing.T, c *MessageCache) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	entries := 0
	for _, g := range c.guilds {
		if len(g.messages) == 0 {
			t.Fatal("empty guild index retained")
		}
		for _, entry := range g.messages {
			total += messageBytes(entry.message)
			entries++
		}
	}
	for _, limit := range c.limits {
		total += limit.bytes
	}
	if total != c.retainedBytes || total > c.byteBudget || entries != c.global.Len() {
		t.Fatalf("accounting total=%d retained=%d budget=%d entries=%d global=%d", total, c.retainedBytes, c.byteBudget, entries, c.global.Len())
	}
	if c.idleLimits.Len() > maxIdleGuildLimits {
		t.Fatal("idle metadata overflow")
	}
}

// TestCacheGlobalFIFOUsesAggregateBytes proves cross-guild oldest eviction and
// preserves original insertion age after replacement or a context lookup.
func TestCacheGlobalFIFOUsesAggregateBytes(t *testing.T) {
	c := NewMessageCache(10)
	a, b, d := budgetMessage("a", "1"), budgetMessage("b", "2"), budgetMessage("d", "3")
	c.byteBudget = messageBytes(a) + messageBytes(b)
	c.Put(a)
	c.Put(b)
	a.Content = strings.Repeat("y", 100)
	if previous, ok := c.Replace(a); !ok || previous.Content == a.Content {
		t.Fatal("replacement lost old snapshot")
	}
	c.Get("a", "1")
	c.Put(d)
	if _, ok := c.Get("a", "1"); ok {
		t.Fatal("replacement refreshed global FIFO age")
	}
	if c.Len("b") != 1 || c.Len("d") != 1 {
		t.Fatal("evicted a newer guild")
	}
	assertCacheAccounting(t, c)
}

// TestCacheReplacementDeleteAndOversizeAccounting covers growth, shrinkage, and
// rejection without stale old text or eviction of unrelated useful context.
func TestCacheReplacementDeleteAndOversizeAccounting(t *testing.T) {
	c := NewMessageCache(10)
	c.byteBudget = 4096
	a, b := budgetMessage("a", "1"), budgetMessage("b", "2")
	c.Put(a)
	c.Put(b)
	a.Content = strings.Repeat("large", 200)
	c.Replace(a)
	assertCacheAccounting(t, c)
	a.Content = "small"
	a.AuthorDiscordUserID = ""
	a.ChannelDiscordID = ""
	c.Replace(a)
	saved, _ := c.Get("a", "1")
	if saved.AuthorDiscordUserID != "author" || saved.ChannelDiscordID != "channel" {
		t.Fatal("partial replacement lost identity")
	}
	assertCacheAccounting(t, c)
	a.Content = strings.Repeat("x", 5000)
	if old, ok := c.Replace(a); !ok || old.Content != "small" {
		t.Fatal("oversized replacement lost previous snapshot")
	}
	if _, ok := c.Get("a", "1"); ok {
		t.Fatal("stale text survived oversized replacement")
	}
	a.MessageDiscordID = "new"
	c.Put(a)
	if c.Len("b") != 1 {
		t.Fatal("oversized insert evicted unrelated context")
	}
	if _, ok := c.Delete("b", "2"); !ok {
		t.Fatal("delete lost cached context")
	}
	assertCacheAccounting(t, c)
	if c.retainedBytes != 0 || len(c.guilds) != 0 {
		t.Fatal("delete retained message/index bytes")
	}
}

// TestCacheGuildCapsProtectOtherGuilds checks local eviction precedes global
// pressure and lowering one guild's limit does not evict another guild's context.
func TestCacheGuildCapsProtectOtherGuilds(t *testing.T) {
	c := NewMessageCache(10)
	c.SetGuildLimit("a", 2)
	c.Put(budgetMessage("b", "first"))
	for i := 0; i < 20; i++ {
		c.Put(budgetMessage("a", fmt.Sprint(i)))
	}
	if c.Len("a") != 2 || c.Len("b") != 1 {
		t.Fatal("guild caps leaked across guilds")
	}
	c.SetGuildLimit("a", 1)
	if _, ok := c.Get("a", "19"); !ok {
		t.Fatal("local FIFO evicted latest message")
	}
	if c.Len("b") != 1 {
		t.Fatal("lowering cap affected other guild")
	}
	assertCacheAccounting(t, c)
}

// TestCacheIdleMetadataBoundPreservesActiveLimits prevents empty configuration
// churn from growing forever while retaining settings for actively cached guilds.
func TestCacheIdleMetadataBoundPreservesActiveLimits(t *testing.T) {
	c := NewMessageCache(10)
	c.SetGuildLimit("active", 1)
	c.Put(budgetMessage("active", "1"))
	for i := 0; i < maxIdleGuildLimits+100; i++ {
		c.SetGuildLimit(fmt.Sprint(i), 3)
	}
	if c.idleLimits.Len() != maxIdleGuildLimits || len(c.limits) != maxIdleGuildLimits+1 {
		t.Fatal("metadata not bounded", len(c.limits))
	}
	c.Put(budgetMessage("active", "2"))
	if c.Len("active") != 1 {
		t.Fatal("idle churn discarded active limit")
	}
	c.Delete("active", "2")
	assertCacheAccounting(t, c)
	// Metadata itself participates in the aggregate budget, including long keys.
	c.byteBudget = 4096
	c.SetGuildLimit(strings.Repeat("z", 8192), 1)
	assertCacheAccounting(t, c)
}

// TestCacheDefensiveSlicesAndConcurrentBudget protects mutable context snapshots
// while exercising aggregate accounting under concurrent independent guild writes.
func TestCacheDefensiveSlicesAndConcurrentBudget(t *testing.T) {
	c := NewMessageCache(5)
	c.byteBudget = 8192
	m := budgetMessage("a", "1")
	m.Attachments = []AttachmentMetadata{{Filename: "original"}}
	m.EmbedTypes = []string{"rich"}
	c.Put(m)
	m.Attachments[0].Filename = "changed"
	m.EmbedTypes[0] = "changed"
	saved, _ := c.Get("a", "1")
	if saved.Attachments[0].Filename != "original" || saved.EmbedTypes[0] != "rich" {
		t.Fatal("input mutated stored slices")
	}
	saved.Attachments[0].Filename = "changed"
	again, _ := c.Get("a", "1")
	if again.Attachments[0].Filename != "original" {
		t.Fatal("output mutated cache")
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			guild := fmt.Sprint(i)
			for j := 0; j < 40; j++ {
				c.Put(budgetMessage(guild, fmt.Sprint(j)))
				c.Get(guild, fmt.Sprint(j))
				if j%3 == 0 {
					c.Delete(guild, fmt.Sprint(j))
				}
			}
		}(i)
	}
	workers.Wait()
	assertCacheAccounting(t, c)
}
