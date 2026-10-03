package discordbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// UserGuilds lists the guilds a dashboard user belongs to using their OAuth
// bearer token, not the bot token. It goes through HTTPClient so tests and the
// OAuth flow can share a client.
func (b *Bot) UserGuilds(ctx context.Context, accessToken string) ([]quack.DiscordUserGuild, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, errors.New("missing discord access token")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, discordUserGuildsURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	client := b.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("discord user guilds failed with status %d", response.StatusCode)
	}
	var guilds []quack.DiscordUserGuild
	if err := json.NewDecoder(response.Body).Decode(&guilds); err != nil {
		return nil, err
	}
	return guilds, nil
}

// BotGuild returns display metadata for one guild the bot is in, preferring the
// gateway cache and falling back to one REST read. Any failure is reported as
// quack.ErrBotNotInGuild because callers only need to know whether to proceed.
func (b *Bot) BotGuild(ctx context.Context, guildID string) (*quack.DiscordBotGuild, error) {
	if b.Session.State != nil {
		if guild, err := b.Session.State.Guild(guildID); err == nil && guild != nil {
			return botGuild(guild), nil
		}
	}
	guild, err := b.Session.Guild(guildID, singleAttempt(ctx)...)
	if err != nil {
		return nil, quack.ErrBotNotInGuild
	}
	return botGuild(guild), nil
}

// BotGuilds lists every guild in the gateway cache. It never calls Discord, so
// before the gateway has delivered its guilds the list is empty, not an error.
func (b *Bot) BotGuilds(context.Context) ([]quack.DiscordBotGuild, error) {
	if b.Session.State == nil {
		return []quack.DiscordBotGuild{}, nil
	}
	b.Session.State.RLock()
	defer b.Session.State.RUnlock()
	guilds := make([]quack.DiscordBotGuild, 0, len(b.Session.State.Guilds))
	for _, guild := range b.Session.State.Guilds {
		if guild != nil {
			guilds = append(guilds, *botGuild(guild))
		}
	}
	return guilds, nil
}

// botGuild copies the display fields the core needs from a discordgo guild.
func botGuild(guild *discordgo.Guild) *quack.DiscordBotGuild {
	return &quack.DiscordBotGuild{ID: guild.ID, Name: guild.Name, Icon: guild.Icon, OwnerID: guild.OwnerID}
}

// GuildAuthorization fetches current guild, actor, bot, and optional target state directly from Discord for one protected request.
func (b *Bot) GuildAuthorization(ctx context.Context, guildID, actorDiscordUserID, targetDiscordUserID string) (*quack.DiscordGuildAuthorization, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || b.Session == nil {
		return nil, quack.ErrAuthorizationUnavailable
	}
	guild, err := b.Session.Guild(guildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, guildAuthorizationError(err)
	}
	if guild == nil {
		return nil, quack.ErrAuthorizationUnavailable
	}

	botID := ""
	if b.Session.State != nil && b.Session.State.User != nil {
		botID = b.Session.State.User.ID
	}
	if botID == "" {
		botUser, userErr := b.Session.User("@me", discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if userErr != nil || botUser == nil {
			return nil, quack.ErrAuthorizationUnavailable
		}
		botID = botUser.ID
	}

	actor, err := b.liveMemberAuthorization(ctx, guild, actorDiscordUserID)
	if err != nil {
		return nil, err
	}
	bot, err := b.liveMemberAuthorization(ctx, guild, botID)
	if err != nil {
		return nil, err
	}
	snapshot := &quack.DiscordGuildAuthorization{Guild: *botGuild(guild), Actor: actor, Bot: bot}
	if strings.TrimSpace(targetDiscordUserID) != "" {
		target, targetErr := b.liveMemberAuthorization(ctx, guild, targetDiscordUserID)
		if targetErr != nil {
			return nil, targetErr
		}
		snapshot.Target = &target
	}
	return snapshot, nil
}

// guildAuthorizationError preserves inactive-guild semantics while hiding transient Discord failures.
func guildAuthorizationError(err error) error {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		switch restErr.Response.StatusCode {
		case http.StatusForbidden, http.StatusNotFound:
			return quack.ErrBotNotInGuild
		}
	}
	return quack.ErrAuthorizationUnavailable
}

// liveMemberAuthorization fetches one current member and safely represents Discord's unknown-member response as non-membership.
func (b *Bot) liveMemberAuthorization(ctx context.Context, guild *discordgo.Guild, userID string) (quack.DiscordMemberAuthorization, error) {
	memberState := quack.DiscordMemberAuthorization{DiscordUserID: strings.TrimSpace(userID)}
	if memberState.DiscordUserID == "" {
		return memberState, nil
	}
	if err := ctx.Err(); err != nil {
		return memberState, err
	}
	member, err := b.Session.GuildMember(guild.ID, memberState.DiscordUserID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		var restErr *discordgo.RESTError
		if errors.As(err, &restErr) && restErr.Response != nil && restErr.Response.StatusCode == http.StatusNotFound {
			return memberState, nil
		}
		return memberState, quack.ErrAuthorizationUnavailable
	}
	return discordMemberAuthorization(guild, member), nil
}

// discordMemberAuthorization calculates guild-level permission and hierarchy state from a fresh guild/member response.
func discordMemberAuthorization(guild *discordgo.Guild, member *discordgo.Member) quack.DiscordMemberAuthorization {
	if guild == nil || member == nil || member.User == nil {
		return quack.DiscordMemberAuthorization{}
	}
	permissions := int64(0)
	topRolePosition := 0
	roleIDs := make(map[string]struct{}, len(member.Roles))
	for _, roleID := range member.Roles {
		roleIDs[roleID] = struct{}{}
	}
	for _, role := range guild.Roles {
		if role == nil {
			continue
		}
		if role.ID == guild.ID {
			permissions |= role.Permissions
		}
		if _, ok := roleIDs[role.ID]; ok {
			permissions |= role.Permissions
			if role.Position > topRolePosition {
				topRolePosition = role.Position
			}
		}
	}
	if member.User.ID == guild.OwnerID || permissions&discordgo.PermissionAdministrator != 0 {
		permissions |= discordgo.PermissionAll
	}
	displayName := strings.TrimSpace(member.Nick)
	if displayName == "" {
		displayName = strings.TrimSpace(member.User.GlobalName)
	}
	if displayName == "" {
		displayName = member.User.Username
	}
	return quack.DiscordMemberAuthorization{
		DiscordUserID: member.User.ID, DisplayName: displayName,
		PermissionBits: uint64(permissions), TopRolePosition: topRolePosition,
		Present: true, Bot: member.User.Bot,
	}
}

// GuildLifecycleHandler translates Discord guild and channel events into idempotent core lifecycle operations.
type GuildLifecycleHandler struct {
	Guilds   *quack.GuildService
	Evidence *quack.EvidenceService
}

// RegisterGuildLifecycle installs lifecycle handlers before the gateway opens so initial GuildCreate events cannot be missed.
func RegisterGuildLifecycle(session *discordgo.Session, services *quack.Services) error {
	if session == nil {
		return errors.New("discord session is not configured")
	}
	if services == nil || services.Guilds == nil {
		return errors.New("guild service is not configured")
	}
	handler := &GuildLifecycleHandler{Guilds: services.Guilds, Evidence: services.Evidence}
	session.AddHandler(handler.HandleGuildCreate)
	session.AddHandler(handler.HandleGuildUpdate)
	session.AddHandler(handler.HandleGuildDelete)
	session.AddHandler(handler.HandleChannelDelete)
	return nil
}

// HandleGuildCreate installs a new guild or reactivates known history and repairs channel references from the complete create payload.
func (h *GuildLifecycleHandler) HandleGuildCreate(_ *discordgo.Session, event *discordgo.GuildCreate) {
	if h == nil || h.Guilds == nil || event == nil || event.Guild == nil || event.Unavailable {
		return
	}
	input := guildLifecycleInput(event.Guild, channelIDs(event.Channels))
	result, err := h.Guilds.BootstrapDiscordGuild(context.Background(), input)
	if err != nil {
		slog.Error("Failed to bootstrap Discord guild", "error", err, "guild_id", event.ID)
		return
	}
	if h.Evidence != nil && result != nil {
		if _, err := h.Evidence.EnsureGuildEvidenceChannel(context.Background(), result.Guild, result.Settings); err != nil {
			slog.Error("Failed to ensure managed evidence channel", "error", err, "guild_id", event.ID)
		}
	}
}

// HandleGuildUpdate refreshes authoritative name, icon, owner, and active state without treating a partial payload as channel deletion.
func (h *GuildLifecycleHandler) HandleGuildUpdate(_ *discordgo.Session, event *discordgo.GuildUpdate) {
	if h == nil || h.Guilds == nil || event == nil || event.Guild == nil || event.Unavailable {
		return
	}
	result, err := h.Guilds.BootstrapDiscordGuild(context.Background(), guildLifecycleInput(event.Guild, nil))
	if err != nil {
		slog.Error("Failed to refresh Discord guild", "error", err, "guild_id", event.ID)
		return
	}
	if h.Evidence != nil && result != nil {
		if _, err := h.Evidence.EnsureGuildEvidenceChannel(context.Background(), result.Guild, result.Settings); err != nil {
			slog.Error("Detected managed evidence channel drift", "error", err, "guild_id", event.ID)
		}
	}
}

// HandleGuildDelete marks a true bot removal inactive while ignoring Discord's temporary unavailable signal.
func (h *GuildLifecycleHandler) HandleGuildDelete(_ *discordgo.Session, event *discordgo.GuildDelete) {
	if h == nil || h.Guilds == nil || event == nil || event.Guild == nil || event.Unavailable {
		return
	}
	if _, err := h.Guilds.DeactivateDiscordGuild(context.Background(), event.ID); err != nil {
		slog.Error("Failed to deactivate departed Discord guild", "error", err, "guild_id", event.ID)
	}
}

// HandleChannelDelete clears configured references to a channel Discord confirms was deleted.
func (h *GuildLifecycleHandler) HandleChannelDelete(_ *discordgo.Session, event *discordgo.ChannelDelete) {
	if h == nil || h.Guilds == nil || event == nil || event.Channel == nil || event.GuildID == "" {
		return
	}
	if _, err := h.Guilds.ClearDeletedChannel(context.Background(), event.GuildID, event.ID); err != nil {
		slog.Error("Failed to clear deleted Discord channel reference", "error", err, "guild_id", event.GuildID, "channel_id", event.ID)
	}
	if h.Evidence != nil {
		if _, err := h.Evidence.RepairDiscordGuildEvidenceChannel(context.Background(), event.GuildID); err != nil {
			slog.Error("Failed to repair managed evidence channel after deletion", "error", err, "guild_id", event.GuildID)
		}
	}
}

// guildLifecycleInput maps Discord gateway metadata into the transport-neutral lifecycle contract.
func guildLifecycleInput(guild *discordgo.Guild, knownChannelIDs []string) quack.DiscordGuildLifecycleInput {
	return quack.DiscordGuildLifecycleInput{
		DiscordGuildID: guild.ID, Name: guild.Name, Icon: guild.Icon,
		OwnerDiscordUserID: guild.OwnerID, KnownChannelDiscordIDs: knownChannelIDs,
	}
}

// channelIDs returns the complete channel identity inventory supplied by a GuildCreate event.
func channelIDs(channels []*discordgo.Channel) []string {
	if channels == nil {
		return nil
	}
	ids := make([]string, 0, len(channels))
	for _, channel := range channels {
		if channel != nil && channel.ID != "" {
			ids = append(ids, channel.ID)
		}
	}
	return ids
}
