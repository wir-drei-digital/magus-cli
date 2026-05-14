package config

import "os"

// ResolveToken returns the token to use. The MAGUS_API_TOKEN env var wins;
// otherwise the active profile's token. Empty string means no token configured.
func ResolveToken(cfg *Config, profileOverride string) string {
	if env := os.Getenv("MAGUS_API_TOKEN"); env != "" {
		return env
	}
	if p, ok := cfg.Active(profileOverride); ok {
		return p.Token
	}
	return ""
}

// ResolveAPIURL returns the API base URL with the same precedence as
// ResolveToken: MAGUS_API_URL env > profile > the supplied default.
func ResolveAPIURL(cfg *Config, profileOverride string, fallback string) string {
	if env := os.Getenv("MAGUS_API_URL"); env != "" {
		return env
	}
	if p, ok := cfg.Active(profileOverride); ok && p.APIURL != "" {
		return p.APIURL
	}
	return fallback
}
