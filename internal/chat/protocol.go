// Package chat speaks the magus cloud chat protocol: {type, v} framed JSON over
// one WebSocket, shared by both front-ends (`magus chat` and the `magus acp`
// bridge).
//
// Server contract this client is written against (magus PR #29):
//
//   - hello is sent exactly ONCE per connection. Reconnecting means a new socket
//     plus a fresh hello carrying conversation.resume; a second hello on a live
//     connection is answered with an already_initialized error frame.
//   - Any session_id we send is ignored: the server routes by authenticated user
//     id plus its own resolved conversation id. We keep sending ours because it
//     is what correlates frames on THIS side.
//   - Tokens must be write-scoped. A read-scoped token is rejected at connect
//     with HTTP 403 insufficient_scope; an invalid or expired one with 401 (see
//     dialErr in client.go).
//   - Error frame codes to expect: not_ready (chat before hello), bad_frame
//     (undecodable frame or missing text), already_initialized. They arrive as
//     ordinary error frames and surface as KindError events.
//   - Idle contract: the server times out a receive after 60s and wants a ping
//     at least once a minute; the write loop pings every 25s.
//   - Inbound frames are capped at 1MB and an OVERSIZE FRAME CLOSES THE
//     CONNECTION — there is no error frame for it. Outbound mcp_result frames
//     are therefore budgeted by encoded size in FitMcpResult (fit.go), applied
//     at the Client.Send choke point so no front-end can forget.
package chat

import (
	"encoding/json"
	"errors"
	"fmt"
)

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

// decodeHead parses and validates the {type, v} envelope of an inbound frame.
// Every frame must carry a non-empty type and the supported protocol version.
// A returned non-empty type alongside an error means the envelope parsed but
// failed validation (e.g. version mismatch) — a protocol error worth surfacing,
// as opposed to undecodable garbage (empty type + error), which callers ignore.
func decodeHead(data []byte) (string, error) {
	var head struct {
		Type string `json:"type"`
		V    int    `json:"v"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return "", err
	}
	if head.Type == "" {
		return "", errors.New("frame missing type")
	}
	if head.V != protocolVersion {
		return head.Type, fmt.Errorf("unsupported protocol version %d (want %d)", head.V, protocolVersion)
	}
	return head.Type, nil
}

func decodePayload(data []byte, out any) error { return json.Unmarshal(data, out) }
