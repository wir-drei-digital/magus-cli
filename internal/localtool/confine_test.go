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

	// Containment must be evaluated on path SEGMENT boundaries, not raw string
	// prefixes: a sibling directory whose name merely starts with the root's
	// name is outside the root. Load-bearing for allowlist matching, where an
	// allow on /proj/a.txt must never match /proj/a.txt.bak.
	t.Run("string-prefix sibling of root rejected", func(t *testing.T) {
		// Build the sibling from the SYMLINK-RESOLVED root so the escaping path
		// really is a raw string-prefix match of the root Confine compares
		// against (on macOS /var/... resolves to /private/var/...).
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		sibling := realRoot + "x"
		if err := os.MkdirAll(sibling, 0o700); err != nil {
			t.Fatal(err)
		}
		secret := filepath.Join(sibling, "secret.txt")
		if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Confine(root, secret); !errors.Is(err, ErrEscapesRoot) {
			t.Fatalf("expected ErrEscapesRoot for string-prefix sibling, got %v", err)
		}
	})

	// An ancestor that cannot be RESOLVED (EACCES/ELOOP/ENOTDIR) is not the same
	// as an ancestor that does not exist: treating it as missing and climbing to
	// its parent lets an unresolvable directory hide a symlink escape.
	t.Run("unresolvable ancestor rejected", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: permission bits are not enforced")
		}
		priv := filepath.Join(root, "priv")
		if err := os.MkdirAll(priv, 0o700); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(priv, "esc")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if err := os.Chmod(priv, 0o000); err != nil {
			t.Skipf("chmod unsupported: %v", err)
		}
		// Restore before TempDir cleanup, which cannot remove an unreadable dir.
		t.Cleanup(func() { _ = os.Chmod(priv, 0o700) })

		if _, err := Confine(root, "priv/esc/secret.txt"); !errors.Is(err, ErrEscapesRoot) {
			t.Fatalf("expected ErrEscapesRoot for unresolvable ancestor, got %v", err)
		}
	})

	// The ancestor walk starts at Dir(target), so it never classifies the
	// target's OWN final component. A leaf that exists but cannot be fully
	// resolved must therefore be rejected by the leaf check, not left to the
	// walk (which would only inspect the leaf's parent and find it contained).

	// Variant (a): the escaping symlink IS the leaf, under a directory that
	// cannot be traversed. The walk would check root/priv2 — inside root — and
	// accept the escaping path.
	t.Run("unresolvable symlink leaf rejected", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: permission bits are not enforced")
		}
		priv := filepath.Join(root, "priv2")
		if err := os.MkdirAll(priv, 0o700); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(priv, "esc")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if err := os.Chmod(priv, 0o000); err != nil {
			t.Skipf("chmod unsupported: %v", err)
		}
		// Restore before TempDir cleanup, which cannot remove an unreadable dir.
		t.Cleanup(func() { _ = os.Chmod(priv, 0o700) })

		if _, err := Confine(root, "priv2/esc"); !errors.Is(err, ErrEscapesRoot) {
			t.Fatalf("expected ErrEscapesRoot for unresolvable symlink leaf, got %v", err)
		}
	})

	// Variant (b): a DANGLING symlink pointing outside root. EvalSymlinks fails
	// with ENOENT — indistinguishable from a plain missing file unless the leaf
	// itself is stat'd — yet a write through this path would land outside root.
	t.Run("dangling symlink leaf escaping root rejected", func(t *testing.T) {
		outside := t.TempDir()
		link := filepath.Join(root, "link2")
		if err := os.Symlink(filepath.Join(outside, "nonexistent"), link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if _, err := Confine(root, "link2"); !errors.Is(err, ErrEscapesRoot) {
			t.Fatalf("expected ErrEscapesRoot for dangling escaping symlink, got %v", err)
		}
	})
}
