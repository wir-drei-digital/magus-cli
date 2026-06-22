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

func TestExecutorReadFileDeniedWhenNotAdvertised(t *testing.T) {
	// fs off → NewSession advertises no local tools → Advertised is empty → an
	// inbound read_file mcp_call must be denied at the gate, with no permission
	// round-trip to the editor.
	ed := &fakeEditor{permOptionID: "allow", fileContent: "x"}
	e := &Executor{SessionID: "s", Editor: ed, Advertised: map[string]bool{}, Ctx: context.Background()}
	res := e.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "a.txt"}})
	if res.Status != "denied" {
		t.Fatalf("read_file must be denied when not advertised, got %+v", res)
	}
	if ed.asked {
		t.Error("must not request permission for an unadvertised tool")
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
