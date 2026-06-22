# magus acp — CLI Implementation Plan (ACP Agent Adapter)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `magus acp` command: an ACP (Agent Client Protocol) agent that an editor (Zed, etc.) launches as a subprocess, bridging the editor to the magus cloud agent over the chat WebSocket, and delegating local `read_file` to the editor's `fs/read_text_file` behind `session/request_permission`.

**Architecture:** `magus acp` is a two-sided bridge. It speaks **ACP agent-side** to the editor over stdio (via `github.com/coder/acp-go-sdk`) and **chat WS client-side** to the cloud (via a new `internal/chat` transport). Core logic is written against two small ports — `EditorConn` (toward the editor; `*sdk.AgentSideConnection` satisfies it) and `CloudConn` (toward the cloud; `*chat.Client` satisfies it) — so the orchestration, stream mapping, and tool executor are all unit-testable with fakes. The single SDK-coupled file (`agent.go`) implements the `sdk.Agent` interface.

**Tech Stack:** Go 1.26, cobra, `github.com/coder/acp-go-sdk` v0.13.5, `github.com/coder/websocket`, stdlib (`io`, `context`, `crypto/rand`, `net/http/httptest`).

**Repo:** `/Users/daniel/Development/magus-cli`.

**Spec:** `docs/superpowers/specs/2026-06-22-magus-acp-adapter-design.md`.

## Global Constraints

- Module path: `github.com/wir-drei-digital/magus-cli`. Go 1.26.
- The SDK package is named `acp`; our package is also `internal/acp` (package `acp`). **Always import the SDK aliased as `sdk`:** `sdk "github.com/coder/acp-go-sdk"`. Never import it unaliased inside `internal/acp`.
- The `magus acp` command owns **stdout and stdin** for the JSON-RPC channel. **All diagnostics go to stderr.** Never `fmt.Print*` to stdout in the acp path.
- Advertised local tool set for v1 is exactly `["read_file"]`. The executor's known-tool gate rejects everything else.
- Reuse existing config: `config.Load()`, `config.ResolveToken(cfg, profile)`, `config.ResolveAPIURL(cfg, profile, DefaultAPIURL)`. The package-level `profile` var and `DefaultAPIURL` const live in `internal/cli`.
- Transport is **WSS only**; plaintext `ws://` is allowed only for `localhost`/`127.0.0.1`/`::1` (dev). TLS verification always on.
- Session model is **1:1:1** — one ACP session ↔ one chat WS connection ↔ one cloud Conversation.
- ACP protocol version constant is `sdk.ProtocolVersionNumber` (currently `1`). Stop reason for a completed turn is `sdk.StopReasonEndTurn`.

### Verified SDK surface (from `go doc`, v0.13.5)

```go
func sdk.NewAgentSideConnection(agent sdk.Agent, peerInput io.Writer, peerOutput io.Reader) *sdk.AgentSideConnection
func (c *sdk.AgentSideConnection) SessionUpdate(ctx, sdk.SessionNotification) error
func (c *sdk.AgentSideConnection) RequestPermission(ctx, sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error)
func (c *sdk.AgentSideConnection) ReadTextFile(ctx, sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error)
func (c *sdk.AgentSideConnection) Done() <-chan struct{}

// sdk.Agent (11 methods): Initialize, NewSession, Prompt, Cancel (real for us);
//   Authenticate, Logout, CloseSession, ListSessions, ResumeSession,
//   SetSessionConfigOption, SetSessionMode (stub → sdk.NewMethodNotFound).
// LoadSession is a SEPARATE optional interface (sdk.AgentLoader) — we do NOT implement it.

type sdk.InitializeRequest struct { ProtocolVersion sdk.ProtocolVersion; ClientCapabilities sdk.ClientCapabilities; ClientInfo *sdk.Implementation }
type sdk.InitializeResponse struct { ProtocolVersion sdk.ProtocolVersion; AgentCapabilities sdk.AgentCapabilities; AgentInfo *sdk.Implementation; AuthMethods []sdk.AuthMethod }
type sdk.NewSessionRequest struct { Cwd string; McpServers []sdk.McpServer; AdditionalDirectories []string }
type sdk.NewSessionResponse struct { SessionId sdk.SessionId; Modes *sdk.SessionModeState; ConfigOptions []sdk.SessionConfigOption }
type sdk.PromptRequest struct { SessionId sdk.SessionId; Prompt []sdk.ContentBlock; MessageId *string }
type sdk.PromptResponse struct { StopReason sdk.StopReason; Usage *sdk.Usage; UserMessageId *string }
type sdk.CancelNotification struct { SessionId sdk.SessionId }
type sdk.SessionNotification struct { SessionId sdk.SessionId; Update sdk.SessionUpdate }
type sdk.ContentBlock struct { Text *sdk.ContentBlockText; Image ...; ... }   // sdk.ContentBlockText{ Text string; Type string }
type sdk.RequestPermissionRequest struct { SessionId sdk.SessionId; ToolCall sdk.ToolCallUpdate; Options []sdk.PermissionOption }
type sdk.RequestPermissionResponse struct { Outcome sdk.RequestPermissionOutcome }
type sdk.RequestPermissionOutcome struct { Selected *sdk.RequestPermissionOutcomeSelected; Cancelled *sdk.RequestPermissionOutcomeCancelled }
type sdk.RequestPermissionOutcomeSelected struct { OptionId sdk.PermissionOptionId }
type sdk.PermissionOption struct { OptionId sdk.PermissionOptionId; Name string; Kind sdk.PermissionOptionKind }
type sdk.ToolCallUpdate struct { ToolCallId sdk.ToolCallId; Title *string; Kind *sdk.ToolKind; Status *sdk.ToolCallStatus; Content []sdk.ToolCallContent }
type sdk.ReadTextFileRequest struct { SessionId sdk.SessionId; Path string; Line *int; Limit *int }
type sdk.ReadTextFileResponse struct { Content string }
type sdk.Implementation struct { Name string; Title *string }

// Constructors / helpers:
func sdk.UpdateAgentMessageText(text string) sdk.SessionUpdate
func sdk.StartToolCall(id sdk.ToolCallId, title string, opts ...sdk.ToolCallStartOpt) sdk.SessionUpdate
func sdk.UpdateToolCall(id sdk.ToolCallId, opts ...sdk.ToolCallUpdateOpt) sdk.SessionUpdate
func sdk.WithStartStatus(s sdk.ToolCallStatus) sdk.ToolCallStartOpt
func sdk.WithUpdateStatus(s sdk.ToolCallStatus) sdk.ToolCallUpdateOpt
func sdk.TextBlock(text string) sdk.ContentBlock
func sdk.Ptr[T any](v T) *T
func sdk.NewMethodNotFound(method string) *sdk.RequestError
func sdk.NewAuthRequired(data any) *sdk.RequestError

// Consts: sdk.ToolKindRead, sdk.ToolCallStatusPending/InProgress/Completed/Failed,
//   sdk.PermissionOptionKindAllowOnce/RejectOnce, sdk.StopReasonEndTurn, sdk.ProtocolVersionNumber.
// sdk.SessionId, sdk.ToolCallId, sdk.PermissionOptionId are all `string` named types.
```

> **NOTE on `ClientCapabilities.Fs`:** the editor advertises fs support in `InitializeRequest`. The exact Go field name for the fs sub-struct was not pinned; confirm with `go doc github.com/coder/acp-go-sdk.ClientCapabilities` during Task 7 and read `.Fs.ReadTextFile` (JSON `fs.readTextFile`). In v1 we record it but do not hard-gate on it (Zed supports it).

## File structure

| File | Responsibility |
|---|---|
| `internal/chat/url.go` (create) | `WSURL(apiBaseURL) (string,error)`: derive `wss://host/cli/chat`; reject plaintext non-localhost. **Shared with chat Plan 2 — build once.** |
| `internal/chat/protocol.go` (create) | wire frame structs (`Hello`/`ServerHello`/`Chat`/`ChatStream`/`McpCall`/`McpResult`/`FrameError`); `Encode`/`DecodeType`/`wrap`/`decodePayload`; the `Executor` interface. **Shared with chat Plan 2.** |
| `internal/chat/client.go` (create) | `Dial`, `Client`, `Event`/`EventKind`, `Send`, `Events`, `Close`; single-writer + read/dispatch + ping heartbeat. **Shared with chat Plan 2.** |
| `internal/acp/ports.go` (create) | `EditorConn` + `CloudConn` interfaces; shared helpers (`denied`/`errResult`/`newID`). |
| `internal/acp/stream.go` (create) | pure `MapStream(chat.ChatStream) (sdk.SessionUpdate,bool)` + `TurnEnd(chat.ChatStream) (bool,string)`. |
| `internal/acp/executor.go` (create) | `Executor` implementing `chat.Executor`: gate → `RequestPermission` → `ReadTextFile` → `McpResult`. |
| `internal/acp/session.go` (create) | `Session`: pump loop (`Run`) + `Prompt` (send `chat`, await turn end). |
| `internal/acp/agent.go` (create) | implements `sdk.Agent`: `Initialize`/`NewSession`/`Prompt`/`Cancel` + 7 stubs; holds `EditorConn` + injectable cloud dialer. |
| `internal/cli/acp.go` (create) | `magus acp` command: load token/url, build agent, `NewAgentSideConnection`, back-ref editor, `<-conn.Done()`. |
| `internal/cli/root.go` (modify) | register `newACPCmd()` in the `agent` group. |
| `internal/acp/integration_test.go` (create) | in-process editor (`sdk.NewClientSideConnection`) ↔ agent over `io.Pipe`, + stub chat WS server: full prompt→read_file→answer. |
| `go.mod`/`go.sum` (modify) | add `github.com/coder/acp-go-sdk`, `github.com/coder/websocket`. |

---

## Task 1: Transport — WS URL derivation + deps

**Files:**
- Create: `internal/chat/url.go`
- Test: `internal/chat/url_test.go`
- Modify: `go.mod` (add `coder/websocket`; `coder/acp-go-sdk` already present)

**Interfaces:**
- Produces: `chat.WSURL(apiBaseURL string) (string, error)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/chat/url_test.go
package chat

import "testing"

func TestWSURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"https becomes wss", "https://magus.digital", "wss://magus.digital/cli/chat", false},
		{"trailing slash trimmed", "https://magus.digital/", "wss://magus.digital/cli/chat", false},
		{"http localhost allowed", "http://localhost:4000", "ws://localhost:4000/cli/chat", false},
		{"http 127.0.0.1 allowed", "http://127.0.0.1:4000", "ws://127.0.0.1:4000/cli/chat", false},
		{"http remote rejected", "http://magus.digital", "", true},
		{"unknown scheme rejected", "ftp://magus.digital", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := WSURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("WSURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("WSURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/`
Expected: FAIL — `undefined: WSURL` (package does not compile yet).

- [ ] **Step 3: Add the websocket dependency**

Run: `go get github.com/coder/websocket@latest`
Expected: `go.mod`/`go.sum` updated. (`github.com/coder/acp-go-sdk` is already present from spec eval.)

- [ ] **Step 4: Write the implementation**

```go
// internal/chat/url.go
package chat

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// WSURL derives the chat WebSocket URL from the API base URL (e.g.
// "https://magus.digital" -> "wss://magus.digital/cli/chat"). Plaintext ws://
// is allowed only for localhost; any other plaintext or unknown scheme errors.
func WSURL(apiBaseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(apiBaseURL, "/"))
	if err != nil {
		return "", fmt.Errorf("parse api url: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		if !isLocalhost(u.Host) {
			return "", fmt.Errorf("refusing plaintext ws:// to non-localhost host %q (use https)", u.Host)
		}
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported scheme %q in api url", u.Scheme)
	}
	u.Path = "/cli/chat"
	u.RawQuery = ""
	return u.String(), nil
}

func isLocalhost(host string) bool {
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/chat/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/chat/url.go internal/chat/url_test.go
git commit -m "feat(acp): chat transport — ws url derivation + deps"
```

---

## Task 2: Transport — wire protocol frames + Executor interface

**Files:**
- Create: `internal/chat/protocol.go`
- Test: `internal/chat/protocol_test.go`

**Interfaces:**
- Produces: frame types `Hello`, `ServerHello`, `Chat`, `ChatStream`, `McpCall`, `McpResult`, `FrameError`; `Encode(v any) ([]byte,error)`; `DecodeType([]byte) (string,error)`; `wrap(typ string, v any) ([]byte,error)`; `decodePayload([]byte, any) error`; and the `Executor` interface (`Handle(McpCall) McpResult`).

- [ ] **Step 1: Write the failing test**

```go
// internal/chat/protocol_test.go
package chat

import (
	"encoding/json"
	"testing"
)

func TestEncodeHello(t *testing.T) {
	data, err := Encode(Hello{
		SessionID:    "s1",
		Capabilities: Capabilities{LocalTools: []string{"read_file"}},
		Conversation: map[string]any{"new": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "hello" || got["v"].(float64) != 1 {
		t.Fatalf("bad envelope: %v", got)
	}
}

func TestDecodeTypeAndMcpCall(t *testing.T) {
	raw := []byte(`{"type":"mcp_call","v":1,"call_id":"c1","tool_name":"read_file","params":{"path":"a.txt"}}`)
	typ, err := DecodeType(raw)
	if err != nil || typ != "mcp_call" {
		t.Fatalf("DecodeType=%q err=%v", typ, err)
	}
	var c McpCall
	if err := decodePayload(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.CallID != "c1" || c.ToolName != "read_file" || c.Params["path"] != "a.txt" {
		t.Fatalf("bad decode: %+v", c)
	}
}

func TestExecutorInterfaceSatisfied(t *testing.T) {
	var _ Executor = stubExec{}
}

type stubExec struct{}

func (stubExec) Handle(call McpCall) McpResult {
	return McpResult{CallID: call.CallID, Status: "ok"}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run 'TestEncode|TestDecode|TestExecutor'`
Expected: FAIL — `undefined: Encode`, `undefined: Executor`, etc.

- [ ] **Step 3: Write the implementation**

```go
// internal/chat/protocol.go
package chat

import "encoding/json"

const protocolVersion = 1

type Capabilities struct {
	LocalTools []string `json:"local_tools"`
}

type Hello struct {
	SessionID     string         `json:"session_id"`
	ClientVersion string         `json:"client_version,omitempty"`
	Capabilities  Capabilities   `json:"capabilities"`
	Conversation  map[string]any `json:"conversation"`
}

type ServerHello struct {
	ConversationID string   `json:"conversation_id"`
	AcceptedTools  []string `json:"accepted_tools"`
	ServerVersion  string   `json:"server_version"`
}

type Chat struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type ChatStream struct {
	Event string         `json:"event"`
	Data  map[string]any `json:"data"`
}

type McpCall struct {
	CallID   string         `json:"call_id"`
	ToolName string         `json:"tool_name"`
	Params   map[string]any `json:"params"`
}

type McpResult struct {
	CallID string         `json:"call_id"`
	Status string         `json:"status"` // "ok" | "error" | "denied"
	Result map[string]any `json:"result,omitempty"`
	Error  *FrameError    `json:"error,omitempty"`
}

type FrameError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Executor turns an inbound mcp_call into an mcp_result. Each front-end provides
// its own backend (terminal pipeline, or editor delegation in the ACP bridge).
type Executor interface {
	Handle(call McpCall) McpResult
}

func frameType(v any) string {
	switch v.(type) {
	case Hello:
		return "hello"
	case Chat:
		return "chat"
	case McpResult:
		return "mcp_result"
	default:
		return ""
	}
}

// Encode wraps an outbound frame value with {type, v} and marshals it.
func Encode(v any) ([]byte, error) { return wrap(frameType(v), v) }

func wrap(typ string, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	env["type"] = typ
	env["v"] = protocolVersion
	return json.Marshal(env)
}

// DecodeType reads just the "type" discriminator from an inbound frame.
func DecodeType(data []byte) (string, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return "", err
	}
	return head.Type, nil
}

func decodePayload(data []byte, out any) error { return json.Unmarshal(data, out) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chat/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/protocol.go internal/chat/protocol_test.go
git commit -m "feat(acp): chat wire protocol frames + Executor seam"
```

---

## Task 3: Transport — WebSocket client

**Files:**
- Create: `internal/chat/client.go`
- Test: `internal/chat/client_test.go`

**Interfaces:**
- Consumes: frame types + `Encode`/`wrap`/`decodePayload` (Task 2).
- Produces: `Dial(ctx, wsURL, token, userAgent) (*Client, error)`; `(*Client).Send(frame any) error`; `(*Client).Events() <-chan Event`; `(*Client).Close()`; `Event{Kind EventKind; ServerHello; ChatStream; McpCall; Err error}`; `EventKind` consts `KindServerHello`, `KindChatStream`, `KindMcpCall`, `KindError`, `KindClosed`.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestClientRoundTrip`
Expected: FAIL — `undefined: Dial` / `Event` / kinds.

- [ ] **Step 3: Write the implementation**

```go
// internal/chat/client.go
package chat

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

type EventKind int

const (
	KindServerHello EventKind = iota
	KindChatStream
	KindMcpCall
	KindError
	KindClosed
)

type Event struct {
	Kind        EventKind
	ServerHello ServerHello
	ChatStream  ChatStream
	McpCall     McpCall
	Err         error
}

// Client is a chat WebSocket connection. Inbound frames surface on Events();
// outbound frames are serialized through a single writer goroutine.
type Client struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	send   chan []byte
	events chan Event
}

// Dial connects to wsURL with a Bearer token. TLS verification uses Go defaults.
func Dial(ctx context.Context, wsURL, token, userAgent string) (*Client, error) {
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	hdr.Set("User-Agent", userAgent)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, fmt.Errorf("ws dial: %w", err)
	}
	conn.SetReadLimit(8 << 20)

	cctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		conn:   conn,
		ctx:    cctx,
		cancel: cancel,
		send:   make(chan []byte, 16),
		events: make(chan Event, 16),
	}
	go c.writeLoop()
	go c.readLoop()
	return c, nil
}

func (c *Client) Events() <-chan Event { return c.events }

// Send marshals and enqueues an outbound frame.
func (c *Client) Send(frame any) error {
	data, err := Encode(frame)
	if err != nil {
		return err
	}
	select {
	case c.send <- data:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

func (c *Client) Close() {
	c.cancel()
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *Client) writeLoop() {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case data := <-c.send:
			if err := c.conn.Write(c.ctx, websocket.MessageText, data); err != nil {
				c.cancel()
				return
			}
		case <-ping.C:
			_ = c.conn.Ping(c.ctx)
		}
	}
}

func (c *Client) readLoop() {
	defer close(c.events)
	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			c.emit(Event{Kind: KindClosed, Err: err})
			c.cancel()
			return
		}
		typ, err := DecodeType(data)
		if err != nil {
			continue
		}
		switch typ {
		case "server_hello":
			var sh ServerHello
			if decodePayload(data, &sh) == nil {
				c.emit(Event{Kind: KindServerHello, ServerHello: sh})
			}
		case "chat_stream":
			var cs ChatStream
			if decodePayload(data, &cs) == nil {
				c.emit(Event{Kind: KindChatStream, ChatStream: cs})
			}
		case "mcp_call":
			var mc McpCall
			if decodePayload(data, &mc) == nil {
				c.emit(Event{Kind: KindMcpCall, McpCall: mc})
			}
		case "error":
			c.emit(Event{Kind: KindError, ChatStream: ChatStream{Event: "error"}})
		}
	}
}

func (c *Client) emit(ev Event) {
	select {
	case c.events <- ev:
	case <-c.ctx.Done():
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chat/`
Expected: PASS (all chat transport tests).

- [ ] **Step 5: Commit**

```bash
git add internal/chat/client.go internal/chat/client_test.go
git commit -m "feat(acp): chat websocket client (single-writer, heartbeat, typed events)"
```

---

## Task 4: ACP stream mapping (pure)

**Files:**
- Create: `internal/acp/stream.go`
- Test: `internal/acp/stream_test.go`

**Interfaces:**
- Consumes: `chat.ChatStream` (Task 2); SDK update constructors.
- Produces: `MapStream(cs chat.ChatStream) (sdk.SessionUpdate, bool)`; `TurnEnd(cs chat.ChatStream) (ended bool, errMsg string)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/acp/stream_test.go
package acp

import (
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

func TestMapStreamTextDelta(t *testing.T) {
	up, ok := MapStream(chat.ChatStream{Event: "text.delta", Data: map[string]any{"delta": "Hel"}})
	if !ok {
		t.Fatal("expected an update for text.delta")
	}
	// The SDK update is opaque here; assert it round-trips to JSON containing the text.
	if !updateContains(t, up, "Hel") {
		t.Errorf("update should carry the delta text")
	}
}

func TestMapStreamEmptyDeltaSkipped(t *testing.T) {
	if _, ok := MapStream(chat.ChatStream{Event: "text.delta", Data: map[string]any{"delta": ""}}); ok {
		t.Error("empty delta should produce no update")
	}
}

func TestMapStreamToolStartAndComplete(t *testing.T) {
	if _, ok := MapStream(chat.ChatStream{Event: "tool.start", Data: map[string]any{"event_id": "e1", "tool_name": "read_file"}}); !ok {
		t.Error("tool.start should map")
	}
	if _, ok := MapStream(chat.ChatStream{Event: "tool.complete", Data: map[string]any{"event_id": "e1", "status": "success"}}); !ok {
		t.Error("tool.complete should map")
	}
}

func TestMapStreamUnmapped(t *testing.T) {
	if _, ok := MapStream(chat.ChatStream{Event: "text.done", Data: map[string]any{}}); ok {
		t.Error("text.done should not produce an update")
	}
	if _, ok := MapStream(chat.ChatStream{Event: "thinking.chunk"}); ok {
		t.Error("unknown events should not produce an update")
	}
}

func TestTurnEnd(t *testing.T) {
	if ended, _ := TurnEnd(chat.ChatStream{Event: "turn.done"}); !ended {
		t.Error("turn.done should end the turn")
	}
	ended, msg := TurnEnd(chat.ChatStream{Event: "error", Data: map[string]any{"message": "boom"}})
	if !ended || msg != "boom" {
		t.Errorf("error should end the turn with message, got ended=%v msg=%q", ended, msg)
	}
	if ended, _ := TurnEnd(chat.ChatStream{Event: "text.delta"}); ended {
		t.Error("text.delta should not end the turn")
	}
}

// updateContains marshals a SessionUpdate and checks for substr (test helper).
func updateContains(t *testing.T, up any, substr string) bool {
	t.Helper()
	b, err := jsonMarshal(up)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	return contains(string(b), substr)
}
```

> Add tiny local helpers to the test file (keep them unexported, test-only):
> ```go
> import "encoding/json"
> import "strings"
> func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
> func contains(s, sub string) bool       { return strings.Contains(s, sub) }
> ```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/acp/`
Expected: FAIL — `undefined: MapStream` / `TurnEnd` (package does not compile).

- [ ] **Step 3: Write the implementation**

```go
// internal/acp/stream.go
package acp

import (
	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// MapStream converts a chat_stream event into an ACP session update.
// ok=false means "emit nothing" (e.g. text.done, unmapped events).
func MapStream(cs chat.ChatStream) (sdk.SessionUpdate, bool) {
	switch cs.Event {
	case "text.delta":
		if d := str(cs.Data["delta"]); d != "" {
			return sdk.UpdateAgentMessageText(d), true
		}
		return sdk.SessionUpdate{}, false
	case "tool.start":
		id := sdk.ToolCallId(str(cs.Data["event_id"]))
		return sdk.StartToolCall(id, toolTitle(cs.Data), sdk.WithStartStatus(sdk.ToolCallStatusInProgress)), true
	case "tool.complete":
		id := sdk.ToolCallId(str(cs.Data["event_id"]))
		return sdk.UpdateToolCall(id, sdk.WithUpdateStatus(toolStatus(cs.Data))), true
	default:
		return sdk.SessionUpdate{}, false
	}
}

// TurnEnd reports whether the event ends the prompt turn, with any error message.
func TurnEnd(cs chat.ChatStream) (bool, string) {
	switch cs.Event {
	case "turn.done":
		return true, ""
	case "error":
		return true, str(cs.Data["message"])
	default:
		return false, ""
	}
}

func toolTitle(data map[string]any) string {
	if name := str(data["tool_name"]); name != "" {
		return name
	}
	return "tool"
}

func toolStatus(data map[string]any) sdk.ToolCallStatus {
	switch str(data["status"]) {
	case "error", "failed":
		return sdk.ToolCallStatusFailed
	default:
		return sdk.ToolCallStatusCompleted
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/acp/ -run 'TestMapStream|TestTurnEnd'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/acp/stream.go internal/acp/stream_test.go
git commit -m "feat(acp): map chat_stream events to ACP session updates"
```

---

## Task 5: ACP ports + editor-backed executor

**Files:**
- Create: `internal/acp/ports.go`
- Create: `internal/acp/executor.go`
- Test: `internal/acp/executor_test.go`

**Interfaces:**
- Consumes: `chat.McpCall`/`chat.McpResult`/`chat.FrameError` (Task 2); SDK permission/read types.
- Produces:
  - `EditorConn` interface (`SessionUpdate`, `RequestPermission`, `ReadTextFile` — same signatures as `*sdk.AgentSideConnection`).
  - `CloudConn` interface (`Send(any) error`, `Events() <-chan chat.Event`, `Close()`).
  - `Executor{SessionID string; Editor EditorConn; Advertised map[string]bool; Ctx context.Context}` implementing `chat.Executor` via `Handle(chat.McpCall) chat.McpResult`.
  - helpers `denied`, `errResult`, `newID() string`.

- [ ] **Step 1: Write the failing test**

```go
// internal/acp/executor_test.go
package acp

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// fakeEditor records calls and returns scripted outcomes.
type fakeEditor struct {
	permOptionID string // option id to "select"; "" => cancelled
	permErr      error
	fileContent  string
	readErr      error

	updates  []sdk.SessionNotification
	readPath string
	asked    bool
}

func (f *fakeEditor) SessionUpdate(_ context.Context, n sdk.SessionNotification) error {
	f.updates = append(f.updates, n)
	return nil
}

func (f *fakeEditor) RequestPermission(_ context.Context, _ sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	f.asked = true
	if f.permErr != nil {
		return sdk.RequestPermissionResponse{}, f.permErr
	}
	if f.permOptionID == "" {
		return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{Cancelled: &sdk.RequestPermissionOutcomeCancelled{}}}, nil
	}
	return sdk.RequestPermissionResponse{Outcome: sdk.RequestPermissionOutcome{
		Selected: &sdk.RequestPermissionOutcomeSelected{OptionId: sdk.PermissionOptionId(f.permOptionID)},
	}}, nil
}

func (f *fakeEditor) ReadTextFile(_ context.Context, p sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error) {
	f.readPath = p.Path
	if f.readErr != nil {
		return sdk.ReadTextFileResponse{}, f.readErr
	}
	return sdk.ReadTextFileResponse{Content: f.fileContent}, nil
}

func newExec(ed EditorConn) *Executor {
	return &Executor{SessionID: "sess1", Editor: ed, Advertised: map[string]bool{"read_file": true}, Ctx: context.Background()}
}

func TestExecutorUnknownToolDenied(t *testing.T) {
	res := newExec(&fakeEditor{}).Handle(chat.McpCall{CallID: "1", ToolName: "exec_command", Params: map[string]any{}})
	if res.Status != "denied" {
		t.Fatalf("expected denied, got %+v", res)
	}
}

func TestExecutorMissingPathError(t *testing.T) {
	res := newExec(&fakeEditor{}).Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{}})
	if res.Status != "error" {
		t.Fatalf("expected error, got %+v", res)
	}
}

func TestExecutorRejectedByUser(t *testing.T) {
	ed := &fakeEditor{permOptionID: ""} // cancelled / no selection
	res := newExec(ed).Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "a.txt"}})
	if res.Status != "denied" {
		t.Fatalf("expected denied, got %+v", res)
	}
	if !ed.asked {
		t.Error("should have requested permission")
	}
}

func TestExecutorApprovedReads(t *testing.T) {
	ed := &fakeEditor{permOptionID: "allow", fileContent: "hello\nworld"}
	res := newExec(ed).Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "mix.exs"}})
	if res.Status != "ok" {
		t.Fatalf("expected ok, got %+v", res)
	}
	if res.Result["content"] != "hello\nworld" {
		t.Errorf("bad content: %v", res.Result["content"])
	}
	if ed.readPath != "mix.exs" {
		t.Errorf("editor read wrong path: %q", ed.readPath)
	}
}

func TestExecutorReadErrorSurfaces(t *testing.T) {
	ed := &fakeEditor{permOptionID: "allow", readErr: errors.New("no such file")}
	res := newExec(ed).Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ghost"}})
	if res.Status != "error" {
		t.Fatalf("expected error, got %+v", res)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/acp/ -run TestExecutor`
Expected: FAIL — `undefined: Executor` / `EditorConn`.

- [ ] **Step 3: Write the ports**

```go
// internal/acp/ports.go
package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// EditorConn is the subset of ACP client calls the bridge makes toward the
// editor. *sdk.AgentSideConnection satisfies it.
type EditorConn interface {
	SessionUpdate(ctx context.Context, params sdk.SessionNotification) error
	RequestPermission(ctx context.Context, params sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error)
	ReadTextFile(ctx context.Context, params sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error)
}

// CloudConn is the subset of the chat WS client the session needs.
// *chat.Client satisfies it.
type CloudConn interface {
	Send(frame any) error
	Events() <-chan chat.Event
	Close()
}

func denied(call chat.McpCall, code, msg string) chat.McpResult {
	return chat.McpResult{CallID: call.CallID, Status: "denied", Error: &chat.FrameError{Code: code, Message: msg}}
}

func errResult(call chat.McpCall, code, msg string) chat.McpResult {
	return chat.McpResult{CallID: call.CallID, Status: "error", Error: &chat.FrameError{Code: code, Message: msg}}
}

// newID returns a short random hex id (for the chat connection session_id).
func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
```

- [ ] **Step 4: Write the executor**

```go
// internal/acp/executor.go
package acp

import (
	"context"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// Executor handles cloud-proposed local tool calls by delegating to the editor.
// It implements chat.Executor. Tool-call *timeline* visibility comes from the
// stream mapping (see stream.go); this only adds permission + the file read.
type Executor struct {
	SessionID  string
	Editor     EditorConn
	Advertised map[string]bool
	Ctx        context.Context
}

func (e *Executor) Handle(call chat.McpCall) chat.McpResult {
	// 1. Known-tool gate — the cloud can only invoke tools we advertised.
	if !e.Advertised[call.ToolName] {
		return denied(call, "unknown_tool", "tool not advertised by this client")
	}
	if call.ToolName != "read_file" {
		return denied(call, "unsupported_tool", "only read_file is supported in v1")
	}
	path, _ := call.Params["path"].(string)
	if path == "" {
		return errResult(call, "invalid_params", "read_file requires a string 'path'")
	}

	// 2. Ask the editor for permission (the editor is the trusted local party).
	tc := sdk.ToolCallUpdate{
		ToolCallId: sdk.ToolCallId(call.CallID),
		Title:      sdk.Ptr("Read " + path),
		Kind:       sdk.Ptr(sdk.ToolKindRead),
		Status:     sdk.Ptr(sdk.ToolCallStatusPending),
	}
	perm, err := e.Editor.RequestPermission(e.Ctx, sdk.RequestPermissionRequest{
		SessionId: sdk.SessionId(e.SessionID),
		ToolCall:  tc,
		Options: []sdk.PermissionOption{
			{OptionId: "allow", Name: "Allow", Kind: sdk.PermissionOptionKindAllowOnce},
			{OptionId: "reject", Name: "Reject", Kind: sdk.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil {
		return errResult(call, "permission_error", err.Error())
	}
	if perm.Outcome.Selected == nil || perm.Outcome.Selected.OptionId != "allow" {
		return denied(call, "denied_by_user", "the user did not allow this read")
	}

	// 3. Delegate the read to the editor (it owns the fs sandbox / unsaved buffers).
	rf, err := e.Editor.ReadTextFile(e.Ctx, sdk.ReadTextFileRequest{
		SessionId: sdk.SessionId(e.SessionID),
		Path:      path,
	})
	if err != nil {
		return errResult(call, "read_error", err.Error())
	}
	return chat.McpResult{CallID: call.CallID, Status: "ok", Result: map[string]any{"content": rf.Content}}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/acp/ -run TestExecutor`
Expected: PASS (5 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/acp/ports.go internal/acp/executor.go internal/acp/executor_test.go
git commit -m "feat(acp): editor-backed read_file executor + ports"
```

---

## Task 6: ACP session + pump loop

**Files:**
- Create: `internal/acp/session.go`
- Test: `internal/acp/session_test.go`

**Interfaces:**
- Consumes: `CloudConn`/`EditorConn` (Task 5); `MapStream`/`TurnEnd` (Task 4); `chat.Event`/`chat.Chat`.
- Produces: `Session{ID, ChatSID string; Cloud CloudConn; Editor EditorConn; Exec chat.Executor; Ctx context.Context}`; `(*Session).Run()`; `(*Session).Prompt(text string) (stopReason string, err error)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/acp/session_test.go
package acp

import (
	"context"
	"testing"
	"time"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// fakeCloud feeds scripted events and records sent frames.
type fakeCloud struct {
	events chan chat.Event
	sent   chan any
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{events: make(chan chat.Event, 16), sent: make(chan any, 16)}
}
func (f *fakeCloud) Send(frame any) error           { f.sent <- frame; return nil }
func (f *fakeCloud) Events() <-chan chat.Event       { return f.events }
func (f *fakeCloud) Close()                          { close(f.events) }

func TestSessionPromptStreamsAndCompletes(t *testing.T) {
	cloud := newFakeCloud()
	ed := &fakeEditor{}
	s := &Session{ID: "conv1", ChatSID: "sid1", Cloud: cloud, Editor: ed, Ctx: context.Background()}
	go s.Run()

	done := make(chan string, 1)
	go func() {
		sr, _ := s.Prompt("hello")
		done <- sr
	}()

	// The prompt should have sent a chat frame.
	select {
	case f := <-cloud.sent:
		c, ok := f.(chat.Chat)
		if !ok || c.Text != "hello" || c.SessionID != "sid1" {
			t.Fatalf("expected chat frame, got %+v", f)
		}
	case <-time.After(time.Second):
		t.Fatal("no chat frame sent")
	}

	// Stream a text delta then end the turn.
	cloud.events <- chat.Event{Kind: chat.KindChatStream, ChatStream: chat.ChatStream{Event: "text.delta", Data: map[string]any{"delta": "Hi"}}}
	cloud.events <- chat.Event{Kind: chat.KindChatStream, ChatStream: chat.ChatStream{Event: "turn.done"}}

	select {
	case sr := <-done:
		if sr != "end_turn" {
			t.Errorf("stopReason = %q, want end_turn", sr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt never completed")
	}

	if len(ed.updates) == 0 {
		t.Error("expected at least one session update forwarded to the editor")
	}
}

func TestSessionDispatchesMcpCallToExecutor(t *testing.T) {
	cloud := newFakeCloud()
	ed := &fakeEditor{permOptionID: "allow", fileContent: "data"}
	s := &Session{
		ID: "conv1", ChatSID: "sid1", Cloud: cloud, Editor: ed, Ctx: context.Background(),
		Exec: &Executor{SessionID: "conv1", Editor: ed, Advertised: map[string]bool{"read_file": true}, Ctx: context.Background()},
	}
	go s.Run()

	cloud.events <- chat.Event{Kind: chat.KindMcpCall, McpCall: chat.McpCall{CallID: "c1", ToolName: "read_file", Params: map[string]any{"path": "a.txt"}}}

	select {
	case f := <-cloud.sent:
		res, ok := f.(chat.McpResult)
		if !ok || res.Status != "ok" || res.Result["content"] != "data" {
			t.Fatalf("expected ok mcp_result, got %+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("executor result never sent back to cloud")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/acp/ -run TestSession`
Expected: FAIL — `undefined: Session`.

- [ ] **Step 3: Write the implementation**

```go
// internal/acp/session.go
package acp

import (
	"context"
	"fmt"
	"sync"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// Session bridges one ACP session to one cloud Conversation over one WS.
//
//   - ID      is the ACP sessionId (== cloud conversation_id); used for editor calls.
//   - ChatSID is the chat connection session_id (caller id); used for cloud chat frames.
type Session struct {
	ID      string
	ChatSID string
	Cloud   CloudConn
	Editor  EditorConn
	Exec    chat.Executor
	Ctx     context.Context

	mu       sync.Mutex
	turnDone chan turnResult
}

type turnResult struct{ errMsg string }

// Run consumes cloud events until the connection closes: it forwards stream
// updates to the editor, dispatches mcp_call to the executor, and signals turn
// completion to a waiting Prompt. Blocking on the executor (permission round-
// trip) naturally pauses the stream — acceptable for v1's single in-flight tool.
func (s *Session) Run() {
	for ev := range s.Cloud.Events() {
		switch ev.Kind {
		case chat.KindChatStream:
			if up, ok := MapStream(ev.ChatStream); ok {
				_ = s.Editor.SessionUpdate(s.Ctx, sdk.SessionNotification{
					SessionId: sdk.SessionId(s.ID),
					Update:    up,
				})
			}
			if ended, errMsg := TurnEnd(ev.ChatStream); ended {
				s.signalTurn(turnResult{errMsg: errMsg})
			}
		case chat.KindMcpCall:
			if s.Exec != nil {
				_ = s.Cloud.Send(s.Exec.Handle(ev.McpCall))
			}
		case chat.KindClosed, chat.KindError:
			s.signalTurn(turnResult{errMsg: "connection closed"})
		}
	}
	s.signalTurn(turnResult{errMsg: "connection closed"})
}

// Prompt sends the user's text as a chat turn and blocks until the turn ends.
func (s *Session) Prompt(text string) (string, error) {
	s.mu.Lock()
	ch := make(chan turnResult, 1)
	s.turnDone = ch
	s.mu.Unlock()

	if err := s.Cloud.Send(chat.Chat{SessionID: s.ChatSID, Text: text}); err != nil {
		return "", err
	}

	res := <-ch
	if res.errMsg != "" {
		return "", fmt.Errorf("turn ended: %s", res.errMsg)
	}
	return "end_turn", nil
}

func (s *Session) signalTurn(r turnResult) {
	s.mu.Lock()
	ch := s.turnDone
	s.turnDone = nil
	s.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/acp/ -run TestSession`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/acp/session.go internal/acp/session_test.go
git commit -m "feat(acp): session pump — stream forwarding, tool dispatch, turn sync"
```

---

## Task 7: ACP agent (SDK boundary)

**Files:**
- Create: `internal/acp/agent.go`
- Test: `internal/acp/agent_test.go`

**Interfaces:**
- Consumes: `Session`/`Executor` (Tasks 5-6); `CloudConn`/`EditorConn`; SDK request/response types.
- Produces:
  - `Agent` struct implementing `sdk.Agent` (all 11 methods).
  - `New(token, apiURL, userAgent string) *Agent`.
  - `(*Agent).SetEditor(e EditorConn)` (back-reference after connection construction).
  - injectable `Dial func(ctx, wsURL, token, ua string) (CloudConn, error)` field (defaults to wrapping `chat.Dial`).

- [ ] **Step 1: Write the failing test**

```go
// internal/acp/agent_test.go
package acp

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

func TestInitializeReportsVersionAndName(t *testing.T) {
	a := New("tok", "https://magus.digital", "magus-cli/test")
	resp, err := a.Initialize(context.Background(), sdk.InitializeRequest{ProtocolVersion: sdk.ProtocolVersion(sdk.ProtocolVersionNumber)})
	if err != nil {
		t.Fatal(err)
	}
	if int(resp.ProtocolVersion) != sdk.ProtocolVersionNumber {
		t.Errorf("protocol version = %d", resp.ProtocolVersion)
	}
	if resp.AgentInfo == nil || resp.AgentInfo.Name != "magus" {
		t.Errorf("expected agent name magus, got %+v", resp.AgentInfo)
	}
}

func TestNewSessionWithoutTokenIsAuthRequired(t *testing.T) {
	a := New("", "https://magus.digital", "ua")
	_, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp"})
	if err == nil {
		t.Fatal("expected an auth-required error without a token")
	}
}

func TestNewSessionDialsAndMapsConversation(t *testing.T) {
	cloud := newFakeCloud()
	a := New("tok", "https://magus.digital", "ua")
	a.SetEditor(&fakeEditor{})
	a.Dial = func(_ context.Context, _, _, _ string) (CloudConn, error) { return cloud, nil }

	// Feed the server_hello the NewSession handshake awaits.
	cloud.events <- chat.Event{Kind: chat.KindServerHello, ChatStream: chat.ChatStream{}, ServerHello: chat.ServerHello{ConversationID: "conv-xyz", AcceptedTools: []string{"read_file"}}}

	resp, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if string(resp.SessionId) != "conv-xyz" {
		t.Errorf("sessionId = %q, want conv-xyz", resp.SessionId)
	}

	// A hello frame must have been sent advertising read_file.
	select {
	case f := <-cloud.sent:
		h, ok := f.(chat.Hello)
		if !ok || len(h.Capabilities.LocalTools) != 1 || h.Capabilities.LocalTools[0] != "read_file" {
			t.Fatalf("expected hello advertising read_file, got %+v", f)
		}
	default:
		t.Fatal("no hello frame sent")
	}
}

func TestNewSessionDialErrorPropagates(t *testing.T) {
	a := New("tok", "https://magus.digital", "ua")
	a.SetEditor(&fakeEditor{})
	a.Dial = func(_ context.Context, _, _, _ string) (CloudConn, error) { return nil, errors.New("dial boom") }
	if _, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp"}); err == nil {
		t.Fatal("expected dial error to propagate")
	}
}

func TestUnsupportedMethodsReturnMethodNotFound(t *testing.T) {
	a := New("tok", "https://magus.digital", "ua")
	if _, err := a.SetSessionMode(context.Background(), sdk.SetSessionModeRequest{}); err == nil {
		t.Error("SetSessionMode should be unsupported")
	}
	if _, err := a.Authenticate(context.Background(), sdk.AuthenticateRequest{}); err == nil {
		t.Error("Authenticate should be unsupported")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/acp/ -run 'TestInitialize|TestNewSession|TestUnsupported'`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the implementation**

```go
// internal/acp/agent.go
package acp

import (
	"context"
	"fmt"
	"sync"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// Agent implements sdk.Agent: it bridges an ACP editor to the magus cloud agent.
type Agent struct {
	token  string
	apiURL string
	ua     string

	// Dial opens a cloud chat connection; injectable for tests. Defaults to chat.Dial.
	Dial func(ctx context.Context, wsURL, token, ua string) (CloudConn, error)

	mu       sync.Mutex
	editor   EditorConn
	sessions map[string]*Session
}

// New builds an Agent. Call SetEditor after constructing the SDK connection.
func New(token, apiURL, userAgent string) *Agent {
	return &Agent{
		token:    token,
		apiURL:   apiURL,
		ua:       userAgent,
		sessions: map[string]*Session{},
		Dial: func(ctx context.Context, wsURL, token, ua string) (CloudConn, error) {
			return chat.Dial(ctx, wsURL, token, ua)
		},
	}
}

// SetEditor stores the back-reference to the ACP connection (which is created
// from this agent), so sessions can push updates and request permissions.
func (a *Agent) SetEditor(e EditorConn) {
	a.mu.Lock()
	a.editor = e
	a.mu.Unlock()
}

func (a *Agent) Initialize(_ context.Context, req sdk.InitializeRequest) (sdk.InitializeResponse, error) {
	return sdk.InitializeResponse{
		ProtocolVersion:   sdk.ProtocolVersion(sdk.ProtocolVersionNumber),
		AgentInfo:         &sdk.Implementation{Name: "magus"},
		AgentCapabilities: sdk.AgentCapabilities{LoadSession: false},
		AuthMethods:       []sdk.AuthMethod{},
	}, nil
}

func (a *Agent) NewSession(ctx context.Context, req sdk.NewSessionRequest) (sdk.NewSessionResponse, error) {
	if a.token == "" {
		return sdk.NewSessionResponse{}, sdk.NewAuthRequired("no magus token; run `magus login` first")
	}
	wsURL, err := chat.WSURL(a.apiURL)
	if err != nil {
		return sdk.NewSessionResponse{}, err
	}
	cloud, err := a.Dial(ctx, wsURL, a.token, a.ua)
	if err != nil {
		return sdk.NewSessionResponse{}, fmt.Errorf("connect to magus: %w", err)
	}

	chatSID := newID()
	if err := cloud.Send(chat.Hello{
		SessionID:    chatSID,
		Capabilities: chat.Capabilities{LocalTools: []string{"read_file"}},
		Conversation: map[string]any{"new": true},
	}); err != nil {
		cloud.Close()
		return sdk.NewSessionResponse{}, err
	}

	// Await server_hello (first inbound event) to learn the conversation id.
	ev, ok := <-cloud.Events()
	if !ok || ev.Kind != chat.KindServerHello {
		cloud.Close()
		return sdk.NewSessionResponse{}, fmt.Errorf("did not receive server_hello")
	}
	convID := ev.ServerHello.ConversationID

	a.mu.Lock()
	editor := a.editor
	a.mu.Unlock()

	sess := &Session{
		ID:      convID,
		ChatSID: chatSID,
		Cloud:   cloud,
		Editor:  editor,
		Ctx:     context.Background(),
		Exec: &Executor{
			SessionID:  convID,
			Editor:     editor,
			Advertised: map[string]bool{"read_file": true},
			Ctx:        context.Background(),
		},
	}
	a.mu.Lock()
	a.sessions[convID] = sess
	a.mu.Unlock()
	go sess.Run()

	return sdk.NewSessionResponse{SessionId: sdk.SessionId(convID)}, nil
}

func (a *Agent) Prompt(_ context.Context, req sdk.PromptRequest) (sdk.PromptResponse, error) {
	a.mu.Lock()
	sess := a.sessions[string(req.SessionId)]
	a.mu.Unlock()
	if sess == nil {
		return sdk.PromptResponse{}, sdk.NewInvalidParams("unknown session")
	}
	stop, err := sess.Prompt(promptText(req.Prompt))
	if err != nil {
		return sdk.PromptResponse{}, err
	}
	return sdk.PromptResponse{StopReason: sdk.StopReason(stop)}, nil
}

// Cancel is a v1 no-op (no server cancel path yet); the turn runs to completion.
func (a *Agent) Cancel(_ context.Context, _ sdk.CancelNotification) error { return nil }

// promptText concatenates the text content blocks of a prompt.
func promptText(blocks []sdk.ContentBlock) string {
	var out string
	for _, b := range blocks {
		if b.Text != nil {
			out += b.Text.Text
		}
	}
	return out
}

// --- Unsupported methods (advertise nothing; reject cleanly) ----------------

func (a *Agent) Authenticate(context.Context, sdk.AuthenticateRequest) (sdk.AuthenticateResponse, error) {
	return sdk.AuthenticateResponse{}, sdk.NewMethodNotFound("authenticate")
}
func (a *Agent) Logout(context.Context, sdk.LogoutRequest) (sdk.LogoutResponse, error) {
	return sdk.LogoutResponse{}, sdk.NewMethodNotFound("logout")
}
func (a *Agent) CloseSession(context.Context, sdk.CloseSessionRequest) (sdk.CloseSessionResponse, error) {
	return sdk.CloseSessionResponse{}, sdk.NewMethodNotFound("session/close")
}
func (a *Agent) ListSessions(context.Context, sdk.ListSessionsRequest) (sdk.ListSessionsResponse, error) {
	return sdk.ListSessionsResponse{}, sdk.NewMethodNotFound("session/list")
}
func (a *Agent) ResumeSession(context.Context, sdk.ResumeSessionRequest) (sdk.ResumeSessionResponse, error) {
	return sdk.ResumeSessionResponse{}, sdk.NewMethodNotFound("session/resume")
}
func (a *Agent) SetSessionConfigOption(context.Context, sdk.SetSessionConfigOptionRequest) (sdk.SetSessionConfigOptionResponse, error) {
	return sdk.SetSessionConfigOptionResponse{}, sdk.NewMethodNotFound("session/set_config_option")
}
func (a *Agent) SetSessionMode(context.Context, sdk.SetSessionModeRequest) (sdk.SetSessionModeResponse, error) {
	return sdk.SetSessionModeResponse{}, sdk.NewMethodNotFound("session/set_mode")
}
```

> **Verify at this step:** run `go build ./...`. If any unsupported-method request/response type name differs from the above (e.g. `CloseSessionRequest`), fix from `go doc github.com/coder/acp-go-sdk.Agent`. The compiler enforces that `*Agent` satisfies `sdk.Agent` once it's passed to `NewAgentSideConnection` in Task 8 — a missing/mis-typed method fails the build there.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/acp/`
Expected: PASS (all acp unit tests).

- [ ] **Step 5: Commit**

```bash
git add internal/acp/agent.go internal/acp/agent_test.go
git commit -m "feat(acp): sdk.Agent implementation (initialize, new_session, prompt)"
```

---

## Task 8: `magus acp` command + wiring

**Files:**
- Create: `internal/cli/acp.go`
- Modify: `internal/cli/root.go` (register the command)
- Test: `internal/cli/acp_test.go`

**Interfaces:**
- Consumes: `acp.New`/`acp.Agent.SetEditor` (Task 7); `sdk.NewAgentSideConnection`; `config.ResolveToken`/`ResolveAPIURL`; `DefaultAPIURL`/`profile`/`Version` (package `cli`).
- Produces: `newACPCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/acp_test.go
package cli

import "testing"

func TestNewACPCmd(t *testing.T) {
	cmd := newACPCmd()
	if cmd.Use != "acp" {
		t.Errorf("Use = %q, want acp", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Error("acp command must have a RunE")
	}
}

func TestACPCmdRegistered(t *testing.T) {
	root := newRootCmd()
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "acp" {
			found = true
			if c.GroupID != "agent" {
				t.Errorf("acp group = %q, want agent", c.GroupID)
			}
		}
	}
	if !found {
		t.Error("acp command not registered on root")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run ACP`
Expected: FAIL — `undefined: newACPCmd`.

- [ ] **Step 3: Write the command**

```go
// internal/cli/acp.go
package cli

import (
	"fmt"
	"os"

	sdk "github.com/coder/acp-go-sdk"
	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/magus-cli/internal/acp"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

func newACPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "acp",
		Short: "Run as an ACP agent for editors (Zed, etc.)",
		Long: `Run as an Agent Client Protocol (ACP) agent over stdio.

An ACP-aware editor (Zed and others) launches 'magus acp' as a subprocess and
drives the magus cloud agent through it. When the agent reads a local file, the
editor services the read behind its own permission prompt.

stdin/stdout carry the JSON-RPC protocol; do not pipe other data through them.
Authentication uses the active profile's token (run 'magus login' first).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			token := config.ResolveToken(cfg, profile)
			apiURL := config.ResolveAPIURL(cfg, profile, DefaultAPIURL)

			agent := acp.New(token, apiURL, "magus-cli/"+Version)
			// stdout/stdin are the protocol channel; diagnostics go to stderr.
			conn := sdk.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
			agent.SetEditor(conn)

			fmt.Fprintln(os.Stderr, "magus acp: connected to editor over stdio")
			<-conn.Done()
			return nil
		},
	}
}
```

- [ ] **Step 4: Register the command**

In `internal/cli/root.go`, in the `agent` group (after `newMCPCmd()`), add:

```go
	addInGroup("agent", newACPCmd())
```

So the block reads:

```go
	addInGroup("agent", newMCPCmd())
	addInGroup("agent", newACPCmd())
	addInGroup("agent", newSkillCmd())
```

- [ ] **Step 5: Run tests + build to verify**

Run: `go build ./... && go test ./internal/cli/ -run ACP`
Expected: PASS. The `go build` is the real check that `*acp.Agent` satisfies `sdk.Agent` (it is passed to `NewAgentSideConnection` here).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/acp.go internal/cli/root.go internal/cli/acp_test.go
git commit -m "feat(acp): magus acp command + registration"
```

---

## Task 9: In-process end-to-end integration test

**Files:**
- Create: `internal/acp/integration_test.go`

**Interfaces:**
- Consumes: everything above; `sdk.NewClientSideConnection` (a stub editor); a stub chat WS server.

> This wires a real ACP client (the SDK's client side) to our agent over `io.Pipe`, and a stub chat WS server (httptest) that scripts the cloud. It proves the full path: `Initialize → NewSession → Prompt → (text stream) → mcp_call → request_permission → fs/read_text_file → mcp_result → turn.done → end_turn`.

- [ ] **Step 1: Write the test**

```go
// internal/acp/integration_test.go
package acp

import (
	"context"
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
	// Accumulate any agent message text we can find by re-marshalling.
	s.mu.Lock()
	defer s.mu.Unlock()
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
	editor.mu.Unlock()
	if readPath != "mix.exs" {
		t.Errorf("editor was asked to read %q, want mix.exs", readPath)
	}
}

// test-only helpers (exported wire helpers are unexported in the package).
func wrapTest(typ string, v any) ([]byte, error) { return wrapForTest(typ, v) }
```

> The chat package's `wrap`/`decodePayload` are unexported. Add **test-only** thin wrappers in this file's package (they compile because the test is in package `acp`, not `chat`) by re-implementing them locally:
> ```go
> import "encoding/json"
> func wrapForTest(typ string, v any) ([]byte, error) {
> 	raw, err := json.Marshal(v); if err != nil { return nil, err }
> 	var env map[string]any
> 	if err := json.Unmarshal(raw, &env); err != nil { return nil, err }
> 	env["type"] = typ; env["v"] = 1
> 	return json.Marshal(env)
> }
> func jsonUnmarshal(b []byte, out any) error { return json.Unmarshal(b, out) }
> ```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/acp/ -run TestACPEndToEnd -v`
Expected: PASS. If it hangs, the most likely cause is the `NewSession` server_hello handshake racing the pump; confirm the stub cloud sends `server_hello` before `chat_stream` (it does).

- [ ] **Step 3: Full verification**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: All green.

- [ ] **Step 4: Commit**

```bash
git add internal/acp/integration_test.go
git commit -m "test(acp): in-process editor<->agent<->stub-cloud end-to-end"
```

---

## Manual end-to-end smoke (after the chat server bridge exists)

The unit + integration suite proves the CLI in isolation. A real smoke test needs the **chat server bridge** (`docs/superpowers/plans/2026-06-02-magus-chat-server-bridge.md`) running. Then:

1. `magus login` against the dev cloud (mint a PAT).
2. Configure Zed's external agent (in `settings.json`):
   ```json
   { "agent_servers": { "magus": { "command": "magus", "args": ["acp"] } } }
   ```
   (Confirm the exact key against Zed's current external-agents docs — see the spec §14.)
3. Open the magus agent in Zed, prompt "what's in mix.exs?", approve the read, watch the answer stream in.

---

## Self-review notes (coverage vs. spec)

- **§2 two-sided bridge** → Tasks 1-3 (cloud transport) + Task 7 (`sdk.Agent`) + Task 8 (wiring). **§3 Executor seam** → `chat.Executor` (Task 2) implemented by `acp.Executor` (Task 5). **§4 ACP surface** → Task 7 (4 real methods + 7 stubs; LoadSession intentionally absent). **§5 session lifecycle** → Tasks 6-7 (sessionId↔conversation_id, hello→server_hello, prompt→chat). **§6 stream mapping** → Task 4. **§7 tool execution + known-tool gate** → Task 5. **§8 auth** → Task 7 (`NewAuthRequired` when no token) + Task 8 (`ResolveToken`). **§13 cancel stub** → Task 7 `Cancel` no-op. **§14 testing** → Tasks 4-9 (unit + in-process e2e).
- **Deferred (per spec, not in this plan):** `write_file`/terminal, `session/load`, server cancel frame, persistent allow-always, MCP-server passthrough, rich tool-call diffs, the Bubble Tea TUI.
- **Naming deviation from spec §3/§9:** the `Executor` interface lives in `internal/chat` (not `internal/localtool`), because `localtool` is not built in the ACP track and the interface only needs the wire types. The chat plan's `localtool.Pipeline` will satisfy `chat.Executor` when built.
- **Transport overlap:** `internal/chat/{url,protocol,client}.go` (Tasks 1-3) are the same files chat Plan 2 Tasks 1, 2, 10 produce. Whichever plan runs first creates them; the other verifies and skips. Built here without the `localtool` pipeline or TUI.
