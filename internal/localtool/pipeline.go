package localtool

import "github.com/wir-drei-digital/magus-cli/internal/chat"

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
	_ = p.Audit.Record(AuditEntry{Tool: plan.Tool, Display: plan.Display, Decision: decision, ConversationID: p.ConversationID})
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
		_ = p.Audit.Record(AuditEntry{Tool: call.ToolName, Decision: decision, ConversationID: p.ConversationID})
	}
}
