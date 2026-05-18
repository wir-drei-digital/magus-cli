package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/skill"
)

func TestResolveSkillPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name    string
		target  string
		path    string
		want    string
		wantErr bool
	}{
		{"default claude-code", "claude-code", "", filepath.Join(home, ".claude", "skills", "magus.md"), false},
		{"claude-code-legacy alias", "claude-code-legacy", "", filepath.Join(home, ".claude", "skills", "magus.md"), false},
		{"codex", "codex", "", filepath.Join(home, ".codex", "skills", "magus.md"), false},
		{"path overrides target", "claude-code", "/tmp/custom.md", "/tmp/custom.md", false},
		{"unknown target", "anthropic-workbench", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSkillPath(tc.target, tc.path)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestWriteSkillRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "magus.md")

	if err := writeSkill(dest, false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeSkill(dest, false); err == nil {
		t.Fatal("expected error on second write without --update")
	}
	if err := writeSkill(dest, true); err != nil {
		t.Fatalf("update overwrite: %v", err)
	}

	content, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != skill.Content {
		t.Error("written content differs from embedded skill")
	}
}

func TestWriteSkillCreatesDir(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "nested", "skills", "magus.md")

	if err := writeSkill(dest, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal(err)
	}
}

func TestSkillContentNonEmpty(t *testing.T) {
	if skill.Content == "" {
		t.Fatal("embedded skill content is empty")
	}
	if len(skill.Content) < 500 {
		t.Errorf("skill content suspiciously short: %d bytes", len(skill.Content))
	}
}
