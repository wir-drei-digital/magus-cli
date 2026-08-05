package localtool

import (
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

// Decision is the outcome of a policy lookup.
type Decision int

const (
	DecisionPrompt Decision = iota
	DecisionAllow
	DecisionDeny
)

// Policy decides whether a planned tool call is allowed, prompts, or is denied,
// based on tier defaults and persisted "allow always" rules.
type Policy struct {
	perms config.Permissions
}

func NewPolicy(perms config.Permissions) *Policy { return &Policy{perms: perms} }

func (p *Policy) Permissions() config.Permissions { return p.perms }

func (p *Policy) Decide(plan Plan) Decision {
	// An explicit "deny" tier is a kill switch and is checked BEFORE the allow
	// rules: persisted rules only ever upgrade prompt -> allow, never
	// deny -> allow. Otherwise a stale "allow always" from an earlier session
	// would silently defeat a user who has since set the tier to deny.
	if p.tierDefault(plan.Tier) == "deny" {
		return DecisionDeny
	}
	for _, r := range p.perms.Allow {
		// Match on path-segment boundaries, NOT a raw string prefix. A raw
		// strings.HasPrefix lets an "allow always" on /proj/a.txt silently
		// auto-approve /proj/a.txt.bak, /proj/a.txtsecrets, etc. within()
		// (from confine.go) accepts only when MatchPath == prefix or MatchPath
		// is under prefix on a separator boundary.
		if r.Tool == plan.Tool && r.PathPrefix != "" && within(r.PathPrefix, plan.MatchPath) {
			return DecisionAllow
		}
	}
	switch p.tierDefault(plan.Tier) {
	case "allow":
		return DecisionAllow
	case "deny":
		return DecisionDeny
	default:
		return DecisionPrompt
	}
}

// AddAllow persists an "allow always" rule scoped to this exact tool+path.
//
// Decide matches PathPrefix on segment boundaries via within(), and within()
// also matches the equal case (rel == "."). Persisting the exact resolved path
// therefore scopes the rule to that one file: an "allow always" on /proj/a.txt
// matches /proj/a.txt and nothing else — NOT /proj/a.txt.bak, NOT siblings.
// This is the tightest, least-surprising "allow always for THIS file"
// semantics. (A broader "allow this directory" grant would store a parent
// directory as the prefix; we deliberately do not, so a per-file approval is
// never silently widened into a whole-subtree grant.)
func (p *Policy) AddAllow(plan Plan) {
	p.perms.Allow = append(p.perms.Allow, config.AllowRule{Tool: plan.Tool, PathPrefix: plan.MatchPath})
}

func (p *Policy) tierDefault(tier string) string {
	switch tier {
	case "read":
		return p.perms.Read
	case "write":
		return p.perms.Write
	case "exec":
		return p.perms.Exec
	default:
		return ""
	}
}
