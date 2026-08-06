package config

import "testing"

func TestChatPermissionsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Profiles: map[string]Profile{},
		Chat: ChatConfig{
			Permissions: Permissions{
				Read:  "prompt",
				Write: "deny",
				Exec:  "deny",
				Allow: []AllowRule{{Tool: "read_file", PathPrefix: "/Users/me/proj"}},
			},
		},
	}
	if err := cfg.saveTo(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Chat.Permissions.Write != "deny" {
		t.Errorf("write tier mismatch: %q", loaded.Chat.Permissions.Write)
	}
	if len(loaded.Chat.Permissions.Allow) != 1 || loaded.Chat.Permissions.Allow[0].Tool != "read_file" {
		t.Errorf("allow rule not round-tripped: %+v", loaded.Chat.Permissions.Allow)
	}
}
