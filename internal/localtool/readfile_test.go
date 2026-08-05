package localtool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileValidate(t *testing.T) {
	rf := &ReadFile{Root: t.TempDir(), MaxBytes: 1024}
	if err := rf.Validate(map[string]any{}); err == nil {
		t.Error("expected error for missing path")
	}
	if err := rf.Validate(map[string]any{"path": 123}); err == nil {
		t.Error("expected error for non-string path")
	}
	if err := rf.Validate(map[string]any{"path": "a.txt"}); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestReadFilePlanConfinesAndDescribes(t *testing.T) {
	root := t.TempDir()
	rf := &ReadFile{Root: root, MaxBytes: 1024}

	plan, err := rf.Plan(map[string]any{"path": "notes.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Tool != "read_file" || plan.Tier != "read" {
		t.Fatalf("bad plan: %+v", plan)
	}
	if !strings.Contains(plan.Display, filepath.Join(root, "notes.txt")) {
		t.Errorf("display should show the canonical path: %q", plan.Display)
	}

	if _, err := rf.Plan(map[string]any{"path": "../escape"}); err == nil {
		t.Error("expected confinement error in Plan")
	}
}

func TestReadFileExecuteCapsSize(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	rf := &ReadFile{Root: root, MaxBytes: 4}

	plan, err := rf.Plan(map[string]any{"path": "big.txt"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := rf.Execute(plan)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	if res["content"].(string) != "0123" {
		t.Errorf("expected capped content, got %q", res["content"])
	}
	if res["truncated"].(bool) != true {
		t.Errorf("expected truncated=true")
	}
}

func TestReadFileExecuteMissingFile(t *testing.T) {
	rf := &ReadFile{Root: t.TempDir(), MaxBytes: 1024}
	plan, _ := rf.Plan(map[string]any{"path": "nope.txt"})
	if _, err := rf.Execute(plan); err == nil {
		t.Error("expected error reading a missing file")
	}
}
