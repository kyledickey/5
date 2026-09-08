package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

// submit enqueues general logging without blocking the gateway or moderation.
func (r *Runtime) submit(event generallogging.Event) {
	if r == nil || r.LoggingQueue == nil {
		return
	}
	if err := r.LoggingQueue.Submit(event); err != nil && !errors.Is(err, generallogging.ErrQueueFull) {
		slog.Error("Failed to queue general logging event", "error", err)
	}
}

// internalGuildID resolves active guilds and suppresses events for unknown guilds.
func (r *Runtime) internalGuildID(discordGuildID string) (string, bool) {
	id, err := (guildResolver{db: r.db}).internalID(context.Background(), discordGuildID)
	return id, err == nil
}

// onMessageCreate retains bounded context only when logging is enabled.
func (r *Runtime) onMessageCreate(_ *discordgo.Session, event *discordgo.MessageCreate) {
	r.submitHoneypotMessage(event)
	if event == nil || event.Message == nil || event.GuildID == "" || (event.Author != nil && event.Author.Bot) {
		return
	}
	guildID, ok := r.internalGuildID(event.GuildID)
	if !ok {
		return
	}
	_ = r.Logging.CacheMessage(context.Background(), cachedMessage(guildID, event.Message))
}

// submitHoneypotMessage performs live member/permission projection only for an
// enabled guild, then submits to the module's isolated bounded runtime.
func (r *Runtime) submitHoneypotMessage(event *discordgo.MessageCreate) {
	if r == nil || r.registry == nil || r.session == nil || r.HoneypotRuntime == nil || event == nil || event.Message == nil || event.GuildID == "" {
		return
	}
	ctx := context.Background()
	guildID, ok := r.internalGuildID(event.GuildID)
	if !ok {
		return
	}
	configuration, err := r.registry.Configuration(ctx, guildID, modules.Honeypots)
	if err != nil || configuration == nil || !configuration.Enabled {
		return
	}
	var settings honeypot.Settings
	if err := json.Unmarshal([]byte(configuration.ConfigJSON), &settings); err != nil || settings.ChannelDiscordID != event.ChannelID {
		return
	}
	if event.Author == nil || event.Author.Bot || event.WebhookID != "" {
		return
	}
	channel, err := r.session.Channel(event.ChannelID)
	if err != nil || channel == nil || channel.GuildID != event.GuildID || event.Author == nil {
		return
	}
	guild, err := r.session.Guild(event.GuildID)
	if err != nil || guild == nil {
		return
	}
	member, err := r.session.GuildMember(event.GuildID, event.Author.ID)
	if err != nil || member == nil {
		return
	}
	message, err := projectHoneypotMessage(guildID, event, guild, channel, member, currentBotID(r.session))
	if err != nil {
		return
	}
	if err := r.HoneypotRuntime.Submit(message); err != nil && !errors.Is(err, honeypot.ErrQueueFull) {
		slog.Error("Failed to queue honeypot event", "error", err, "guild_id", event.GuildID)
	}
}

// onMessageUpdate queues an edit and refreshes bounded cache context.
func (r *Runtime) onMessageUpdate(_ *discordgo.Session, event *discordgo.MessageUpdate) {
	if event == nil || event.Message == nil || event.GuildID == "" {
		return
	}
	guildID, ok := r.internalGuildID(event.GuildID)
	if !ok {
		return
	}
	// Discord also emits partial updates for link previews and other metadata.
	if event.EditedTimestamp == nil {
		return
	}
	var before *generallogging.CachedMessage
	if event.BeforeUpdate != nil {
		value := cachedMessage(guildID, event.BeforeUpdate)
		before = &value
	}
	prepared, err := r.Logging.PrepareMessageEdit(context.Background(), cachedMessage(guildID, event.Message), before)
	if err == nil && prepared != nil {
		r.submit(*prepared)
	}
}

// onMessageDelete queues a cache-enriched deletion event.
func (r *Runtime) onMessageDelete(_ *discordgo.Session, event *discordgo.MessageDelete) {
	if event == nil || event.Message == nil || event.GuildID == "" {
		return
	}
	guildID, ok := r.internalGuildID(event.GuildID)
	if ok {
		r.submit(messageEvent(guildID, generallogging.MessageDelete, event.Message, "", ""))
		r.repairDeletedHoneypotWarning(guildID, event.ChannelID, []string{event.ID})
	}
}

// onMessageDeleteBulk queues bounded cache-aware bulk work.
func (r *Runtime) onMessageDeleteBulk(_ *discordgo.Session, event *discordgo.MessageDeleteBulk) {
	if event == nil {
		return
	}
	guildID, ok := r.internalGuildID(event.GuildID)
	if ok {
		r.submitBulkDelete(bulkDeleteEvent{guildID: guildID, channelID: event.ChannelID, messageIDs: append([]string(nil), event.Messages...)})
		r.repairDeletedHoneypotWarning(guildID, event.ChannelID, event.Messages)
	}
}

// cachedMessage copies the bounded subset allowed by logging privacy settings.
func cachedMessage(guildID string, message *discordgo.Message) generallogging.CachedMessage {
	cached := generallogging.CachedMessage{GuildID: guildID, ChannelDiscordID: message.ChannelID, MessageDiscordID: message.ID, Content: message.Content}
	for _, attachment := range message.Attachments {
		cached.Attachments = append(cached.Attachments, generallogging.AttachmentMetadata{DiscordID: attachment.ID, Filename: attachment.Filename, ContentType: attachment.ContentType, Size: int64(attachment.Size)})
	}
	for _, embed := range message.Embeds {
		cached.EmbedTypes = append(cached.EmbedTypes, string(embed.Type))
	}
	return cached
}

// messageEvent copies one Discord message event into the ephemeral module shape.
func messageEvent(guildID string, eventType generallogging.EventType, message *discordgo.Message, before, after string) generallogging.Event {
	cached := cachedMessage(guildID, message)
	actorID := ""
	if message.Author != nil {
		actorID = message.Author.ID
	}
	return generallogging.Event{
		GuildID: guildID, ChannelDiscordID: message.ChannelID, MessageDiscordID: message.ID,
		ActorDiscordUserID: actorID, Type: eventType, Before: before, After: after,
		Attachments: cached.Attachments, EmbedTypes: cached.EmbedTypes,
	}
}
