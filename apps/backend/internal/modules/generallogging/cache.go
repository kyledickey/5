package generallogging

import (
	"container/list"
	"strings"
	"sync"
	"time"
)

// CachedMessage is bounded edit/delete context, never a permanent archive.
type CachedMessage struct {
	GuildID, ChannelDiscordID, MessageDiscordID, AuthorDiscordUserID, Content string
	Attachments                                                               []AttachmentMetadata
	EmbedTypes                                                                []string
	CachedAt                                                                  time.Time
}

// defaultCacheByteBudget limits estimated retained data, not exact Go heap usage.
// Accounting includes owned strings, slice elements, and conservative map/list/
// struct overhead; allocator slack and transient caller snapshots are excluded.
const defaultCacheByteBudget int64 = 64 << 20

// maxIdleGuildLimits bounds configuration-only metadata between captures. The
// service reapplies settings before capture; an evicted idle limit uses the default.
const maxIdleGuildLimits = 1024

// MessageCache combines per-guild FIFO caps with a shared oldest-first byte budget.
// Replacements retain their original place in both queues. There are no timers or
// background workers; context is best-effort and may be evicted under pressure.
type MessageCache struct {
	mu                        sync.Mutex
	guilds                    map[string]*guildCache
	limits                    map[string]*guildLimit
	idleLimits                list.List
	global                    list.List
	defaultLimit              int
	byteBudget, retainedBytes int64
}

// guildCache indexes nodes in its FIFO; empty message maps are removed immediately.
type guildCache struct {
	order    list.List
	messages map[string]*cacheEntry
}

// cacheEntry joins both FIFO lists so every removal updates accounting once.
type cacheEntry struct {
	message               CachedMessage
	bytes                 int64
	guildNode, globalNode *list.Element
}

// guildLimit retains active settings and tracks bounded, recently used idle settings.
type guildLimit struct {
	value    int
	bytes    int64
	idleNode *list.Element
}

func NewMessageCache(defaultLimit int) *MessageCache {
	if defaultLimit < 1 {
		defaultLimit = 1000
	}
	return &MessageCache{guilds: map[string]*guildCache{}, limits: map[string]*guildLimit{}, defaultLimit: defaultLimit, byteBudget: defaultCacheByteBudget}
}

// SetGuildLimit changes one guild's cap and immediately evicts oldest entries.
// Active limits remain configured; idle limits may be forgotten under pressure.
func (c *MessageCache) SetGuildLimit(guildID string, limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if limit < 1 {
		limit = 1
	}
	configured := c.limits[guildID]
	if configured == nil {
		// Own keys rather than retaining arbitrarily large backing strings via slices.
		guildID = strings.Clone(guildID)
		configured = &guildLimit{bytes: 256 + int64(len(guildID))}
		c.limits[guildID] = configured
		c.retainedBytes += configured.bytes
	}
	configured.value = limit
	if configured.idleNode != nil {
		c.idleLimits.Remove(configured.idleNode)
		configured.idleNode = nil
	}
	if c.guilds[guildID] == nil {
		configured.idleNode = c.idleLimits.PushBack(guildID)
	}
	c.evict(guildID)
	c.trim()
}

// Put stores or replaces one message while preserving stable FIFO order.
func (c *MessageCache) Put(message CachedMessage) { c.Replace(message) }

// Replace snapshots previous content and preserves missing author/channel metadata.
// An oversized replacement removes old context rather than leaving stale text;
// rejecting a single oversized message does not evict unrelated messages.
func (c *MessageCache) Replace(message CachedMessage) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.guilds[message.GuildID]
	var entry *cacheEntry
	if g != nil {
		entry = g.messages[message.MessageDiscordID]
	}
	var previous CachedMessage
	found := entry != nil
	if found {
		previous = cloneMessage(entry.message)
		if message.AuthorDiscordUserID == "" {
			message.AuthorDiscordUserID = previous.AuthorDiscordUserID
		}
		if message.ChannelDiscordID == "" {
			message.ChannelDiscordID = previous.ChannelDiscordID
		}
	}
	size := messageBytes(message)
	if size > c.byteBudget {
		if found {
			c.remove(entry)
		}
		c.trim()
		return previous, found
	}
	message = ownMessage(message)
	if found {
		message.GuildID = entry.message.GuildID
		message.MessageDiscordID = entry.message.MessageDiscordID
		c.retainedBytes -= entry.bytes
		entry.message, entry.bytes = message, size
	} else {
		if g == nil {
			g = &guildCache{messages: map[string]*cacheEntry{}}
			c.guilds[message.GuildID] = g
			if configured := c.limits[message.GuildID]; configured != nil && configured.idleNode != nil {
				c.idleLimits.Remove(configured.idleNode)
				configured.idleNode = nil
			}
		}
		entry = &cacheEntry{message: message, bytes: size}
		entry.guildNode = g.order.PushBack(entry)
		entry.globalNode = c.global.PushBack(entry)
		g.messages[message.MessageDiscordID] = entry
	}
	c.retainedBytes += size
	c.evict(message.GuildID)
	c.trim()
	return previous, found
}

// Get returns a copy so callers cannot mutate cached slices; FIFO age is unchanged.
func (c *MessageCache) Get(guildID, messageID string) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if g := c.guilds[guildID]; g != nil {
		if entry := g.messages[messageID]; entry != nil {
			return cloneMessage(entry.message), true
		}
	}
	return CachedMessage{}, false
}

func (c *MessageCache) Delete(guildID, messageID string) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if g := c.guilds[guildID]; g != nil {
		if entry := g.messages[messageID]; entry != nil {
			previous := cloneMessage(entry.message)
			c.remove(entry)
			c.trim()
			return previous, true
		}
	}
	return CachedMessage{}, false
}

func (c *MessageCache) Len(guildID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if g := c.guilds[guildID]; g != nil {
		return len(g.messages)
	}
	return 0
}

// remove unlinks one entry from both queues. Callers hold the cache mutex.
func (c *MessageCache) remove(entry *cacheEntry) {
	guildID := entry.message.GuildID
	g := c.guilds[guildID]
	delete(g.messages, entry.message.MessageDiscordID)
	g.order.Remove(entry.guildNode)
	c.global.Remove(entry.globalNode)
	c.retainedBytes -= entry.bytes
	if len(g.messages) == 0 {
		delete(c.guilds, guildID)
		if configured := c.limits[guildID]; configured != nil {
			configured.idleNode = c.idleLimits.PushBack(guildID)
		}
	}
}

func (c *MessageCache) evict(guildID string) {
	g := c.guilds[guildID]
	if g == nil {
		return
	}
	limit := c.defaultLimit
	if configured := c.limits[guildID]; configured != nil {
		limit = configured.value
	}
	for g.order.Len() > limit {
		c.remove(g.order.Front().Value.(*cacheEntry))
	}
}

func (c *MessageCache) dropIdleLimit() {
	node := c.idleLimits.Front()
	guildID := node.Value.(string)
	c.retainedBytes -= c.limits[guildID].bytes
	delete(c.limits, guildID)
	c.idleLimits.Remove(node)
}

// trim bounds idle metadata before enforcing shared oldest-message eviction.
// Active settings are retained until their last message is evicted.
func (c *MessageCache) trim() {
	for c.idleLimits.Len() > maxIdleGuildLimits {
		c.dropIdleLimit()
	}
	for c.retainedBytes > c.byteBudget {
		if c.idleLimits.Len() > 0 {
			c.dropIdleLimit()
			continue
		}
		if oldest := c.global.Front(); oldest != nil {
			c.remove(oldest.Value.(*cacheEntry))
			continue
		}
		break
	}
	// A shared-budget eviction may have made several configurations idle.
	for c.idleLimits.Len() > maxIdleGuildLimits {
		c.dropIdleLimit()
	}
}

// messageBytes estimates owned payload plus generous per-entry indexing overhead.
// Charging guild map overhead per message deliberately overcounts shared metadata.
// Identity strings are counted twice to cover map keys surviving entry replacement.
func messageBytes(m CachedMessage) int64 {
	total := int64(640 + 2*len(m.GuildID) + len(m.ChannelDiscordID) + 2*len(m.MessageDiscordID) + len(m.AuthorDiscordUserID) + len(m.Content))
	for _, attachment := range m.Attachments {
		total += 96 + int64(len(attachment.DiscordID)+len(attachment.Filename)+len(attachment.ContentType)+len(attachment.URL))
	}
	for _, embed := range m.EmbedTypes {
		total += 16 + int64(len(embed))
	}
	return total
}

// ownMessage detaches retained strings from potentially larger caller allocations.
func ownMessage(m CachedMessage) CachedMessage {
	m = cloneMessage(m)
	m.GuildID = strings.Clone(m.GuildID)
	m.ChannelDiscordID = strings.Clone(m.ChannelDiscordID)
	m.MessageDiscordID = strings.Clone(m.MessageDiscordID)
	m.AuthorDiscordUserID = strings.Clone(m.AuthorDiscordUserID)
	m.Content = strings.Clone(m.Content)
	for i := range m.Attachments {
		a := &m.Attachments[i]
		a.DiscordID = strings.Clone(a.DiscordID)
		a.Filename = strings.Clone(a.Filename)
		a.ContentType = strings.Clone(a.ContentType)
		a.URL = strings.Clone(a.URL)
	}
	for i := range m.EmbedTypes {
		m.EmbedTypes[i] = strings.Clone(m.EmbedTypes[i])
	}
	return m
}

func cloneMessage(m CachedMessage) CachedMessage {
	m.Attachments = append([]AttachmentMetadata(nil), m.Attachments...)
	m.EmbedTypes = append([]string(nil), m.EmbedTypes...)
	return m
}
