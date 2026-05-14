package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEmptyDir(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadFrom(dir)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if len(cfg.Profiles) != 0 {
		t.Errorf("expected empty profiles, got %d", len(cfg.Profiles))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{
		DefaultProfile: "work",
		Profiles: map[string]Profile{
			"work": {
				APIURL:      "https://magus.digital",
				WorkspaceID: "ws-uuid",
				Workspace:   "Acme",
				UserEmail:   "me@example.com",
				Scope:       "write",
				Token:       "mgs_pat_secret123",
			},
		},
	}

	if err := cfg.saveTo(dir); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify file permissions
	info, err := os.Stat(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected mode 0600, got %o", info.Mode().Perm())
	}

	loaded, err := loadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DefaultProfile != "work" {
		t.Errorf("default profile mismatch: %q", loaded.DefaultProfile)
	}
	if loaded.Profiles["work"].Token != "mgs_pat_secret123" {
		t.Error("token round-trip failed")
	}
}

func TestActiveProfileResolution(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		DefaultProfile: "personal",
		Profiles: map[string]Profile{
			"personal": {APIURL: "https://magus.digital", Token: "tok1"},
			"work":     {APIURL: "https://magus.digital", Token: "tok2"},
		},
	}
	if err := cfg.saveTo(dir); err != nil {
		t.Fatal(err)
	}

	// Default
	loaded, _ := loadFrom(dir)
	p, ok := loaded.Active("")
	if !ok || p.Token != "tok1" {
		t.Errorf("default active resolution failed: %+v", p)
	}

	// Override
	p, ok = loaded.Active("work")
	if !ok || p.Token != "tok2" {
		t.Errorf("override resolution failed: %+v", p)
	}

	// Unknown
	if _, ok := loaded.Active("nope"); ok {
		t.Error("expected unknown profile to fail")
	}
}
