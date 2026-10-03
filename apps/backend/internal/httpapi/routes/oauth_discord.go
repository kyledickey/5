package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/quackdiscord/bot/internal/config"
)

func validateDiscordOAuthConfig(cfg config.Config) error {
	if strings.TrimSpace(cfg.Discord.AppID) == "" {
		return fmt.Errorf("discord oauth is not configured missing DISCORD_APP_ID")
	}
	if strings.TrimSpace(cfg.Discord.ClientSecret) == "" {
		return fmt.Errorf("discord oauth is not configured missing DISCORD_CLIENT_SECRET")
	}
	if strings.TrimSpace(cfg.Discord.OAuthRedirectURI) == "" {
		return fmt.Errorf("discord oauth is not configured missing DISCORD_OAUTH_REDIRECT_URI")
	}
	return nil
}

func buildDiscordAuthURL(cfg config.Config, state string) string {
	v := url.Values{}
	v.Set("client_id", cfg.Discord.AppID)
	v.Set("redirect_uri", cfg.Discord.OAuthRedirectURI)
	v.Set("response_type", "code")
	v.Set("scope", cfg.Discord.OAuthScopes)
	v.Set("state", state)

	return discordAuthorizeURL + "?" + v.Encode()
}

func exchangeDiscordCode(ctx context.Context, cfg config.Config, code string) (*discordTokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", cfg.Discord.AppID)
	body.Set("client_secret", cfg.Discord.ClientSecret)
	body.Set("grant_type", "authorization_code")
	body.Set("code", code)
	body.Set("redirect_uri", cfg.Discord.OAuthRedirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordTokenEndpoint, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := discordHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	var token discordTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDiscordBodyBytes)).Decode(&token); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}

	if resp.StatusCode >= 400 || token.AccessToken == "" || token.ExpiresIn <= 0 {
		return nil, fmt.Errorf("discord token exchange rejected")
	}

	return &token, nil
}

func fetchDiscordUser(ctx context.Context, accessToken string) (*discordUserResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discordMeEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := discordHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("user request failed: %w", err)
	}
	defer resp.Body.Close()

	var user discordUserResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDiscordBodyBytes)).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode user response: %w", err)
	}

	if resp.StatusCode >= 400 || user.ID == "" {
		return nil, fmt.Errorf("discord user fetch rejected")
	}

	return &user, nil
}

func discordAvatarURL(userID, avatarHash string) string {
	if userID == "" || avatarHash == "" {
		return ""
	}

	ext := "png"
	if strings.HasPrefix(avatarHash, "a_") {
		ext = "gif"
	}

	return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.%s", userID, avatarHash, ext)
}

// sanitizeRedirectTarget permits only local paths and URLs on the configured
// dashboard origin, so a crafted redirect_to cannot bounce a freshly minted
// session off to an attacker's host.
func sanitizeRedirectTarget(target, fallback string) string {
	target, fallback = strings.TrimSpace(target), strings.TrimSpace(fallback)
	fallbackURL, ok := safeRedirectURL(fallback)
	if !ok {
		fallback, fallbackURL = "/", &url.URL{Path: "/"}
	}
	targetURL, ok := safeRedirectURL(target)
	if !ok {
		return fallback
	}
	if targetURL.Host == "" {
		return target
	}
	if strings.EqualFold(targetURL.Scheme, fallbackURL.Scheme) && strings.EqualFold(targetURL.Host, fallbackURL.Host) {
		return target
	}
	return fallback
}

// safeRedirectURL rejects browser-normalized network paths and non-HTTP destinations.
func safeRedirectURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || raw == "" || parsed.User != nil || strings.ContainsAny(raw, "\\\r\n\t") || strings.Contains(parsed.Path, "\\") || strings.HasPrefix(parsed.Path, "//") {
		return nil, false
	}
	if parsed.Scheme == "" {
		return parsed, strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") && parsed.Host == ""
	}
	return parsed, (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.Opaque == ""
}
