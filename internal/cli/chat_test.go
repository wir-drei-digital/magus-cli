package cli

import (
	"bytes"
	"context"
	"encoding/json"
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

// wrapTest mirrors chat.Encode for arbitrary maps in this test package.
func wrapTest(typ string, m map[string]any) ([]byte, error) {
	m["type"] = typ
	m["v"] = 1
	return json.Marshal(m)
}
