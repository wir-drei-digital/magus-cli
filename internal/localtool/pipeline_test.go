package localtool

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

// failingAudit is an Auditor that never records anything — a 0400 log, a full
// disk, a read-only config directory.
type failingAudit struct {
	calls int
	err   error
}

func (f *failingAudit) Record(AuditEntry) error {
	f.calls++
	return f.err
}

// FileAudit was deliberately hardened to SURFACE its failures (close errors
// propagate, a failed chmod is an error, not a warning) — which buys nothing if
// its only consumer throws the error away. A log that has stopped recording is
// a security event: reads keep executing and keep returning content, so the one
// person who can act on it must be told. The pipeline reports it through
// OnAuditError and otherwise carries on: auditing is not a gate, and failing the
// call closed would let a broken log file become a denial of service.
func TestPipelineReportsAuditFailures(t *testing.T) {
	tests := []struct {
		name       string
		perms      config.Permissions
		approver   Approver
		tool       string
		params     map[string]any
		wantStatus string
	}{
		{"allow path", config.Permissions{Read: "prompt"}, stubApprover{DecisionAllow}, "read_file", map[string]any{"path": "ok.txt"}, "ok"},
		{"deny path", config.Permissions{Read: "deny"}, stubApprover{DecisionAllow}, "read_file", map[string]any{"path": "ok.txt"}, "denied"},
		{"pre-Plan rejection", config.Permissions{Read: "prompt"}, stubApprover{DecisionAllow}, "exec_command", map[string]any{}, "denied"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newPipeline(t, tc.approver, tc.perms)
			boom := errors.New("no space left on device")
			audit := &failingAudit{err: boom}
			p.Audit = audit

			var got []error
			p.OnAuditError = func(err error) { got = append(got, err) }

			res := p.Handle(chat.McpCall{CallID: "1", ToolName: tc.tool, Params: tc.params})

			if audit.calls != 1 {
				t.Fatalf("expected exactly one Record attempt, got %d", audit.calls)
			}
			if len(got) != 1 {
				t.Fatalf("audit failure was not surfaced: callback got %v", got)
			}
			if !errors.Is(got[0], boom) {
				t.Errorf("callback got %v, want the underlying %v", got[0], boom)
			}
			// The decision itself is unaffected — the cloud still gets the same
			// answer it would have got with a healthy log.
			if res.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q (audit must stay non-fatal): %+v", res.Status, tc.wantStatus, res)
			}
			if tc.wantStatus == "ok" && res.Result["content"] != "hello" {
				t.Errorf("result changed by the audit failure: %+v", res.Result)
			}
		})
	}
}

// A nil OnAuditError is the documented default (the ACP front-end wires no
// callback), so a failing log must not panic the session.
func TestPipelineAuditFailureWithoutCallback(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	p.Audit = &failingAudit{err: errors.New("boom")}

	if res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ok.txt"}}); res.Status != "ok" {
		t.Fatalf("expected ok, got %+v", res)
	}
}

// A healthy log must not cry wolf: OnAuditError exists to report a broken audit
// trail, and a callback that fires on success would train the user to ignore it.
func TestPipelineDoesNotReportSuccessfulAudits(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	called := 0
	p.OnAuditError = func(error) { called++ }

	if res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ok.txt"}}); res.Status != "ok" {
		t.Fatalf("setup: expected ok, got %+v", res)
	}
	if called != 0 {
		t.Errorf("OnAuditError fired %d times for a successful write", called)
	}
}

// The tool name on a rejection is raw server input, and it is the ONE field the
// log copies verbatim (there is no Plan yet to render). Unbounded, that makes
// the audit log a server-writable growth vector: the 8MB inbound frame limit is
// the only other bound, so each rejected call could append megabytes to
// chat-audit.jsonl. Bound it at the record, where the untrusted value enters the
// file.
func TestPipelineBoundsAuditedToolName(t *testing.T) {
	tests := []struct {
		name string
		tool string
	}{
		{"ascii", strings.Repeat("A", 100_000)},
		// Multi-byte: the cut must land on a rune boundary, or the log line
		// carries a mangled half-rune (and JSON-encodes it as U+FFFD).
		{"multi-byte runes", strings.Repeat("é", 100)},        // 2 bytes each
		{"three-byte runes", strings.Repeat("世", 100)},        // 3 bytes each
		{"four-byte runes", strings.Repeat("\U0001f600", 50)}, // 4 bytes each
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "audit.jsonl")
			p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
			p.Audit = &FileAudit{Path: logPath}

			// An unadvertised tool is rejected at step 1, before any Plan exists,
			// so the raw name is what reaches recordOrTool.
			if res := p.Handle(chat.McpCall{CallID: "1", ToolName: tc.tool, Params: map[string]any{}}); res.Status != "denied" {
				t.Fatalf("setup: expected denied, got %+v", res)
			}

			entries := readAuditLines(t, logPath)
			if len(entries) != 1 {
				t.Fatalf("expected 1 audit line, got %d", len(entries))
			}
			logged := entries[0].Tool
			if len(logged) > maxAuditToolName {
				t.Errorf("logged tool name is %d bytes, want <= %d", len(logged), maxAuditToolName)
			}
			if logged == "" {
				t.Error("the log must still say what was refused")
			}
			if !utf8.ValidString(logged) {
				t.Errorf("truncation split a rune: %q", logged)
			}
			if !strings.HasPrefix(tc.tool, logged) {
				t.Errorf("logged name %q is not a prefix of the requested name", logged)
			}
			// The whole point: the line stays small no matter what was sent.
			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) > 1024 {
				t.Errorf("audit line is %d bytes for a %d-byte tool name", len(raw), len(tc.tool))
			}
		})
	}
}

// A tool name that is already short is API the log should reproduce exactly —
// truncation must not nibble at ordinary names.
func TestPipelineKeepsShortToolNamesVerbatim(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	p.Audit = &FileAudit{Path: logPath}

	const name = "exec_command"
	if res := p.Handle(chat.McpCall{CallID: "1", ToolName: name, Params: map[string]any{}}); res.Status != "denied" {
		t.Fatalf("setup: expected denied, got %+v", res)
	}
	if got := readAuditLines(t, logPath)[0].Tool; got != name {
		t.Errorf("tool = %q, want %q", got, name)
	}
}
