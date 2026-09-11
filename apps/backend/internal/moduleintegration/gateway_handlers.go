package moduleintegration

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
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
