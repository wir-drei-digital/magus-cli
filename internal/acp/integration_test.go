// internal/acp/integration_test.go
package acp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/coder/acp-go-sdk"
	"github.com/coder/websocket"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// stubEditor is a minimal sdk.Client: it auto-allows permission, serves a file,
// and records agent message text.
type stubEditor struct {
	mu       sync.Mutex
	text     strings.Builder
	readPath string
}

func (s *stubEditor) RequestPermission(_ context.Context, _ sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{
		Selected: &sdk.RequestPermissionOutcomeSelected{OptionId: "allow"},
	}}, nil
}
func (s *stubEditor) SessionUpdate(_ context.Context, n sdk.SessionNotification) error {
	// Accumulate the streamed update's JSON so the test can assert the agent's
	// text reached the editor end-to-end.
	raw, err := json.Marshal(n.Update)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.text.Write(raw)
	s.mu.Unlock()
	return nil
}
func (s *stubEditor) ReadTextFile(_ context.Context, p sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error) {
	s.mu.Lock()
	s.readPath = p.Path
	s.mu.Unlock()
	return sdk.ReadTextFileResponse{Content: "defmodule App"}, nil
}
func (s *stubEditor) WriteTextFile(context.Context, sdk.WriteTextFileRequest) (sdk.WriteTextFileResponse, error) {
	return sdk.WriteTextFileResponse{}, nil
}
func (s *stubEditor) CreateTerminal(context.Context, sdk.CreateTerminalRequest) (sdk.CreateTerminalResponse, error) {
	return sdk.CreateTerminalResponse{}, nil
}
func (s *stubEditor) KillTerminal(context.Context, sdk.KillTerminalRequest) (sdk.KillTerminalResponse, error) {
	return sdk.KillTerminalResponse{}, nil
}
func (s *stubEditor) ReleaseTerminal(context.Context, sdk.ReleaseTerminalRequest) (sdk.ReleaseTerminalResponse, error) {
	return sdk.ReleaseTerminalResponse{}, nil
}
func (s *stubEditor) TerminalOutput(context.Context, sdk.TerminalOutputRequest) (sdk.TerminalOutputResponse, error) {
	return sdk.TerminalOutputResponse{}, nil
}
func (s *stubEditor) WaitForTerminalExit(context.Context, sdk.WaitForTerminalExitRequest) (sdk.WaitForTerminalExitResponse, error) {
	return sdk.WaitForTerminalExitResponse{}, nil
}

// cloudServer scripts the cloud side: server_hello, a text delta, an mcp_call,
// then turn.done after the mcp_result arrives.
func cloudServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		write := func(typ string, v any) {
			b, _ := wrapTest(typ, v)
			_ = c.Write(ctx, websocket.MessageText, b)
		}

		_, _, _ = c.Read(ctx) // hello
		write("server_hello", chat.ServerHello{ConversationID: "conv1", AcceptedTools: []string{"read_file"}})

		_, _, _ = c.Read(ctx) // chat
		write("chat_stream", chat.ChatStream{Event: "text.delta", Data: map[string]any{"delta": "Reading... "}})
		write("mcp_call", chat.McpCall{CallID: "call1", ToolName: "read_file", Params: map[string]any{"path": "mix.exs"}})

		_, data, err := c.Read(ctx) // mcp_result
		if err != nil {
			return
		}
		var res chat.McpResult
		_ = jsonUnmarshal(data, &res)
		if res.Status == "ok" {
			write("chat_stream", chat.ChatStream{Event: "text.delta", Data: map[string]any{"delta": "the app module is App"}})
		}
		write("chat_stream", chat.ChatStream{Event: "turn.done"})
	}))
}

func TestACPEndToEnd(t *testing.T) {
	srv := cloudServer(t)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"

	// Agent under test, with the real chat.Dial pointed at our stub cloud.
	agent := New("tok", "https://unused", "magus-cli/test")
	agent.Dial = func(ctx context.Context, _, _, _ string) (CloudConn, error) {
		return chat.Dial(ctx, wsURL, "tok", "magus-cli/test")
	}

	// Connect a real ACP client (stub editor) to the agent over two pipes.
	a2cR, a2cW := io.Pipe()
	c2aR, c2aW := io.Pipe()
	agentConn := sdk.NewAgentSideConnection(agent, a2cW, c2aR)
	agent.SetEditor(agentConn)

	editor := &stubEditor{}
	clientConn := sdk.NewClientSideConnection(editor, c2aW, a2cR)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	if _, err := clientConn.Initialize(ctx, sdk.InitializeRequest{ProtocolVersion: sdk.ProtocolVersion(sdk.ProtocolVersionNumber)}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	ns, err := clientConn.NewSession(ctx, sdk.NewSessionRequest{Cwd: "/tmp/work", McpServers: []sdk.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	pr, err := clientConn.Prompt(ctx, sdk.PromptRequest{
		SessionId: ns.SessionId,
		Prompt:    []sdk.ContentBlock{sdk.TextBlock("what's the app module in mix.exs?")},
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if pr.StopReason != sdk.StopReasonEndTurn {
		t.Errorf("stop reason = %q, want end_turn", pr.StopReason)
	}
	editor.mu.Lock()
	readPath := editor.readPath
	streamed := editor.text.String()
	editor.mu.Unlock()
	if readPath != "mix.exs" {
		t.Errorf("editor was asked to read %q, want mix.exs", readPath)
	}
	if !strings.Contains(streamed, "the app module is App") {
		t.Errorf("streamed text forwarded to editor = %q, want it to contain %q", streamed, "the app module is App")
	}
}

// test-only helpers (exported wire helpers are unexported in the package).
func wrapTest(typ string, v any) ([]byte, error) { return wrapForTest(typ, v) }

// wrapForTest re-implements chat.wrap locally (it is unexported in the chat
// package). It wraps an outbound frame value with {type, v} and marshals it.
func wrapForTest(typ string, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	env["type"] = typ
	env["v"] = 1
	return json.Marshal(env)
}

// jsonUnmarshal re-implements chat.decodePayload locally (also unexported).
func jsonUnmarshal(b []byte, out any) error { return json.Unmarshal(b, out) }
