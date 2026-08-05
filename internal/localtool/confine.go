// Package localtool implements the local-side execution of cloud-proposed tool
// calls, starting with the path confinement every local file access must clear.
package localtool

import (
	"errors"
	"io/fs"
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
	// Any resolution error falls through to layer 3, which is safe ONLY because
	// layer 3 classifies the error: an unresolvable component is rejected there
	// rather than mistaken for a missing one. EvalSymlinks resolves left to
	// right, so an EACCES/ELOOP ancestor surfaces here first and layer 3 hits it
	// again on the very first iteration of its walk.
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
		real, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			if !within(absRoot, real) {
				return "", ErrEscapesRoot
			}
			break
		}
		// Fail closed: ONLY a genuinely missing component justifies climbing to
		// the parent. Any other resolution error (EACCES, ELOOP, ENOTDIR) means
		// containment of this ancestor is UNKNOWN, not that it is absent —
		// treating it as absent lets an unresolvable directory hide a symlink
		// escape. e.g. root/priv is mode 0000 and holds esc -> /outside: the
		// walk would climb past the unreadable component to root/priv, find it
		// inside root, and accept root/priv/esc/secret.txt.
		if !errors.Is(err, fs.ErrNotExist) {
			return "", ErrEscapesRoot
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
