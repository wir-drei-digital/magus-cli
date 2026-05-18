package skill_test

import (
	"os"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/skill"
)

// TestSkillContentMatchesPluginCopy guards against the embedded copy at
// internal/skill/SKILL.md drifting from the canonical Claude Code plugin copy
// at plugins/magus/skills/magus/SKILL.md. Edits should be made to the plugin
// copy; `make sync-skill` (or `make build`) regenerates the embedded copy.
func TestSkillContentMatchesPluginCopy(t *testing.T) {
	pluginCopy, err := os.ReadFile("../../plugins/magus/skills/magus/SKILL.md")
	if err != nil {
		t.Fatalf("read plugin copy: %v", err)
	}
	if skill.Content != string(pluginCopy) {
		t.Fatal("internal/skill/SKILL.md and plugins/magus/skills/magus/SKILL.md have diverged; run `make sync-skill` to fix")
	}
}
