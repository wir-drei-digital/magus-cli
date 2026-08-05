package localtool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

// stubApprover returns a fixed decision.
type stubApprover struct{ d Decision }

func (s stubApprover) Approve(Plan) (Decision, error) { return s.d, nil }

func newPipeline(t *testing.T, approver Approver, perms config.Permissions) (*Pipeline, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := Registry{"read_file": &ReadFile{Root: root, MaxBytes: 1024}}
	p := &Pipeline{
		Registry: reg,
		Policy:   NewPolicy(perms),
		Approver: approver,
		Audit:    &FileAudit{Path: filepath.Join(t.TempDir(), "audit.jsonl")},
	}
	return p, root
}

func TestPipelineUnknownToolDenied(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "exec_command", Params: map[string]any{}})
	if res.Status != "denied" {
		t.Fatalf("expected denied, got %+v", res)
	}
}

func TestPipelineInvalidParamsError(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{}})
	if res.Status != "error" {
		t.Fatalf("expected error, got %+v", res)
	}
}

func TestPipelineConfinementDenied(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "../../etc/passwd"}})
	if res.Status != "denied" {
		t.Fatalf("escape must be denied, got %+v", res)
	}
}

func TestPipelinePolicyDenyShortCircuits(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "deny"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ok.txt"}})
	if res.Status != "denied" {
		t.Fatalf("deny tier must deny, got %+v", res)
	}
}

func TestPipelinePromptDeniedByUser(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionDeny}, config.Permissions{Read: "prompt"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ok.txt"}})
	if res.Status != "denied" {
		t.Fatalf("user-denied must be denied, got %+v", res)
	}
}

func TestPipelineApprovedReadsFile(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ok.txt"}})
	if res.Status != "ok" {
		t.Fatalf("expected ok, got %+v", res)
	}
	if res.Result["content"].(string) != "hello" {
		t.Errorf("bad content: %v", res.Result["content"])
	}
}

func TestPipelineAllowAlwaysPersists(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllowAlways}, config.Permissions{Read: "prompt"})
	_ = p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ok.txt"}})
	if len(p.Policy.Permissions().Allow) != 1 {
		t.Errorf("allow-always should have persisted a rule")
	}
}
