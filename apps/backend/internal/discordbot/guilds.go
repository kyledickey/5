package discordbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
