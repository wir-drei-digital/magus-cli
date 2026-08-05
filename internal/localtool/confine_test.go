package localtool

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfine(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("relative inside root resolves", func(t *testing.T) {
		got, err := Confine(root, "ok.txt")
		if err != nil {
			t.Fatal(err)
		}
		want, _ := filepath.EvalSymlinks(filepath.Join(root, "ok.txt"))
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("dotdot traversal rejected", func(t *testing.T) {
		if _, err := Confine(root, "../../etc/passwd"); err == nil {
			t.Fatal("expected escape error")
		}
	})

	t.Run("absolute outside root rejected", func(t *testing.T) {
		if _, err := Confine(root, "/etc/passwd"); err == nil {
			t.Fatal("expected escape error")
		}
	})

	t.Run("symlink escaping root rejected", func(t *testing.T) {
		outside := t.TempDir()
		secret := filepath.Join(outside, "secret.txt")
		_ = os.WriteFile(secret, []byte("x"), 0o600)
		link := filepath.Join(root, "link")
		if err := os.Symlink(secret, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if _, err := Confine(root, "link"); err == nil {
			t.Fatal("expected escape error for symlink pointing outside root")
		}
	})

	t.Run("nonexistent inside root passes confinement", func(t *testing.T) {
		if _, err := Confine(root, "newfile.txt"); err != nil {
			t.Fatalf("nonexistent-but-contained should pass, got %v", err)
		}
	})

	// Guards the deferred write_file write-outside-root hole: the leaf does not
	// exist (so the symlink check on the target itself cannot fire), but its
	// parent directory is a symlink resolving outside root.
	t.Run("symlinked parent with nonexistent leaf rejected", func(t *testing.T) {
		outside := t.TempDir()
		link := filepath.Join(root, "linkdir")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if _, err := Confine(root, "linkdir/new.txt"); err == nil {
			t.Fatal("expected escape error for nonexistent leaf under symlinked parent")
		} else if !errors.Is(err, ErrEscapesRoot) {
			t.Fatalf("expected ErrEscapesRoot, got %v", err)
		}
	})
}
