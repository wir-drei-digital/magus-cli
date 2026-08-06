package localtool

import (
	"unicode/utf8"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// Pipeline is the local Policy Decision + Enforcement Point. Every mcp_call runs
// the six fail-closed steps before anything touches the filesystem.
type Pipeline struct {
	Registry       Registry
	Policy         *Policy
	Approver       Approver
	Audit          Auditor
	ConversationID string

	// OnAllowAlways is called when the user persists an "allow always" rule, so
	// the caller can save the updated config. Optional.
	OnAllowAlways func(*Policy)

	// OnAuditError is called with any error the Auditor returns. FileAudit goes
	// out of its way to surface its failures — close errors propagate, a failed
	// chmod is an error rather than a warning — and that is wasted if the only
	// consumer discards them: a 0400 log, a full disk or a read-only config
	// directory would leave every read still executing and returning content
	// while the security trail quietly stopped.
	//
	// Auditing is deliberately NOT a gate. The call has already run by the time
	// this fires and its result still goes back to the cloud; failing closed on
	// a broken log would turn one bad file into a denial of service. The
	// callback exists so the one person who can fix the log finds out. Optional
	// — a nil callback keeps the old silent behaviour.
	OnAuditError func(error)
}

// Pipeline is the terminal front-end's Executor: the session loop hands it every
// inbound mcp_call and returns whatever it decides.
var _ chat.Executor = (*Pipeline)(nil)

func (p *Pipeline) Handle(call chat.McpCall) chat.McpResult {
	// 1. Known-tool gate.
	tool, ok := p.Registry[call.ToolName]
	if !ok {
		return p.deny(call, "unknown_tool", "tool not advertised by this client")
	}

	// 2. Schema validation. Deliberately before Plan even though ReadFile.Plan
	// re-validates internally: malformed params are a protocol "error", not a
	// safety "denied", and only running Validate first gets that distinction
	// right.
	if err := tool.Validate(call.Params); err != nil {
		return p.fail(call, "invalid_params", err.Error())
	}

	// 3. Tool-specific safety (e.g. path confinement) + canonical plan.
	plan, err := tool.Plan(call.Params)
	if err != nil {
		return p.deny(call, "unsafe", err.Error())
	}

	// 4. Policy decision (+ approval). Decide already short-circuits an explicit
	// "deny" tier ahead of the persisted allow rules, so there is no deny logic
	// to repeat here.
	switch p.Policy.Decide(plan) {
	case DecisionDeny:
		return p.deny(call, "denied_by_policy", "blocked by local policy", plan)
	case DecisionAllow:
		// proceed
	default: // DecisionPrompt
		decision, aerr := p.Approver.Approve(plan)
		if aerr != nil {
			return p.fail(call, "approval_error", aerr.Error(), plan)
		}
		switch decision {
		case DecisionAllowAlways:
			p.Policy.AddAllow(plan)
			if p.OnAllowAlways != nil {
				p.OnAllowAlways(p.Policy)
			}
		case DecisionAllow:
			// proceed
		default:
			return p.deny(call, "denied_by_user", "the user denied this action", plan)
		}
	}

	// 5. Execute.
	out, err := tool.Execute(plan)
	if err != nil {
		return p.fail(call, "execute_error", err.Error(), plan)
	}

	// 6. Audit + return.
	p.record(plan, "allow")
	result, _ := out.(map[string]any)
	return chat.McpResult{CallID: call.CallID, Status: "ok", Result: result}
}

func (p *Pipeline) deny(call chat.McpCall, code, msg string, plan ...Plan) chat.McpResult {
	p.recordOrTool(call, plan, "deny")
	return chat.McpResult{CallID: call.CallID, Status: "denied", Error: &chat.FrameError{Code: code, Message: msg}}
}

func (p *Pipeline) fail(call chat.McpCall, code, msg string, plan ...Plan) chat.McpResult {
	p.recordOrTool(call, plan, "error")
	return chat.McpResult{CallID: call.CallID, Status: "error", Error: &chat.FrameError{Code: code, Message: msg}}
}

func (p *Pipeline) record(plan Plan, decision string) {
	if p.Audit == nil {
		return
	}
	p.auditErr(p.Audit.Record(AuditEntry{Tool: plan.Tool, Display: plan.Display, Decision: decision, ConversationID: p.ConversationID}))
}

// recordOrTool audits a rejection. Before Plan succeeds there is no canonical
// Display to log, so the raw (server-supplied) tool name is all the record can
// carry — deliberately not the raw params, which are unvalidated at that point.
func (p *Pipeline) recordOrTool(call chat.McpCall, plan []Plan, decision string) {
	if len(plan) == 1 {
		p.record(plan[0], decision)
		return
	}
	if p.Audit != nil {
		p.auditErr(p.Audit.Record(AuditEntry{Tool: truncateToolName(call.ToolName), Decision: decision, ConversationID: p.ConversationID}))
	}
}

// auditErr routes a failed audit write to the caller. Nil-guarded on both sides:
// no error is the normal case, and no callback is a valid configuration.
func (p *Pipeline) auditErr(err error) {
	if err != nil && p.OnAuditError != nil {
		p.OnAuditError(err)
	}
}

// maxAuditToolName bounds the one field the audit log copies verbatim from the
// server. 64 bytes is far past any real tool name and far short of anything that
// matters on disk.
const maxAuditToolName = 64

// truncateToolName caps a rejected call's server-supplied tool name. It is the
// only untrusted string that reaches the log unrendered (a rejection has no Plan
// yet, so there is no canonical Display to use instead), and it is bounded only
// by the 8MB inbound frame limit — which would let a hostile server append
// megabytes to chat-audit.jsonl per rejected call, growing a local file it can
// otherwise never touch.
//
// The cut is rune-boundary-safe: a name truncated mid-rune would be re-encoded
// by the JSON marshaller as U+FFFD, making the log say something the server did
// not.
func truncateToolName(name string) string {
	if len(name) <= maxAuditToolName {
		return name
	}
	// name[cut] is the first EXCLUDED byte; if it continues a rune, the cut
	// split one, so walk back to that rune's lead byte and drop it whole.
	cut := maxAuditToolName
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return name[:cut]
}
