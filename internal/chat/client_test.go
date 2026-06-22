// internal/chat/client_test.go
package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestClientRoundTrip(t *testing.T) {
	gotResult := make(chan McpResult, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		_, _, _ = c.Read(ctx) // hello
		sh, _ := wrap("server_hello", ServerHello{ConversationID: "conv1", AcceptedTools: []string{"read_file"}})
		_ = c.Write(ctx, websocket.MessageText, sh)
		mc, _ := wrap("mcp_call", McpCall{CallID: "call1", ToolName: "read_file", Params: map[string]any{"path": "a.txt"}})
		_ = c.Write(ctx, websocket.MessageText, mc)

		_, data, err := c.Read(ctx) // mcp_result
		if err != nil {
			return
		}
		var res McpResult
		_ = decodePayload(data, &res)
		gotResult <- res
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := Dial(ctx, wsURL, "tok123", "magus-cli/test")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	if err := cli.Send(Hello{SessionID: "s1", Capabilities: Capabilities{LocalTools: []string{"read_file"}}, Conversation: map[string]any{"new": true}}); err != nil {
		t.Fatal(err)
	}

	// Drive the exchange until we've replied to the mcp_call. Breaking on
	// server_hello alone would exit before the mcp_call event is handled.
	sentResult := false
	for ev := range cli.Events() {
		switch ev.Kind {
		case KindServerHello:
			if ev.ServerHello.ConversationID != "conv1" {
				t.Errorf("bad conversation id: %q", ev.ServerHello.ConversationID)
			}
		case KindMcpCall:
			_ = cli.Send(McpResult{CallID: ev.McpCall.CallID, Status: "ok", Result: map[string]any{"content": "hi"}})
			sentResult = true
		}
		if sentResult {
			break
		}
	}

	select {
	case res := <-gotResult:
		if res.CallID != "call1" || res.Status != "ok" {
			t.Errorf("server got bad mcp_result: %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never received mcp_result")
	}
}
