package localtool

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

// stubApprover returns a fixed decision.
type stubApprover struct{ d Decision }

func (s stubApprover) Approve(Plan) (Decision, error) { return s.d, nil }

// funcApprover runs an arbitrary callback as the approval step, so a test can
// fail the prompt, observe the exact Plan the user was shown, or mutate the
// filesystem inside the approval window (between Plan and Execute).
type funcApprover struct{ fn func(Plan) (Decision, error) }

func (f funcApprover) Approve(plan Plan) (Decision, error) { return f.fn(plan) }

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

// TestPipelineFailureCodes pins the wire contract of every rejection path. The
// codes are what the cloud side branches on, so they are API: a rename here is
// a protocol change and must fail loudly rather than silently reshape the
// frames. Each row also asserts the fail-closed invariants that apply to ALL
// rejections — an Error is always present, no Result ever rides along, and the
// CallID is echoed so the caller can correlate the rejection.
func TestPipelineFailureCodes(t *testing.T) {
	prompt := config.Permissions{Read: "prompt"}

	// An approver that fails while ALSO returning Allow: "could not ask" must
	// never read as consent, so the error has to win over the decision.
	erroringApprover := funcApprover{fn: func(Plan) (Decision, error) {
		return DecisionAllow, errors.New("terminal closed")
	}}
	// The approved target vanishes inside the approval window: the user said
	// yes, but by the time Execute opens the path there is nothing there.
	vanishingApprover := funcApprover{fn: func(plan Plan) (Decision, error) {
		if err := os.Remove(plan.MatchPath); err != nil {
			return DecisionDeny, err
		}
		return DecisionAllow, nil
	}}

	tests := []struct {
		name     string
		perms    config.Permissions
		approver Approver
		tool     string
		params   map[string]any
		status   string
		code     string
	}{
		{"unadvertised tool", prompt, stubApprover{DecisionAllow}, "exec_command", map[string]any{}, "denied", "unknown_tool"},
		{"missing path param", prompt, stubApprover{DecisionAllow}, "read_file", map[string]any{}, "error", "invalid_params"},
		{"non-string path param", prompt, stubApprover{DecisionAllow}, "read_file", map[string]any{"path": 42}, "error", "invalid_params"},
		{"confinement escape", prompt, stubApprover{DecisionAllow}, "read_file", map[string]any{"path": "../../etc/passwd"}, "denied", "unsafe"},
		{"deny tier", config.Permissions{Read: "deny"}, stubApprover{DecisionAllow}, "read_file", map[string]any{"path": "ok.txt"}, "denied", "denied_by_policy"},
		{"user declines", prompt, stubApprover{DecisionDeny}, "read_file", map[string]any{"path": "ok.txt"}, "denied", "denied_by_user"},
		{"approval fails", prompt, erroringApprover, "read_file", map[string]any{"path": "ok.txt"}, "error", "approval_error"},
		{"execution fails", prompt, vanishingApprover, "read_file", map[string]any{"path": "ok.txt"}, "error", "execute_error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newPipeline(t, tc.approver, tc.perms)
			res := p.Handle(chat.McpCall{CallID: "call-7", ToolName: tc.tool, Params: tc.params})

			if res.Status != tc.status {
				t.Fatalf("status = %q, want %q (%+v)", res.Status, tc.status, res)
			}
			if res.Error == nil {
				t.Fatalf("a rejection must carry an error, got %+v", res)
			}
			if res.Error.Code != tc.code {
				t.Errorf("error code = %q, want %q", res.Error.Code, tc.code)
			}
			if res.Error.Message == "" {
				t.Error("error message must not be empty")
			}
			if res.Result != nil {
				t.Errorf("fail-closed: a rejected call must return no result, got %v", res.Result)
			}
			if res.CallID != "call-7" {
				t.Errorf("call_id = %q, want the inbound %q", res.CallID, "call-7")
			}
		})
	}
}

// TestPipelineAuditsEveryOutcome covers step 6 end-to-end against a real
// FileAudit: every decision — including the ones that never touch the disk —
// leaves exactly one line, and the line records the CLIENT-canonical action.
// The log is the after-the-fact record of what was proposed and what was
// allowed, so raw server-supplied params must never appear in it verbatim:
// they are unvalidated bytes, and a log that echoes them lets the server write
// arbitrary text into a local security artifact.
func TestPipelineAuditsEveryOutcome(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	audit := &FileAudit{Path: logPath}

	// Two markers that can only reach the log if the pipeline copies raw params
	// into it. rawPath resolves to <root>/ok.txt, so the literal "sub/../"
	// segment is textually absent from every client-canonical Display.
	const rawPath = "sub/../ok.txt"
	const unknownMarker = "unknown-tool-param-marker"

	newP := func(perms config.Permissions, approver Approver) *Pipeline {
		return &Pipeline{
			Registry:       Registry{"read_file": &ReadFile{Root: root, MaxBytes: 1024}},
			Policy:         NewPolicy(perms),
			Approver:       approver,
			Audit:          audit,
			ConversationID: "conv-audit",
		}
	}
	read := func(p *Pipeline, callID string) chat.McpResult {
		return p.Handle(chat.McpCall{CallID: callID, ToolName: "read_file", Params: map[string]any{"path": rawPath}})
	}

	prompt := config.Permissions{Read: "prompt"}
	newP(prompt, stubApprover{DecisionAllow}).Handle(chat.McpCall{
		CallID: "1", ToolName: "exec_command", Params: map[string]any{"path": unknownMarker},
	})
	read(newP(config.Permissions{Read: "deny"}, stubApprover{DecisionAllow}), "2")
	read(newP(prompt, stubApprover{DecisionDeny}), "3")

	var approved Plan
	spy := funcApprover{fn: func(plan Plan) (Decision, error) { approved = plan; return DecisionAllow, nil }}
	if res := read(newP(prompt, spy), "4"); res.Status != "ok" {
		t.Fatalf("setup: the approved read should have succeeded, got %+v", res)
	}

	entries := readAuditLines(t, logPath)
	want := []string{"deny", "deny", "deny", "allow"}
	if len(entries) != len(want) {
		t.Fatalf("expected %d audit lines (one per decision), got %d: %+v", len(want), len(entries), entries)
	}
	for i, w := range want {
		if entries[i].Decision != w {
			t.Errorf("line %d: decision = %q, want %q", i, entries[i].Decision, w)
		}
		if entries[i].ConversationID != "conv-audit" {
			t.Errorf("line %d: conversation_id = %q, want %q", i, entries[i].ConversationID, "conv-audit")
		}
		if entries[i].TS.IsZero() {
			t.Errorf("line %d: entry has no timestamp", i)
		}
	}
	// The unadvertised tool is rejected before any Plan exists, so its name is
	// all the record can carry — but it must carry that much, or the log cannot
	// say what was refused.
	if entries[0].Tool != "exec_command" {
		t.Errorf("rejected line: tool = %q, want %q", entries[0].Tool, "exec_command")
	}
	if approved.Display == "" || entries[3].Display != approved.Display {
		t.Errorf("allow line must record the Display the user actually approved: logged %q, approved %q",
			entries[3].Display, approved.Display)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{rawPath, unknownMarker} {
		if strings.Contains(string(raw), marker) {
			t.Errorf("audit log echoed raw server-supplied params (%q):\n%s", marker, raw)
		}
	}
}
