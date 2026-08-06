package localtool

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ReadFile reads a file on the local machine, confined to Root and capped at MaxBytes.
type ReadFile struct {
	Root     string
	MaxBytes int
}

func (rf *ReadFile) Name() string { return "read_file" }
func (rf *ReadFile) Tier() string { return "read" }

func (rf *ReadFile) Validate(params map[string]any) error {
	p, ok := params["path"].(string)
	if !ok || p == "" {
		return fmt.Errorf("read_file requires a non-empty string %q param", "path")
	}
	// The path is server-supplied and becomes the text a human reads in the
	// approval prompt, so control bytes make that prompt an attacker-controlled
	// canvas: "\n" forges an additional prompt line, "\r" rewinds the current
	// one, ESC repaints the screen. The human then approves one thing while
	// reading another. Nothing has to exist on disk for this — a nonexistent
	// leaf clears confinement — so the check has to happen here, before any
	// rendering. Reject rather than sanitize: no legitimate path needs them.
	//
	// Byte-wise on purpose: every byte of a multi-byte UTF-8 rune is >= 0x80,
	// so legitimate non-ASCII filenames pass through untouched.
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < 0x20 || c == 0x7f {
			return fmt.Errorf("read_file: path contains control byte %#02x at offset %d", c, i)
		}
	}
	return nil
}

// displayPath renders a path for the human approval prompt. Clean paths render
// bare so the prompt stays readable; anything containing a non-printable rune
// (or invalid UTF-8) is quoted and escaped so it cannot break the prompt's line
// discipline. Validate already rejects control bytes in the server-supplied
// path — this covers what Validate cannot: the operator-supplied Root, which is
// joined into every resolved path and is not guaranteed printable.
func displayPath(p string) string {
	if !utf8.ValidString(p) || strings.ContainsFunc(p, func(r rune) bool { return !strconv.IsPrint(r) }) {
		return strconv.Quote(p)
	}
	return p
}

func (rf *ReadFile) Plan(params map[string]any) (Plan, error) {
	// Plan re-validates instead of trusting the caller to have done it: this is
	// the point where server bytes turn into both the approval Display and the
	// MatchPath that allowlist rules are persisted against, and Plan is
	// reachable without Validate.
	if err := rf.Validate(params); err != nil {
		return Plan{}, err
	}
	p, _ := params["path"].(string)
	real, err := Confine(rf.Root, p)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Tool:      rf.Name(),
		Tier:      rf.Tier(),
		Display:   fmt.Sprintf("read_file: %s", displayPath(real)),
		MatchPath: real,
		path:      real,
	}, nil
}

func (rf *ReadFile) Execute(plan Plan) (any, error) {
	// TOCTOU note: `plan.path` is the symlink-resolved path produced by Plan
	// (confinement happened then). Between Plan and Execute lies the human-
	// approval window, and re-opening by name re-resolves symlinks at open
	// time — a racing local process could swap a component after approval but
	// before the open. Two mitigations, and neither is complete:
	//
	//  1. os.Root: every component (prefix included) is kernel-resolved BENEATH
	//     the root directory fd, so a swapped-in symlink pointing OUTSIDE root
	//     fails at open instead of being followed. plan.path is under the
	//     *resolved* root (Confine resolves the root itself), so relativize
	//     against that.
	//  2. Lstat + IsRegular: os.Root constrains WHERE the open lands but says
	//     nothing about WHAT it lands on. Lstat does not follow the final
	//     component, so requiring a regular file rejects a leaf swapped for a
	//     symlink (including one pointing at another IN-root file, which os.Root
	//     happily permits) and rejects FIFOs/devices/sockets — a FIFO inside
	//     root would otherwise block the O_RDONLY open forever with no
	//     cancellation path. Legitimate symlinks still work: Confine resolves
	//     them at Plan time, so plan.path already names the real file.
	//
	// Residual races, NOT closed, accepted under the single-user dev threat
	// model (the attacker already executes as the user on this machine):
	//   - HARDLINK: the leaf replaced by a hardlink to a file outside root. No
	//     symlink is involved and the result is a regular file beneath root, so
	//     neither os.Root nor IsRegular can tell it apart from the approved file.
	//   - PREFIX SWAP: a parent component swapped for a symlink to another
	//     in-root directory. It resolves beneath root, so os.Root permits it,
	//     and the read may land on a different in-root file than was approved.
	//   - The Lstat -> Open gap is itself a window: the two calls resolve the
	//     name independently, so a swap landing between them goes undetected.
	//   - MOUNTS: per the os.Root docs, traversal does not account for mount
	//     points crossed beneath the root (bind mounts, /proc-style synthetic
	//     filesystems), so a mount planted inside root can still escape it.
	resolvedRoot, err := filepath.Abs(rf.Root)
	if err != nil {
		return nil, err
	}
	if real, rerr := filepath.EvalSymlinks(resolvedRoot); rerr == nil {
		resolvedRoot = real
	}
	rel, err := filepath.Rel(resolvedRoot, plan.path)
	if err != nil {
		return nil, ErrEscapesRoot
	}
	r, err := os.OpenRoot(resolvedRoot)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	fi, err := r.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("read_file: %s is not a regular file (%s)", displayPath(plan.path), fi.Mode().Type())
	}

	f, err := r.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	limit := rf.MaxBytes
	if limit <= 0 {
		limit = 256 * 1024
	}
	buf, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}

	truncated := false
	if len(buf) > limit {
		buf = buf[:limit]
		truncated = true
	}
	return map[string]any{"content": string(buf), "truncated": truncated}, nil
}
