package generallogging

import (
	"container/list"
	"sync"
	"time"
)

// CachedMessage is bounded edit/delete context and is never treated as a permanent archive.
type CachedMessage struct {
	GuildID, ChannelDiscordID, MessageDiscordID, AuthorDiscordUserID, Content string
	Attachments                                                               []AttachmentMetadata
	EmbedTypes                                                                []string
	CachedAt                                                                  time.Time
}

// MessageCache is a concurrency-safe per-guild FIFO cache with independently configurable limits.
type MessageCache struct {
	mu           sync.Mutex
	guilds       map[string]*guildCache
	limits       map[string]int
	defaultLimit int
}

// guildCache combines a FIFO list with indexed nodes for constant-time deletes.
type guildCache struct {
	order    list.List
	messages map[string]*list.Element
}

// NewMessageCache constructs a bounded cache and normalizes unsafe defaults.
func NewMessageCache(defaultLimit int) *MessageCache {
	if defaultLimit < 1 {
		defaultLimit = 1000
	}
	return &MessageCache{guilds: map[string]*guildCache{}, limits: map[string]int{}, defaultLimit: defaultLimit}
}

// SetGuildLimit changes one guild's cap and immediately evicts oldest entries.
func (c *MessageCache) SetGuildLimit(guildID string, limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if limit < 1 {
		limit = 1
	}
	c.limits[guildID] = limit
	c.evict(guildID)
}

// Put stores or replaces one message while preserving stable FIFO order.
func (c *MessageCache) Put(message CachedMessage) { c.Replace(message) }

// Replace atomically snapshots old content while storing the new version.
func (c *MessageCache) Replace(message CachedMessage) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.guilds[message.GuildID]
	if g == nil {
		g = &guildCache{messages: make(map[string]*list.Element)}
		c.guilds[message.GuildID] = g
	}
	var previous CachedMessage
	found := false
	if element, ok := g.messages[message.MessageDiscordID]; ok {
		previous = cloneMessage(element.Value.(CachedMessage))
		if message.AuthorDiscordUserID == "" {
			message.AuthorDiscordUserID = previous.AuthorDiscordUserID
		}
		if message.ChannelDiscordID == "" {
			message.ChannelDiscordID = previous.ChannelDiscordID
		}
		found = true
		element.Value = cloneMessage(message)
	} else {
		g.messages[message.MessageDiscordID] = g.order.PushBack(cloneMessage(message))
	}
	c.evict(message.GuildID)
	return previous, found
}

// Get returns a defensive copy of cached message context.
func (c *MessageCache) Get(guildID, messageID string) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.guilds[guildID]
	if g == nil {
		return CachedMessage{}, false
	}
	element, ok := g.messages[messageID]
	if !ok {
		return CachedMessage{}, false
	}
	return cloneMessage(element.Value.(CachedMessage)), true
}

// Delete removes and returns one cached message.
func (c *MessageCache) Delete(guildID, messageID string) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.guilds[guildID]
	if g == nil {
		return CachedMessage{}, false
	}
	element, ok := g.messages[messageID]
	if !ok {
		return CachedMessage{}, false
	}
	delete(g.messages, messageID)
	g.order.Remove(element)
	return cloneMessage(element.Value.(CachedMessage)), true
}

// Len returns one guild's current bounded cache size.
func (c *MessageCache) Len(guildID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.guilds[guildID] == nil {
		return 0
	}
	return len(c.guilds[guildID].messages)
}

// evict removes oldest entries while the caller holds the cache mutex.
func (c *MessageCache) evict(guildID string) {
	g := c.guilds[guildID]
	if g == nil {
		return
	}
	limit := c.limits[guildID]
	if limit == 0 {
		limit = c.defaultLimit
	}
	for g.order.Len() > limit {
		oldest := g.order.Front()
		delete(g.messages, oldest.Value.(CachedMessage).MessageDiscordID)
		g.order.Remove(oldest)
	}
}

// cloneMessage prevents cached slices from being mutated by gateway callers.
func cloneMessage(m CachedMessage) CachedMessage {
	m.Attachments = append([]AttachmentMetadata(nil), m.Attachments...)
	m.EmbedTypes = append([]string(nil), m.EmbedTypes...)
	return m
}
