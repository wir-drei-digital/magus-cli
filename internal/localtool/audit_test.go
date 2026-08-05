package localtool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// The design spec requires every audit line to carry a timestamp. Record stamps
// UTC now when the caller left TS zero, and preserves a caller-supplied TS so
// callers (and tests) can pin it.
func TestAuditStampsTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	au := &FileAudit{Path: path}

	before := time.Now().UTC()
	if err := au.Record(AuditEntry{Tool: "read_file", Display: "read_file: /a/b", Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	preset := time.Date(2020, 1, 2, 3, 4, 5, 123456789, time.UTC)
	if err := au.Record(AuditEntry{Tool: "read_file", Display: "read_file: /a/c", Decision: "deny", TS: preset}); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()

	entries := readAuditLines(t, path)
	if len(entries) != 2 {
		t.Fatalf("expected 2 audit lines, got %d", len(entries))
	}

	stamped := entries[0].TS
	if stamped.IsZero() {
		t.Fatalf("Record left ts zero: %+v", entries[0])
	}
	if stamped.Before(before) || stamped.After(after) {
		t.Errorf("ts %s outside [%s, %s]", stamped, before, after)
	}
	if _, offset := stamped.Zone(); offset != 0 {
		t.Errorf("ts is not UTC: %s", stamped)
	}

	if got := entries[1].TS; !got.Equal(preset) {
		t.Errorf("caller-supplied ts = %s, want %s", got, preset)
	}
}

// Audit entries carry absolute local filesystem paths, so the log must not be
// world- or group-readable. Guards the deliberate 0o700 dir / 0o600 file modes,
// including for a log file that already existed with wider permissions.
func TestAuditIsOwnerOnly(t *testing.T) {
	t.Run("new file and dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "audit")
		path := filepath.Join(dir, "audit.jsonl")
		au := &FileAudit{Path: path}

		if err := au.Record(AuditEntry{Tool: "read_file", Display: "read_file: /a/b", Decision: "allow"}); err != nil {
			t.Fatal(err)
		}

		if got := statPerm(t, path); got != 0o600 {
			t.Errorf("audit file mode = %#o, want 0600", got)
		}
		if got := statPerm(t, dir); got != 0o700 {
			t.Errorf("audit dir mode = %#o, want 0700", got)
		}
	})

	// A log left world-readable by an earlier run (or a permissive umask) must be
	// tightened: OpenFile's perm argument applies only when it creates the file.
	t.Run("pre-existing wide-open file is tightened", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "audit.jsonl")
		if err := os.WriteFile(path, []byte(`{"tool":"read_file","decision":"allow"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := statPerm(t, path); got != 0o644 {
			t.Fatalf("precondition: mode = %#o, want 0644", got)
		}

		au := &FileAudit{Path: path}
		if err := au.Record(AuditEntry{Tool: "read_file", Display: "read_file: /a/b", Decision: "allow"}); err != nil {
			t.Fatal(err)
		}

		if got := statPerm(t, path); got != 0o600 {
			t.Errorf("pre-existing audit file mode = %#o, want 0600", got)
		}
		if entries := readAuditLines(t, path); len(entries) != 2 {
			t.Errorf("expected append to keep the existing line, got %d lines", len(entries))
		}
	})
}

// Record must surface failures rather than reporting a write that never landed.
func TestAuditRecordReportsFailure(t *testing.T) {
	// Path is an existing directory: the open cannot succeed.
	au := &FileAudit{Path: t.TempDir()}
	if err := au.Record(AuditEntry{Tool: "read_file", Decision: "allow"}); err == nil {
		t.Fatal("expected an error when the audit log cannot be opened, got nil")
	}
}

func statPerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func readAuditLines(t *testing.T, path string) []AuditEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []AuditEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad audit line %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	return entries
}
