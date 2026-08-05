// Package localtool implements the local-side execution of cloud-proposed tool
// calls, starting with the path confinement every local file access must clear.
package localtool

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrEscapesRoot is returned when a path resolves outside the confinement root.
var ErrEscapesRoot = errors.New("path escapes the allowed root")

// Confine resolves path against root and guarantees the result stays inside
// root, defeating "../" traversal, absolute-outside paths, and symlink escapes.
// The returned path is absolute (and symlink-resolved when the target exists).
func Confine(root, path string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = real
	}

	target := path
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, target)
	}
	target = filepath.Clean(target)

	// Layer 1: lexical containment (rejects ../ and absolute-outside).
	if !within(absRoot, target) {
		return "", ErrEscapesRoot
	}

	// Layer 2: symlink containment (only meaningful when the target exists).
	if real, err := filepath.EvalSymlinks(target); err == nil {
		if !within(absRoot, real) {
			return "", ErrEscapesRoot
		}
		return real, nil
	}

	// Layer 3: nonexistent leaf — the lexical check above only proves the
	// *clean* path is inside root, NOT that the path it would be created at
	// stays inside root once symlinks in its existing prefix are resolved.
	// e.g. root/linkdir is a symlink to /outside; root/linkdir/new.txt does
	// not exist, so EvalSymlinks(target) fails and we fall through here, but
	// the parent resolves OUTSIDE root. Resolve the deepest EXISTING ancestor
	// and re-check containment of that resolved ancestor before accepting the
	// lexical target. (Benign for read_file — the leaf must exist — but a
	// write-outside-root hole once the deferred write_file lands.)
	ancestor := filepath.Dir(target)
	for {
		if real, err := filepath.EvalSymlinks(ancestor); err == nil {
			if !within(absRoot, real) {
				return "", ErrEscapesRoot
			}
			break
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			break // reached filesystem root without an existing ancestor
		}
		ancestor = parent
	}

	return target, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
