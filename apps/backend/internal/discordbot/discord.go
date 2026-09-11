package discordbot

import (
	"net/http"
	"sync/atomic"

	"github.com/bwmarrin/discordgo"
)

const discordUserGuildsURL = "https://discord.com/api/v10/users/@me/guilds"

// Bot owns the Discord gateway session and adapts Discord operations to the
// ports in internal/quack. Session is required and is set by New; callers that
// build a Bot literal (module integrations) must supply one. HTTPClient is used
// only for OAuth user requests and evidence downloads and defaults to
// http.DefaultClient when nil. connected tracks gateway readiness for health checks.
type Bot struct {
	Session    *discordgo.Session
	HTTPClient *http.Client
	connected  atomic.Bool
}

// New creates a session for token with the Guilds intent and a bounded message
// cache, and registers the readiness handlers. It does not open the gateway;
// the runtime adds module intents first and then calls Open.
func New(token string) (*Bot, error) {
	session, err := discordgo.New(token)
	if err != nil {
		return nil, err
	}
	// Runtime adds only the intents required by currently enabled optional
	// modules before opening the gateway.
	session.Identify.Intents = discordgo.IntentGuilds
	session.StateEnabled = true
	session.State.MaxMessageCount = 5000
	bot := &Bot{Session: session, HTTPClient: http.DefaultClient}
	session.AddHandler(bot.gatewayReady)
	session.AddHandler(bot.gatewayResumed)
	session.AddHandler(bot.gatewayConnected)
	session.AddHandler(bot.gatewayDisconnected)
	return bot, nil
}

// gatewayReady marks the authenticated initial gateway session ready.
func (b *Bot) gatewayReady(_ *discordgo.Session, _ *discordgo.Ready) { b.connected.Store(true) }

// gatewayResumed marks a successfully resumed gateway session ready.
func (b *Bot) gatewayResumed(_ *discordgo.Session, _ *discordgo.Resumed) { b.connected.Store(true) }

// gatewayConnected marks DiscordGo's post-handshake synthetic connect event ready.
func (b *Bot) gatewayConnected(_ *discordgo.Session, _ *discordgo.Connect) { b.connected.Store(true) }

// gatewayDisconnected immediately removes Discord from process readiness while reconnecting.
func (b *Bot) gatewayDisconnected(_ *discordgo.Session, _ *discordgo.Disconnect) {
	b.connected.Store(false)
}

// Open connects to the gateway and marks the bot ready. Startup fails before
// serving traffic when Discord rejects the token or is unreachable.
func (b *Bot) Open() error {
	if err := b.Session.Open(); err != nil {
		return err
	}
	b.connected.Store(true)
	return nil
}

// Close disconnects the gateway once. It tolerates a nil receiver and a bot
// that was never opened so reverse-order shutdown can call it unconditionally.
func (b *Bot) Close() error {
	if b == nil || b.Session == nil {
		return nil
	}
	if !b.connected.Swap(false) {
		return nil
	}
	return b.Session.Close()
}

// Status reports gateway readiness, the bot's username and heartbeat latency
// for health endpoints. It is not ready until the session state has a user.
func (b *Bot) Status() (bool, string, int64) {
	if !b.connected.Load() || b.Session.State == nil || b.Session.State.User == nil {
		return false, "", 0
	}
	return true, b.Session.State.User.Username, b.Session.HeartbeatLatency().Milliseconds()
}
