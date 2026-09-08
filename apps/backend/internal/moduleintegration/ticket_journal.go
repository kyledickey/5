package moduleintegration

import (
	"context"
	"errors"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"log/slog"

	"github.com/bwmarrin/discordgo"
)

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
