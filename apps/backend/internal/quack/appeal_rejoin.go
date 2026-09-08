package quack

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var discordInviteCode = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// normalizeAppealRejoinURL accepts only Discord invite links, returning a canonical
// URL safe to include in member notifications. Empty input removes the link.
func normalizeAppealRejoinURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || len(value) > 256 || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: use an HTTPS Discord invite link", ErrGuildSettingsValidation)
	}
	code := ""
	switch strings.ToLower(parsed.Host) {
	case "discord.gg":
		code = strings.TrimPrefix(parsed.Path, "/")
	case "discord.com":
		code = strings.TrimPrefix(parsed.Path, "/invite/")
		if code == parsed.Path {
			code = ""
		}
	}
	if !discordInviteCode.MatchString(code) {
		return "", fmt.Errorf("%w: use an HTTPS Discord invite link", ErrGuildSettingsValidation)
	}
	return "https://discord.gg/" + code, nil
}
