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

func TestFrameErr(t *testing.T) {
	cases := []struct {
		code, msg, want string
	}{
		{"forbidden", "not yours", "forbidden: not yours"},
		{"", "boom", "boom"},
		{"bad_frame", "", "bad_frame"},
		{"", "", "server error"},
	}
	for _, c := range cases {
		got := frameErr(FrameError{Code: c.code, Message: c.msg}).Error()
		if got != c.want {
			t.Errorf("frameErr(%q,%q) = %q, want %q", c.code, c.msg, got, c.want)
		}
	}
}

func TestDialSurfacesScopeAndAuthFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   []string
	}{
		{"read-scoped token", http.StatusForbidden, []string{"insufficient_scope", "write", "magus login"}},
		{"invalid token", http.StatusUnauthorized, []string{"expired", "magus login"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, http.StatusText(tc.status), tc.status)
			}))
			defer srv.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/cli/chat", "tok", "magus-cli/test")
			if err == nil {
				t.Fatal("expected a dial error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// The server closes the connection on frames over 1MB, so Send — the single
// choke point both front-ends share — must fit every mcp_result to the budget.
func TestClientSendFitsOversizedMcpResult(t *testing.T) {
	gotFrame := make(chan []byte, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		c.SetReadLimit(8 << 20)
		_, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		gotFrame <- data
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/cli/chat", "tok", "magus-cli/test")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	content := strings.Repeat("\x1b", 300*1024) // 1.8MB once escaped
	if err := cli.Send(McpResult{CallID: "c1", Status: "ok", Result: map[string]any{"content": content}}); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case data := <-gotFrame:
		if len(data) > maxResultFrameBytes {
			t.Fatalf("wire frame is %d bytes, budget is %d", len(data), maxResultFrameBytes)
		}
		var res McpResult
		if err := decodePayload(data, &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if res.Result["truncated"] != true {
			t.Errorf("truncated = %v, want true", res.Result["truncated"])
		}
		if out, _ := res.Result["content"].(string); !strings.HasPrefix(content, out) || out == "" {
			t.Errorf("content is not a non-empty prefix of the original (%d bytes)", len(out))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never received the frame")
	}
}

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
