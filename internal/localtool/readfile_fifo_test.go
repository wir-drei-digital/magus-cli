//go:build unix

package localtool

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO inside the root passes confinement (it is a real file beneath root)
// and passes os.Root's traversal check, but a blocking O_RDONLY open on a FIFO
// with no writer never returns — and Execute has no cancellation path, so this
// is a permanent hang of the agent, reachable from a plain read_file call.
// Rejecting non-regular files before the open is what prevents it.
//
// Build-tagged: syscall.Mkfifo is POSIX-only and magus also ships a Windows
// build, where this file must not be compiled.
func TestReadFileExecuteDoesNotBlockOnFIFO(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	rf := &ReadFile{Root: root, MaxBytes: 1024}

	plan, err := rf.Plan(map[string]any{"path": "pipe"})
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		out any
		err error
	}
	// Run off the test goroutine so a regression reports as a failure instead
	// of wedging the whole package until the go test timeout fires.
	done := make(chan result, 1)
	go func() {
		out, err := rf.Execute(plan)
		done <- result{out, err}
	}()

	select {
	case r := <-done:
		if r.err == nil {
			t.Errorf("expected an error for a FIFO, got %#v", r.out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute blocked on a FIFO: no writer will ever arrive, so this never returns")
	}
}
