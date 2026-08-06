package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestRunChatReadFileFlow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "mix.exs"), []byte("app: :magus"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Hermetic policy + audit: runChat loads the persisted permissions from the
	// default config dir, so point that at a temp dir instead of the developer's
	// real ~/.config/magus (whose tiers would otherwise change the outcome).
	confDir := t.TempDir()
	t.Setenv("MAGUS_CONFIG_DIR", confDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		_, _, _ = c.Read(ctx) // hello
		sh, _ := wrapTest("server_hello", map[string]any{"conversation_id": "c1", "accepted_tools": []string{"read_file"}})
		_ = c.Write(ctx, websocket.MessageText, sh)

		_, _, _ = c.Read(ctx) // chat
		mc, _ := wrapTest("mcp_call", map[string]any{"call_id": "k1", "tool_name": "read_file", "params": map[string]any{"path": "mix.exs"}})
		_ = c.Write(ctx, websocket.MessageText, mc)

		_, data, _ := c.Read(ctx) // mcp_result
		if !strings.Contains(string(data), "app: :magus") {
			t.Errorf("server did not receive file content; got %s", data)
		}
		done, _ := wrapTest("chat_stream", map[string]any{"event": "turn.done", "data": map[string]any{}})
		_ = c.Write(ctx, websocket.MessageText, done)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"

	// Scripted stdin: one message, then "a" to approve the read.
	in := strings.NewReader("what's in mix.exs?\na\n")
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := runChat(ctx, chatOptions{
		WSURL:     wsURL,
		Token:     "tok",
		UserAgent: "magus-cli/test",
		Root:      root,
		In:        in,
		Out:       &out,
		ConfigDir: confDir,
	})
	if err != nil {
		t.Fatalf("runChat: %v", err)
	}

	// The approval prompt renders the client-canonical action, never anything
	// the server said.
	if !strings.Contains(out.String(), "read_file: ") {
		t.Errorf("approval prompt missing from output; got %q", out.String())
	}

	// The decision is on the local audit trail.
	audit, err := os.ReadFile(filepath.Join(confDir, "chat-audit.jsonl"))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if !strings.Contains(string(audit), `"decision":"allow"`) {
		t.Errorf("audit log missing allow entry; got %s", audit)
	}
}

// A cancelled context (Ctrl-C) must end the session. The client's internal
// context is detached from the caller's, so without an explicit hookup runChat
// would sit in the event loop forever — and further SIGINTs would not reach the
// default kill behaviour, leaving the process unkillable from the keyboard.
func TestRunChatReturnsOnContextCancel(t *testing.T) {
	root := t.TempDir()
	confDir := t.TempDir()
	t.Setenv("MAGUS_CONFIG_DIR", confDir)

	// Closed once the server has the user's message, i.e. once the connection is
	// definitely up — cancelling on a timer instead could race the dial.
	chatted := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		_, _, _ = c.Read(ctx) // hello
		sh, _ := wrapTest("server_hello", map[string]any{"conversation_id": "c1", "accepted_tools": []string{"read_file"}})
		_ = c.Write(ctx, websocket.MessageText, sh)

		_, _, _ = c.Read(ctx) // chat
		close(chatted)
		// Then go silent: the turn never completes. This read only returns once
		// the client tears the connection down.
		_, _, _ = c.Read(ctx)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-chatted
		cancel()
	}()

	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runChat(ctx, chatOptions{
			WSURL:     wsURL,
			Token:     "tok",
			UserAgent: "magus-cli/test",
			Root:      root,
			In:        strings.NewReader("hello there\n"),
			Out:       &out,
			ConfigDir: confDir,
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runChat after cancel: %v", err)
		}
		// The user pressed Ctrl-C; they are not told about the connection they
		// just closed — and crucially they are not told about it only sometimes.
		if strings.Contains(out.String(), "[connection closed") {
			t.Errorf("cancel path reported a connection close; got %q", out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runChat did not return after the context was cancelled")
	}
}

// A connection that drops mid-turn must say so. The failure races Send (its
// buffer has room while the context is already cancelled), so both outcomes
// have to converge on the same visible, zero-exit behaviour.
func TestRunChatReportsMidTurnDisconnect(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "mix.exs"), []byte("app: :magus"), 0o600); err != nil {
		t.Fatal(err)
	}
	confDir := t.TempDir()
	t.Setenv("MAGUS_CONFIG_DIR", confDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()

		_, _, _ = c.Read(ctx) // hello
		sh, _ := wrapTest("server_hello", map[string]any{"conversation_id": "c1", "accepted_tools": []string{"read_file"}})
		_ = c.Write(ctx, websocket.MessageText, sh)

		_, _, _ = c.Read(ctx) // chat
		mc, _ := wrapTest("mcp_call", map[string]any{"call_id": "k1", "tool_name": "read_file", "params": map[string]any{"path": "mix.exs"}})
		_ = c.Write(ctx, websocket.MessageText, mc)

		// Drop while the user is still at the approval prompt.
		_ = c.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"

	in := strings.NewReader("what's in mix.exs?\na\n")
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := runChat(ctx, chatOptions{
		WSURL:     wsURL,
		Token:     "tok",
		UserAgent: "magus-cli/test",
		Root:      root,
		In:        in,
		Out:       &out,
		ConfigDir: confDir,
	})
	if err != nil {
		t.Fatalf("runChat: %v", err)
	}
	if !strings.Contains(out.String(), "[connection closed]") {
		t.Errorf("disconnect not reported to the user; got %q", out.String())
	}
}

// A broken audit log is a security event: the reads keep executing and keep
// returning content, so the trail stopping has to reach the only person who can
// fix it. Once, though — a warning per tool call would bury the session output
// and train the user to ignore it.
func TestRunChatWarnsOnceWhenAuditingFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "mix.exs"), []byte("app: :magus"), 0o600); err != nil {
		t.Fatal(err)
	}
	confDir := t.TempDir()
	t.Setenv("MAGUS_CONFIG_DIR", confDir)

	// The audit log's directory is a regular FILE, so MkdirAll fails on every
	// Record — the read-only config dir / full disk case, deterministically.
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	results := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		_, _, _ = c.Read(ctx) // hello
		sh, _ := wrapTest("server_hello", map[string]any{"conversation_id": "c1", "accepted_tools": []string{"read_file"}})
		_ = c.Write(ctx, websocket.MessageText, sh)

		_, _, _ = c.Read(ctx) // chat
		// Two calls in one turn: the second is what proves the warning is not
		// repeated.
		for _, id := range []string{"k1", "k2"} {
			mc, _ := wrapTest("mcp_call", map[string]any{"call_id": id, "tool_name": "read_file", "params": map[string]any{"path": "mix.exs"}})
			_ = c.Write(ctx, websocket.MessageText, mc)
			_, data, _ := c.Read(ctx)
			results <- string(data)
		}
		done, _ := wrapTest("chat_stream", map[string]any{"event": "turn.done", "data": map[string]any{}})
		_ = c.Write(ctx, websocket.MessageText, done)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"
	in := strings.NewReader("what's in mix.exs?\na\na\n")
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := runChat(ctx, chatOptions{
		WSURL:     wsURL,
		Token:     "tok",
		UserAgent: "magus-cli/test",
		Root:      root,
		In:        in,
		Out:       &out,
		ConfigDir: blocked,
	}); err != nil {
		t.Fatalf("runChat: %v", err)
	}

	if got := strings.Count(out.String(), "audit log write failed"); got != 1 {
		t.Errorf("audit warning appeared %d times, want exactly 1; output:\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "no longer being recorded locally") {
		t.Errorf("warning does not say what stopped working; got %q", out.String())
	}
	// Non-fatal: both approved reads still ran and still returned their content.
	close(results)
	n := 0
	for res := range results {
		n++
		if !strings.Contains(res, "app: :magus") {
			t.Errorf("read %d did not return content: %s", n, res)
		}
	}
	if n != 2 {
		t.Errorf("expected 2 tool results, got %d", n)
	}
}

// Server-streamed text is attacker-controlled and a terminal is an interpreter,
// not a text sink: an unescaped "\x1b[2J\x1b[H" clears the screen and lets the
// server paint a byte-identical fake approval prompt. Everything the server says
// therefore goes through sanitizeStream on its way to Out.
func TestRunChatSanitizesServerText(t *testing.T) {
	root := t.TempDir()
	confDir := t.TempDir()
	t.Setenv("MAGUS_CONFIG_DIR", confDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		_, _, _ = c.Read(ctx) // hello
		sh, _ := wrapTest("server_hello", map[string]any{"conversation_id": "c1", "accepted_tools": []string{"read_file"}})
		_ = c.Write(ctx, websocket.MessageText, sh)

		_, _, _ = c.Read(ctx) // chat
		// Split across deltas, as a real stream would be: the screen-clear, the
		// forged prompt, and ordinary prose that must survive intact.
		for _, delta := range []string{
			"\x1b[2J\x1b[H",
			"\nThe cloud agent wants to run:\r",
			"日本語 \U0001f389 ok",
		} {
			d, _ := wrapTest("chat_stream", map[string]any{"event": "text.delta", "data": map[string]any{"delta": delta}})
			_ = c.Write(ctx, websocket.MessageText, d)
		}
		done, _ := wrapTest("chat_stream", map[string]any{"event": "turn.done", "data": map[string]any{}})
		_ = c.Write(ctx, websocket.MessageText, done)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := runChat(ctx, chatOptions{
		WSURL:     wsURL,
		Token:     "tok",
		UserAgent: "magus-cli/test",
		Root:      root,
		In:        strings.NewReader("hi\n"),
		Out:       &out,
		ConfigDir: confDir,
	}); err != nil {
		t.Fatalf("runChat: %v", err)
	}

	got := out.String()
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, '\r') {
		t.Errorf("server text reached the terminal with live control bytes: %q", got)
	}
	if !strings.Contains(got, `\x1b[2J`) {
		t.Errorf("escape sequence not rendered as inert text; got %q", got)
	}
	if !strings.Contains(got, "日本語 \U0001f389 ok") {
		t.Errorf("ordinary unicode was mangled; got %q", got)
	}
}

// The stream "error" event and the WS close reason are server-controlled strings
// too, and both are printed straight to the terminal.
func TestRunChatSanitizesStreamError(t *testing.T) {
	root := t.TempDir()
	confDir := t.TempDir()
	t.Setenv("MAGUS_CONFIG_DIR", confDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		_, _, _ = c.Read(ctx) // hello
		sh, _ := wrapTest("server_hello", map[string]any{"conversation_id": "c1", "accepted_tools": []string{"read_file"}})
		_ = c.Write(ctx, websocket.MessageText, sh)

		_, _, _ = c.Read(ctx) // chat
		e, _ := wrapTest("chat_stream", map[string]any{
			"event": "error",
			"data":  map[string]any{"message": "boom\x1b[2Jrepainted"},
		})
		_ = c.Write(ctx, websocket.MessageText, e)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := runChat(ctx, chatOptions{
		WSURL:     wsURL,
		Token:     "tok",
		UserAgent: "magus-cli/test",
		Root:      root,
		In:        strings.NewReader("hi\n"),
		Out:       &out,
		ConfigDir: confDir,
	}); err != nil {
		t.Fatalf("runChat: %v", err)
	}

	got := out.String()
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("stream error reached the terminal unescaped: %q", got)
	}
	if !strings.Contains(got, `boom\x1b[2Jrepainted`) {
		t.Errorf("stream error not rendered inert; got %q", got)
	}
}

// closedNotice prints the transport's close reason, which for a WS close frame
// is a server-supplied string.
func TestClosedNoticeSanitizesCloseReason(t *testing.T) {
	var out bytes.Buffer
	closedNotice(context.Background(), &out, errors.New("status = StatusInternalError and reason = \x1b[2Jgone"))

	got := out.String()
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("close reason reached the terminal unescaped: %q", got)
	}
	if !strings.Contains(got, `\x1b[2Jgone`) {
		t.Errorf("close reason not rendered inert; got %q", got)
	}
}

// sendErr is what every cli.Send call site in runChat funnels its failures
// through, so its three outcomes are pinned directly.
func TestSendErrClassifiesFailures(t *testing.T) {
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("dropped connection is reported, not returned", func(t *testing.T) {
		var out bytes.Buffer
		if err := sendErr(live, &out, context.Canceled); err != nil {
			t.Errorf("want nil, got %v", err)
		}
		if !strings.Contains(out.String(), "[connection closed]") {
			t.Errorf("drop not reported; got %q", out.String())
		}
	})

	t.Run("user cancellation is silent", func(t *testing.T) {
		var out bytes.Buffer
		if err := sendErr(cancelled, &out, context.Canceled); err != nil {
			t.Errorf("want nil, got %v", err)
		}
		if out.String() != "" {
			t.Errorf("want silence on Ctrl-C; got %q", out.String())
		}
	})

	t.Run("any other failure still surfaces", func(t *testing.T) {
		var out bytes.Buffer
		want := errors.New("marshal boom")
		if err := sendErr(live, &out, want); !errors.Is(err, want) {
			t.Errorf("want %v, got %v", want, err)
		}
		if out.String() != "" {
			t.Errorf("want no close notice for an unrelated failure; got %q", out.String())
		}
	})
}

// wrapTest mirrors chat.Encode for arbitrary maps in this test package.
func wrapTest(typ string, m map[string]any) ([]byte, error) {
	m["type"] = typ
	m["v"] = 1
	return json.Marshal(m)
}
