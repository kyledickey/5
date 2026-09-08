package moduleintegration

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// onTicketGuildCreate reconciles staff thread membership when gateway state
// becomes available, including after reconnects that may have missed demotions.
func (r *Runtime) onTicketGuildCreate(_ *discordgo.Session, event *discordgo.GuildCreate) {
	if event != nil && event.Guild != nil && !event.Unavailable {
		r.reconcileTicketThreads(event.ID)
	}
}

// onTicketMemberUpdate repairs thread membership after Discord role changes.
func (r *Runtime) onTicketMemberUpdate(_ *discordgo.Session, event *discordgo.GuildMemberUpdate) {
	if event != nil && event.Member != nil && (event.BeforeUpdate == nil || !slices.Equal(event.Roles, event.BeforeUpdate.Roles)) {
		r.reconcileTicketThreads(event.GuildID)
	}
}

// onTicketRoleUpdate reconciles invitations when a role's authority changes.
func (r *Runtime) onTicketRoleUpdate(_ *discordgo.Session, event *discordgo.GuildRoleUpdate) {
	if event != nil && event.GuildRole != nil {
		r.reconcileTicketThreads(event.GuildID)
	}
}

// onTicketRoleDelete repairs grants whose original staff role was removed.
func (r *Runtime) onTicketRoleDelete(_ *discordgo.Session, event *discordgo.GuildRoleDelete) {
	if event != nil {
		r.reconcileTicketThreads(event.GuildID)
	}
}

// reconcileTicketThreads reconciles private-thread invitations with current staff roles.
// Pages and the deadline bound gateway repair work; failures remain visible.
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

// repairTicketThreadsGuild performs one bounded, paginated guild cleanup.
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
		var records []struct{ ID, ThreadDiscordChannelID, OwnerDiscordUserID string }
		if err := r.db.WithContext(ctx).Table("tickets").Select("id, thread_discord_channel_id, owner_discord_user_id").
			Where("guild_id = ? AND status = ? AND id > ?", guildID, tickets.StatusOpen, after).Order("id ASC").Limit(100).Find(&records).Error; err != nil {
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
		if len(records) < 100 {
			return
		}
		after = records[len(records)-1].ID
	}
}
