package config

import (
	"errors"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	configFileName = "config.toml"
	configFileMode = 0o600
	configDirMode  = 0o700
)

// Profile represents a workspace-scoped token + its metadata.
type Profile struct {
	APIURL      string `toml:"api_url"`
	WorkspaceID string `toml:"workspace_id,omitempty"`
	Workspace   string `toml:"workspace,omitempty"`
	UserEmail   string `toml:"user_email,omitempty"`
	Scope       string `toml:"scope,omitempty"`
	Token       string `toml:"token"`
	ActiveBrain string `toml:"active_brain,omitempty"`
}

// Config is the root of the on-disk TOML file.
type Config struct {
	DefaultProfile string             `toml:"default_profile"`
	Profiles       map[string]Profile `toml:"profiles"`
}

// Load reads from the default config directory.
func Load() (*Config, error) {
	dir, err := defaultDir()
	if err != nil {
		return nil, err
	}
	return loadFrom(dir)
}

// Save writes to the default config directory.
func (c *Config) Save() error {
	dir, err := defaultDir()
	if err != nil {
		return err
	}
	return c.saveTo(dir)
}

// Active returns the profile to use. `override` is the optional --profile flag;
// when empty, falls back to DefaultProfile. Returns (zero, false) if not found.
func (c *Config) Active(override string) (Profile, bool) {
	name := override
	if name == "" {
		name = c.DefaultProfile
	}
	if name == "" {
		return Profile{}, false
	}
	p, ok := c.Profiles[name]
	return p, ok
}

func loadFrom(dir string) (*Config, error) {
	path := filepath.Join(dir, configFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{Profiles: map[string]Profile{}}, nil
	}
	if err != nil {
		return nil, err
	}
	cfg := &Config{Profiles: map[string]Profile{}}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	return cfg, nil
}

func (c *Config) saveTo(dir string) error {
	if err := os.MkdirAll(dir, configDirMode); err != nil {
		return err
	}
	data, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, data, configFileMode); err != nil {
		return err
	}
	// Defeat a broken/permissive user umask that may have widened
	// the perms set by WriteFile.
	return os.Chmod(path, configFileMode)
}

func defaultDir() (string, error) {
	if env := os.Getenv("MAGUS_CONFIG_DIR"); env != "" {
		return env, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "magus"), nil
}
