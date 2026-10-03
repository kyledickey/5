package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// RegisterGatewayHandlers subscribes the module gateway handlers to session.
// Handlers only enqueue or run bounded work; none of them touch the moderation
// action queue.
func (r *Runtime) RegisterGatewayHandlers(session *discordgo.Session) error {
	if session == nil {
		return errors.New("optional module gateway session is not configured")
	}
	session.AddHandler(r.onMessageCreate)
	session.AddHandler(r.onMessageUpdate)
	session.AddHandler(r.onMessageDelete)
	session.AddHandler(r.onMessageDeleteBulk)
	session.AddHandler(r.onGuildMemberAdd)
	session.AddHandler(r.onGuildMemberRemove)
	session.AddHandler(r.onTicketGuildCreate)
	session.AddHandler(r.onTicketMemberUpdate)
	session.AddHandler(r.onTicketRoleUpdate)
	session.AddHandler(r.onTicketRoleDelete)
	session.AddHandler(r.onModerationAuditEntry)
	session.AddHandler(r.onGuildUpdate)
	session.AddHandler(r.onGuildDelete)
	session.AddHandler(r.onChannelCreate)
	session.AddHandler(r.onChannelUpdate)
	session.AddHandler(r.onChannelDelete)
	return nil
}

// RequiredGatewayIntents returns the fixed intent set the modules need. It does
// not depend on which guilds currently enable a module, so enabling one later
// never requires a reconnect: message content serves evidence, transcripts and
// logging; member events serve thread permission repair even with logging off.
func (r *Runtime) RequiredGatewayIntents(ctx context.Context) (discordgo.Intent, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return discordgo.IntentGuilds |
		discordgo.IntentGuildMembers |
		discordgo.IntentGuildModeration |
		discordgo.IntentGuildMessages |
		discordgo.IntentMessageContent, nil
}

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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := (guildResolver{db: r.db}).internalID(ctx, discordGuildID)
	return id, err == nil
}

// Ticket admission and logging capture precede the potentially slow honeypot
// permission lookups so a REST stall cannot lose ticket text.
func (r *Runtime) onMessageCreate(_ *discordgo.Session, event *discordgo.MessageCreate) {
	if r == nil {
		return
	}
	r.recordTicketMessage(event)
	if event == nil || event.Message == nil || event.GuildID == "" {
		return
	}
	guildID, ok := r.internalGuildID(event.GuildID)
	if !ok {
		return
	}
	if r.Logging != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = r.Logging.CacheMessage(ctx, cachedMessage(guildID, event.Message))
		cancel()
	}
	r.submitHoneypotMessage(guildID, event)
}

// submitHoneypotMessage performs live member/permission projection only for an
// enabled guild, then submits to the module's isolated bounded runtime.
func (r *Runtime) submitHoneypotMessage(guildID string, event *discordgo.MessageCreate) {
	if r == nil || r.registry == nil || r.session == nil || r.HoneypotRuntime == nil || event == nil || event.Message == nil || event.GuildID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	configuration, err := r.registry.Configuration(ctx, guildID, modules.Honeypots)
	if err != nil || configuration == nil || !configuration.Enabled {
		return
	}
	var settings honeypot.Settings
	if err := json.Unmarshal([]byte(configuration.ConfigJSON), &settings); err != nil || settings.ChannelDiscordID != event.ChannelID {
		return
	}
	if event.Author == nil || event.Author.ID == currentBotID(r.session) || event.WebhookID != "" {
		return
	}
	channel, err := r.session.Channel(event.ChannelID, discordgo.WithContext(ctx))
	if err != nil || channel == nil || channel.GuildID != event.GuildID {
		return
	}
	guild, err := r.session.Guild(event.GuildID, discordgo.WithContext(ctx))
	if err != nil || guild == nil {
		return
	}
	member, err := r.session.GuildMember(event.GuildID, event.Author.ID, discordgo.WithContext(ctx))
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

// repairDeletedHoneypotWarning restores a honeypot warning a moderator or
// another bot deleted. It is independent of general logging enablement and
// never changes a moderation outcome.
func (r *Runtime) repairDeletedHoneypotWarning(guildID, channelID string, messageIDs []string) {
	if r.honeypotCounter == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.honeypotCounter.WarningDeleted(ctx, guildID, channelID, messageIDs); err != nil {
		slog.WarnContext(ctx, "Could not restore deleted honeypot warning", "guild_id", guildID, "error", err)
	}
}

func cachedMessage(guildID string, message *discordgo.Message) generallogging.CachedMessage {
	cached := generallogging.CachedMessage{GuildID: guildID, ChannelDiscordID: message.ChannelID, MessageDiscordID: message.ID, Content: message.Content}
	if message.Author != nil {
		cached.AuthorDiscordUserID = message.Author.ID
	}
	for _, attachment := range message.Attachments {
		cached.Attachments = append(cached.Attachments, generallogging.AttachmentMetadata{DiscordID: attachment.ID, Filename: attachment.Filename, ContentType: attachment.ContentType, Size: int64(attachment.Size), URL: attachment.URL})
	}
	for _, embed := range message.Embeds {
		cached.EmbedTypes = append(cached.EmbedTypes, string(embed.Type))
	}
	return cached
}

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

func (r *Runtime) onGuildMemberAdd(_ *discordgo.Session, event *discordgo.GuildMemberAdd) {
	r.memberEvent(event.Member, generallogging.MemberJoin)
}

func (r *Runtime) onGuildMemberRemove(_ *discordgo.Session, event *discordgo.GuildMemberRemove) {
	r.memberEvent(event.Member, generallogging.MemberLeave)
}

func (r *Runtime) memberEvent(member *discordgo.Member, eventType generallogging.EventType) {
	if member == nil {
		return
	}
	guildID, ok := r.internalGuildID(member.GuildID)
	if !ok {
		return
	}
	actorID := ""
	if member.User != nil {
		actorID = member.User.ID
	}
	r.submit(generallogging.Event{GuildID: guildID, Type: eventType, ActorDiscordUserID: actorID})
}

// onModerationAuditEntry uses Discord's actor attribution to omit Quack's own
// bans and unbans, which already have case/action audit records.
func (r *Runtime) onModerationAuditEntry(_ *discordgo.Session, entry *discordgo.GuildAuditLogEntryCreate) {
	event, ok := externalBanEvent(entry, currentBotID(r.session))
	if !ok {
		return
	}
	guildID, ok := r.internalGuildID(entry.GuildID)
	if !ok {
		return
	}
	event.GuildID = guildID
	r.submit(event)
}

// externalBanEvent projects only externally performed ban lifecycle changes.
// The target and actor are kept separate rather than labeling the target as staff.
func externalBanEvent(entry *discordgo.GuildAuditLogEntryCreate, botID string) (generallogging.Event, bool) {
	if entry == nil || entry.AuditLogEntry == nil || entry.ActionType == nil || entry.GuildID == "" || entry.TargetID == "" || entry.UserID == "" || botID == "" || entry.UserID == botID {
		return generallogging.Event{}, false
	}
	var kind generallogging.EventType
	switch *entry.ActionType {
	case discordgo.AuditLogActionMemberBanAdd:
		kind = generallogging.DiscordBan
	case discordgo.AuditLogActionMemberBanRemove:
		kind = generallogging.DiscordUnban
	default:
		return generallogging.Event{}, false
	}
	return generallogging.Event{Type: kind, ActorDiscordUserID: entry.UserID, Metadata: map[string]string{"target_id": entry.TargetID, "reason": entry.Reason, "discord_audit_entry_id": entry.ID}}, true
}

func (r *Runtime) onGuildUpdate(_ *discordgo.Session, event *discordgo.GuildUpdate) {
	if event == nil || event.Guild == nil {
		return
	}
	guildID, ok := r.internalGuildID(event.ID)
	if ok {
		r.submit(generallogging.Event{GuildID: guildID, Type: generallogging.GuildChange, Metadata: map[string]string{"name": event.Name}})
	}
}

// onGuildDelete disables the departed guild's honeypot while retaining its
// configuration for an explicit repair after a future rejoin.
func (r *Runtime) onGuildDelete(_ *discordgo.Session, event *discordgo.GuildDelete) {
	if r == nil || r.registry == nil || r.HoneypotDiscord == nil || event == nil || event.Guild == nil || event.Unavailable {
		return
	}
	ctx := context.Background()
	guildID, err := r.resolver.internalIDAny(ctx, event.ID)
	if err != nil {
		return
	}
	configuration, err := r.registry.Configuration(ctx, guildID, modules.Honeypots)
	if err != nil || configuration == nil || !configuration.Enabled {
		return
	}
	var settings honeypot.Settings
	if json.Unmarshal([]byte(configuration.ConfigJSON), &settings) != nil || settings.ChannelDiscordID == "" {
		return
	}
	_ = r.HoneypotDiscord.HandleDeletedChannel(ctx, guildID, settings.ChannelDiscordID)
}

func (r *Runtime) onChannelCreate(_ *discordgo.Session, event *discordgo.ChannelCreate) {
	if event != nil {
		r.channelEvent(event.Channel, "created")
	}
}

func (r *Runtime) onChannelUpdate(_ *discordgo.Session, event *discordgo.ChannelUpdate) {
	if event != nil {
		r.channelEvent(event.Channel, "updated")
	}
}

func (r *Runtime) channelEvent(channel *discordgo.Channel, operation string) {
	if channel == nil || channel.GuildID == "" {
		return
	}
	guildID, ok := r.internalGuildID(channel.GuildID)
	if ok {
		r.submit(generallogging.Event{GuildID: guildID, ChannelDiscordID: channel.ID, Type: generallogging.ChannelChange, Metadata: map[string]string{"operation": operation, "name": channel.Name}})
	}
}

// onChannelDelete queues logging and repairs every module reference to the channel.
func (r *Runtime) onChannelDelete(_ *discordgo.Session, event *discordgo.ChannelDelete) {
	if event == nil || event.Channel == nil {
		return
	}
	r.channelEvent(event.Channel, "deleted")
	guildID, ok := r.internalGuildID(event.GuildID)
	if !ok {
		return
	}
	ctx := context.Background()
	if r.TicketDiscord != nil {
		_ = r.TicketDiscord.HandleDeletedEntryChannel(ctx, guildID, event.ID)
	}
	if r.HoneypotDiscord != nil {
		_ = r.HoneypotDiscord.HandleDeletedChannel(ctx, guildID, event.ID)
	}
	if r.Tickets != nil && r.TicketDiscord != nil {
		if ticketID, err := r.Tickets.DeletedChannelTicketID(ctx, guildID, event.ID); err == nil {
			_ = r.TicketDiscord.HandleDeletedChannel(ctx, guildID, ticketID, event.ID)
		}
	}
	if r.Logging != nil {
		_, _, _ = r.Logging.RepairDeletedChannel(ctx, generallogging.Actor{GuildID: guildID, DiscordUserID: "quack-system", CanManage: true}, event.ID)
	}
}

// recordTicketMessage preserves original native ticket text before optional
// logging filters. Failed writes are retained by the service's bounded retry
// buffer; closure must flush it successfully before deleting the thread.
func (r *Runtime) recordTicketMessage(event *discordgo.MessageCreate) {
	if r == nil || r.Tickets == nil || event == nil || event.Message == nil || event.GuildID == "" || event.Author == nil {
		return
	}
	guildID, known := r.Tickets.KnownMessageThread(event.ChannelID)
	if !known {
		return
	}
	if err := r.Tickets.RecordMessage(context.Background(), guildID, event.ChannelID, ticketTranscriptMessage(event.Message)); err != nil {
		if errors.Is(err, tickets.ErrJournalCutoff) {
			slog.Warn("Ticket message arrived after locked-thread transcript cutoff", "thread_id", event.ChannelID, "message_id", event.ID)
			return
		}
		slog.Warn("Ticket message retention failed; closure will retry", "guild_id", guildID, "thread_id", event.ChannelID, "message_id", event.ID)
	}
}

// onTicketGuildCreate reconciles staff thread membership when gateway state
// becomes available, including after reconnects that may have missed demotions.
func (r *Runtime) onTicketGuildCreate(_ *discordgo.Session, event *discordgo.GuildCreate) {
	if event != nil && event.Guild != nil && !event.Unavailable {
		r.reconcileTicketThreads(event.ID)
	}
}

func (r *Runtime) onTicketMemberUpdate(_ *discordgo.Session, event *discordgo.GuildMemberUpdate) {
	if event != nil && event.Member != nil && (event.BeforeUpdate == nil || !slices.Equal(event.Roles, event.BeforeUpdate.Roles)) {
		r.reconcileTicketThreads(event.GuildID)
	}
}

func (r *Runtime) onTicketRoleUpdate(_ *discordgo.Session, event *discordgo.GuildRoleUpdate) {
	if event != nil && event.GuildRole != nil {
		r.reconcileTicketThreads(event.GuildID)
	}
}

func (r *Runtime) onTicketRoleDelete(_ *discordgo.Session, event *discordgo.GuildRoleDelete) {
	if event != nil {
		r.reconcileTicketThreads(event.GuildID)
	}
}

// reconcileTicketThreads reconciles private-thread invitations with current staff roles.
func (r *Runtime) reconcileTicketThreads(discordGuildID string) {
	if r == nil || r.db == nil || r.session == nil || r.Tickets == nil {
		return
	}
	// Coalesce bursts while retaining repairs for other guilds. A reconnect
	// must not silently drop all but its first guild's cleanup operation.
	r.ticketRepairMu.Lock()
	if r.ticketRepairPending == nil {
		r.ticketRepairPending = make(map[string]struct{})
	}
	r.ticketRepairPending[discordGuildID] = struct{}{}
	if r.ticketRepairRunning {
		r.ticketRepairMu.Unlock()
		return
	}
	r.ticketRepairRunning = true
	for len(r.ticketRepairPending) > 0 {
		var next string
		for guildID := range r.ticketRepairPending {
			next = guildID
			break
		}
		delete(r.ticketRepairPending, next)
		r.ticketRepairMu.Unlock()
		r.repairTicketThreadsGuild(next)
		r.ticketRepairMu.Lock()
	}
	r.ticketRepairRunning = false
	r.ticketRepairMu.Unlock()
}

func (r *Runtime) repairTicketThreadsGuild(discordGuildID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	guildID, err := r.resolver.internalID(ctx, discordGuildID)
	if err != nil {
		return
	}
	client := ticketDiscordClient{session: r.session, resolver: r.resolver}
	after := ""
	for {
		records, err := r.Tickets.OpenThreadRepairPage(ctx, guildID, after)
		if err != nil {
			slog.ErrorContext(ctx, "Ticket permission repair lookup failed", "guild_id", guildID)
			return
		}
		for _, ticket := range records {
			channel, err := r.session.Channel(ticket.ThreadDiscordChannelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
			if err == nil && channel != nil && channel.GuildID == discordGuildID && channel.Type == discordgo.ChannelTypeGuildPrivateThread {
				err = client.syncTicketThreadMembers(ctx, discordGuildID, channel.ID, ticket.OwnerDiscordUserID)
			}
			if err != nil {
				slog.WarnContext(ctx, "Ticket permission repair incomplete", "guild_id", guildID, "ticket_id", ticket.ID)
			}
			if ctx.Err() != nil {
				return
			}
		}
		if len(records) < tickets.ThreadRepairPageSize {
			return
		}
		after = records[len(records)-1].ID
	}
}
