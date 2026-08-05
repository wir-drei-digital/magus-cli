package localtool

import (
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/config"
)

func TestPolicyDecide(t *testing.T) {
	perms := config.Permissions{Read: "prompt", Write: "deny", Exec: "deny"}
	p := NewPolicy(perms)

	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/a/b"}); d != DecisionPrompt {
		t.Errorf("read default should prompt, got %v", d)
	}
	if d := p.Decide(Plan{Tool: "write_file", Tier: "write", MatchPath: "/a/b"}); d != DecisionDeny {
		t.Errorf("write default should deny, got %v", d)
	}
}

func TestPolicyAllowRuleMatchesPrefix(t *testing.T) {
	perms := config.Permissions{
		Read:  "prompt",
		Allow: []config.AllowRule{{Tool: "read_file", PathPrefix: "/Users/me/proj"}},
	}
	p := NewPolicy(perms)

	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/Users/me/proj/sub/x.txt"}); d != DecisionAllow {
		t.Errorf("path under allow prefix should allow, got %v", d)
	}
	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/etc/passwd"}); d != DecisionPrompt {
		t.Errorf("path outside allow prefix should fall back to prompt, got %v", d)
	}
}

func TestPolicyAddAllow(t *testing.T) {
	p := NewPolicy(config.Permissions{Read: "prompt"})
	p.AddAllow(Plan{Tool: "read_file", Tier: "read", MatchPath: "/Users/me/proj/x.txt"})

	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/Users/me/proj/x.txt"}); d != DecisionAllow {
		t.Errorf("after AddAllow the exact path should allow, got %v", d)
	}
	if len(p.Permissions().Allow) != 1 {
		t.Errorf("expected one persisted allow rule")
	}
}
