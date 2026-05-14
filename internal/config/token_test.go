package config

import (
	"testing"
)

func TestResolveTokenEnvOverride(t *testing.T) {
	t.Setenv("MAGUS_API_TOKEN", "env_token")
	cfg := &Config{
		DefaultProfile: "p",
		Profiles:       map[string]Profile{"p": {Token: "file_token"}},
	}
	if got := ResolveToken(cfg, ""); got != "env_token" {
		t.Errorf("env should win, got %q", got)
	}
}

func TestResolveTokenFromProfile(t *testing.T) {
	t.Setenv("MAGUS_API_TOKEN", "")
	cfg := &Config{
		DefaultProfile: "p",
		Profiles:       map[string]Profile{"p": {Token: "file_token"}},
	}
	if got := ResolveToken(cfg, ""); got != "file_token" {
		t.Errorf("expected file_token, got %q", got)
	}
}

func TestResolveAPIURLFallback(t *testing.T) {
	t.Setenv("MAGUS_API_URL", "")
	cfg := &Config{Profiles: map[string]Profile{}}
	if got := ResolveAPIURL(cfg, "", "https://fallback.test"); got != "https://fallback.test" {
		t.Errorf("expected fallback, got %q", got)
	}
}
