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
