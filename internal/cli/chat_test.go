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

// wrapTest mirrors chat.Encode for arbitrary maps in this test package.
func wrapTest(typ string, m map[string]any) ([]byte, error) {
	m["type"] = typ
	m["v"] = 1
	return json.Marshal(m)
}
