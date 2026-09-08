package config

import (
	"errors"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// normalizeApplicationBaseURL validates the optional user-facing website base
// independently of browser CORS policy. Canonical paths preserve an application
// mount prefix while making subsequent relative link concatenation predictable.
// Errors name the setting without echoing credentials from malformed input.
func normalizeApplicationBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(raw, "#") || parsed.Opaque != "" {
		return "", errors.New("APPLICATION_BASE_URL must be an absolute HTTPS base URL without credentials, query or fragment")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", errors.New("APPLICATION_BASE_URL port must be between 1 and 65535")
		}
	}
	parsed.Path = strings.TrimRight(path.Clean("/"+strings.TrimLeft(parsed.Path, "/")), "/")
	parsed.RawPath = ""
	return parsed.String(), nil
}
