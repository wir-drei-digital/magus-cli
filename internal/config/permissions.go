package config

// ChatConfig holds chat-related on-disk settings.
type ChatConfig struct {
	Permissions Permissions `toml:"permissions,omitempty"`
}

// Permissions is the local policy for cloud-proposed local tools. Tier defaults
// are "prompt" | "allow" | "deny"; an empty value means "prompt" (fail-safe).
// Allow rules are the persisted "allow always" decisions. An explicit "deny"
// tier overrides persisted allow rules: rules only upgrade prompt to allow.
type Permissions struct {
	Read  string      `toml:"read,omitempty"`
	Write string      `toml:"write,omitempty"`
	Exec  string      `toml:"exec,omitempty"`
	Allow []AllowRule `toml:"allow,omitempty"`
}

// AllowRule pre-approves a tool for paths/commands under a prefix.
type AllowRule struct {
	Tool       string `toml:"tool"`
	PathPrefix string `toml:"path_prefix,omitempty"`
}
