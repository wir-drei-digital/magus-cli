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
	// 4. Bound the frame. The editor's read is unbounded (it may be a whole
	// binary file) and the server closes the connection on frames over 1MB.
	// chat.Client.Send fits every mcp_result too — this second call is deliberate
	// belt-and-braces so the invariant holds for any CloudConn wired in here.
	return chat.FitMcpResult(chat.McpResult{
		CallID: call.CallID,
		Status: "ok",
		Result: map[string]any{"content": rf.Content},
	})
}
