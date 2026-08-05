package localtool

import (
	"os"
	"path/filepath"
	"strconv"
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

// The path is server-controlled and lands in the human approval prompt. Control
// bytes let a hostile path forge additional prompt lines (\n), rewind the cursor
// (\r) or repaint the terminal (ESC), so the human approves one thing while the
// prompt shows another. Validate must fail closed BEFORE any prompt is rendered.
func TestReadFileValidateRejectsControlCharacters(t *testing.T) {
	rf := &ReadFile{Root: t.TempDir(), MaxBytes: 1024}

	hostile := map[string]string{
		"forged prompt line": "\n\nread_file: /etc/shadow\r\x1b[K",
		"newline":            "notes\n.txt",
		"carriage return":    "notes\r.txt",
		"escape":             "notes\x1b[2K.txt",
		"NUL":                "notes\x00.txt",
		"tab":                "notes\t.txt",
		"DEL":                "notes\x7f.txt",
	}
	for name, p := range hostile {
		t.Run(name, func(t *testing.T) {
			if err := rf.Validate(map[string]any{"path": p}); err == nil {
				t.Errorf("expected rejection of control-character path %q", p)
			}
		})
	}

	// Byte-wise rejection must not collaterally reject legitimate non-ASCII
	// names: every byte of a multi-byte UTF-8 rune is >= 0x80.
	for _, ok := range []string{"nötes.txt", "日本語.txt", "emoji-🎉.txt", "with space.txt"} {
		if err := rf.Validate(map[string]any{"path": ok}); err != nil {
			t.Errorf("legitimate path %q rejected: %v", ok, err)
		}
	}
}

// Plan is reachable without Validate, and it is where server bytes become both
// the approval Display and the MatchPath that allowlist rules are persisted
// against. It must fail closed on its own rather than trust the caller.
func TestReadFilePlanRejectsControlCharacters(t *testing.T) {
	rf := &ReadFile{Root: t.TempDir(), MaxBytes: 1024}

	plan, err := rf.Plan(map[string]any{"path": "\n\nread_file: /etc/shadow\r\x1b[K"})
	if err == nil {
		t.Fatalf("Plan accepted a control-character path: Display=%q MatchPath=%q",
			plan.Display, plan.MatchPath)
	}
	if plan.Display != "" || plan.MatchPath != "" {
		t.Errorf("rejected Plan should be zero-valued, got %+v", plan)
	}
}

// Defense in depth for what Validate cannot reach: Root is operator-supplied,
// is joined into every resolved path, and is not guaranteed printable. Even
// then the Display must stay a single, non-repainting line.
func TestReadFilePlanDisplayNeutralizesControlCharacters(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ro\not\x1b[K")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Skipf("cannot create a root with control characters: %v", err)
	}
	rf := &ReadFile{Root: root, MaxBytes: 1024}

	plan, err := rf.Plan(map[string]any{"path": "notes.txt"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"\n", "\r", "\x1b", "\x00"} {
		if strings.Contains(plan.Display, bad) {
			t.Errorf("Display leaks raw %q: %q", bad, plan.Display)
		}
	}
	// A single logical line is the property the approver depends on.
	if got := strings.Count(plan.Display, "\n"); got != 0 {
		t.Errorf("Display spans %d extra lines: %q", got, plan.Display)
	}
}

// Clean paths must stay readable — quoting everything would be safe but ugly,
// so the escaping is conditional and that condition needs a test.
func TestReadFilePlanDisplayKeepsCleanPathsReadable(t *testing.T) {
	root := t.TempDir()
	rf := &ReadFile{Root: root, MaxBytes: 1024}

	plan, err := rf.Plan(map[string]any{"path": "notes.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Display, `"`) {
		t.Errorf("clean path should not be quoted: %q", plan.Display)
	}
	if !strings.HasPrefix(plan.Display, "read_file: /") {
		t.Errorf("expected a bare absolute path in Display, got %q", plan.Display)
	}
}

// Execute must not trust a Plan it did not produce: confinement lives in Plan,
// so Execute needs its own kernel-enforced containment. Plan.path is unexported
// but reachable from in-package code (and from the future dispatch pipeline).
func TestReadFileExecuteRejectsForgedPlan(t *testing.T) {
	root := t.TempDir()
	rf := &ReadFile{Root: root, MaxBytes: 1024}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("zero value plan", func(t *testing.T) {
		if _, err := rf.Execute(Plan{}); err == nil {
			t.Error("expected error for a zero-value Plan")
		}
	})

	t.Run("absolute path outside root", func(t *testing.T) {
		if _, err := rf.Execute(Plan{path: "/etc/passwd"}); err == nil {
			t.Error("expected error for a forged out-of-root path")
		}
	})

	t.Run("dotdot escape from root", func(t *testing.T) {
		escape := filepath.Join(realRoot, "..", "..", "etc", "passwd")
		if _, err := rf.Execute(Plan{path: escape}); err == nil {
			t.Error("expected error for a forged ../ escape")
		}
	})
}

// The +1 on the LimitReader is what separates "exactly at the cap" from
// "actually truncated"; an off-by-one here silently corrupts every full read.
func TestReadFileExecuteSizeBoundaries(t *testing.T) {
	root := t.TempDir()
	const content = "hello"
	if err := os.WriteFile(filepath.Join(root, "s.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	read := func(t *testing.T, maxBytes int) (string, bool) {
		t.Helper()
		rf := &ReadFile{Root: root, MaxBytes: maxBytes}
		plan, err := rf.Plan(map[string]any{"path": "s.txt"})
		if err != nil {
			t.Fatal(err)
		}
		out, err := rf.Execute(plan)
		if err != nil {
			t.Fatal(err)
		}
		res := out.(map[string]any)
		return res["content"].(string), res["truncated"].(bool)
	}

	t.Run("exact fit is not truncated", func(t *testing.T) {
		got, truncated := read(t, len(content))
		if got != content || truncated {
			t.Errorf("got (%q, %v), want (%q, false)", got, truncated, content)
		}
	})

	t.Run("one byte under caps and flags", func(t *testing.T) {
		got, truncated := read(t, len(content)-1)
		if got != content[:len(content)-1] || !truncated {
			t.Errorf("got (%q, %v), want (%q, true)", got, truncated, content[:len(content)-1])
		}
	})

	t.Run("non-positive MaxBytes applies the default cap", func(t *testing.T) {
		got, truncated := read(t, 0)
		if got != content || truncated {
			t.Errorf("got (%q, %v), want (%q, false)", got, truncated, content)
		}
	})
}

// os.Root guarantees the result stays beneath the root, but says nothing about
// WHAT the result is. Only regular files may be read: a directory, and (see
// readfile_fifo_test.go) a FIFO, must fail closed rather than be opened.
func TestReadFileExecuteRejectsNonRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "d"), 0o700); err != nil {
		t.Fatal(err)
	}
	rf := &ReadFile{Root: root, MaxBytes: 1024}

	plan, err := rf.Plan(map[string]any{"path": "d"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rf.Execute(plan); err == nil {
		t.Error("expected error reading a directory")
	}
}

// A symlink leaf swapped in after approval stays beneath root when it points at
// another in-root file, so os.Root alone would follow it and read a file the
// human never approved. Plan resolves legitimate symlinks itself (Confine
// returns the EvalSymlinks target), so Execute never needs to follow one.
func TestReadFileExecuteRejectsSymlinkLeafSwappedAfterPlan(t *testing.T) {
	root := t.TempDir()
	approved := filepath.Join(root, "approved.txt")
	if err := os.WriteFile(approved, []byte("approved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	rf := &ReadFile{Root: root, MaxBytes: 1024}

	plan, err := rf.Plan(map[string]any{"path": "approved.txt"})
	if err != nil {
		t.Fatal(err)
	}
	// The approval window: swap the approved leaf for an in-root symlink.
	if err := os.Remove(approved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("secret.txt", approved); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	out, err := rf.Execute(plan)
	if err == nil {
		t.Fatalf("expected error, but read through the swapped symlink: %#v", out)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error should not leak the swap target: %v", err)
	}
}

func TestDisplayPath(t *testing.T) {
	clean := "/home/u/notes with space.txt"
	if got := displayPath(clean); got != clean {
		t.Errorf("clean path should render bare: got %q", got)
	}
	hostile := "/home/u/\nread_file: /etc/shadow"
	got := displayPath(hostile)
	if strings.Contains(got, "\n") {
		t.Errorf("hostile path leaked a raw newline: %q", got)
	}
	if got != strconv.Quote(hostile) {
		t.Errorf("expected a quoted rendering, got %q", got)
	}
	// The existing Display assertion (strings.Contains of the raw path) has to
	// keep working, which quoting preserves for clean paths.
	if !strings.Contains(displayPath(clean), clean) {
		t.Errorf("quoting broke substring matching for %q", clean)
	}
}
