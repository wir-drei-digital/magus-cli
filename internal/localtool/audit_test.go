package localtool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	au := &FileAudit{Path: path}

	if err := au.Record(AuditEntry{Tool: "read_file", Display: "read_file: /a/b", Decision: "allow", ConversationID: "c1"}); err != nil {
		t.Fatal(err)
	}
	if err := au.Record(AuditEntry{Tool: "read_file", Display: "read_file: /a/c", Decision: "deny", ConversationID: "c1"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d", len(lines))
	}
	var first AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Decision != "allow" || first.Tool != "read_file" {
		t.Errorf("bad first entry: %+v", first)
	}
}

// Audit entries carry absolute local filesystem paths, so the log must not be
// world- or group-readable. Guards the deliberate 0o700 dir / 0o600 file modes.
func TestAuditIsOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	path := filepath.Join(dir, "audit.jsonl")
	au := &FileAudit{Path: path}

	if err := au.Record(AuditEntry{Tool: "read_file", Display: "read_file: /a/b", Decision: "allow"}); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("audit file mode = %#o, want 0600", got)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("audit dir mode = %#o, want 0700", got)
	}
}
