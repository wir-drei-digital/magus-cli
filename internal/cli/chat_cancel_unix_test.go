//go:build unix

package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A Ctrl-C landing in the window between Dial and the hello send must still end
// the session cleanly — not escape as a raw "context canceled" and a status-1
// exit. That window is real (config.Load's disk I/O sits inside it) but narrow,
// so this test pins runChat open in the middle of it: MAGUS_CONFIG_DIR's
// config.toml is a FIFO, and a blocking open of a FIFO with no writer parks
// config.Load exactly where we want it, after the dial and before the hello.
//
// Whether Send then reports the failure at all is still Go's coin flip (its
// select sees both a ready buffer and a done context), so the scenario runs
// several times: the assertion — runChat returns nil — must hold on every
// interleaving, and with the send failure surfacing about half the time the
// pre-fix behaviour fails this test with probability ~1-2^-8.
//
// Build-tagged: syscall.Mkfifo is POSIX-only and magus also ships a Windows
// build, where this file must not be compiled.
func TestRunChatHelloSendUnderCancel(t *testing.T) {
	for i := 0; i < 8; i++ {
		runHelloCancelOnce(t)
	}
}

func runHelloCancelOnce(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	confDir := t.TempDir()
	t.Setenv("MAGUS_CONFIG_DIR", confDir)

	fifo := filepath.Join(confDir, "config.toml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		// Never answers: the hello either never arrives or is never replied to.
		_, _, _ = c.Read(r.Context())
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runChat(ctx, chatOptions{
			WSURL:     wsURL,
			Token:     "tok",
			UserAgent: "magus-cli/test",
			Root:      root,
			In:        strings.NewReader(""),
			Out:       &out,
			ConfigDir: confDir,
		})
	}()

	// A non-blocking O_WRONLY open of a FIFO fails with ENXIO until a reader is
	// there, so its first success proves runChat is parked inside config.Load —
	// past Dial, short of the hello.
	var fd int
	deadline := time.Now().Add(10 * time.Second)
	for {
		f, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			fd = f
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runChat never reached config.Load: %v", err)
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	// Let the cancel actually tear the client down, so the hello send below is
	// the one racing a closed connection rather than a still-live one.
	time.Sleep(20 * time.Millisecond)
	// EOF on the FIFO releases config.Load; runChat walks straight into the
	// hello send.
	_ = syscall.Close(fd)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hello send under cancel: want nil, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runChat did not return after the context was cancelled")
	}
}
