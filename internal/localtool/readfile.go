package localtool

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	return nil
}

func (rf *ReadFile) Plan(params map[string]any) (Plan, error) {
	p, _ := params["path"].(string)
	real, err := Confine(rf.Root, p)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Tool:      rf.Name(),
		Tier:      rf.Tier(),
		Display:   fmt.Sprintf("read_file: %s", real),
		MatchPath: real,
		path:      real,
	}, nil
}

func (rf *ReadFile) Execute(plan Plan) (any, error) {
	// TOCTOU note: `plan.path` is the symlink-resolved path produced by Plan
	// (confinement happened then). Between Plan and Execute lies the human-
	// approval window, and re-opening by name re-resolves symlinks at open
	// time — a racing local process could swap any component to point outside
	// root after approval but before the open. Open through os.Root instead:
	// every component (prefix included) is kernel-resolved BENEATH the root
	// directory fd, so a swapped-in symlink escaping root fails at open rather
	// than being followed. plan.path is under the *resolved* root (Confine
	// resolves the root itself), so relativize against that.
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
