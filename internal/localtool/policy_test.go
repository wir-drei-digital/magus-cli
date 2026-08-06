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

// An explicit "deny" tier is a kill switch: it must beat any persisted allow
// rule, including one matching the exact path. Rules only ever upgrade
// prompt -> allow, never deny -> allow.
func TestPolicyExplicitDenyOverridesAllowRule(t *testing.T) {
	p := NewPolicy(config.Permissions{
		Read:  "deny",
		Allow: []config.AllowRule{{Tool: "read_file", PathPrefix: "/proj/a.txt"}},
	})

	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/proj/a.txt"}); d != DecisionDeny {
		t.Errorf("explicit deny tier must override a matching allow rule, got %v", d)
	}
}

// A raw strings.HasPrefix would accept both of these. within() must not.
func TestPolicyAllowRuleDoesNotMatchPathSuffixes(t *testing.T) {
	p := NewPolicy(config.Permissions{
		Read:  "prompt",
		Allow: []config.AllowRule{{Tool: "read_file", PathPrefix: "/proj/a.txt"}},
	})

	for _, path := range []string{"/proj/a.txt.bak", "/proj/a.txtsecrets"} {
		if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: path}); d != DecisionPrompt {
			t.Errorf("rule /proj/a.txt must not cover %s, got %v", path, d)
		}
	}
	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/proj/a.txt"}); d != DecisionAllow {
		t.Errorf("the exact ruled path must still allow, got %v", d)
	}
}

// Sibling directories sharing a textual prefix are not "under" the rule.
func TestPolicyAllowRuleDoesNotMatchSiblingPrefix(t *testing.T) {
	p := NewPolicy(config.Permissions{
		Read:  "prompt",
		Allow: []config.AllowRule{{Tool: "read_file", PathPrefix: "/Users/me/proj"}},
	})

	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/Users/me/proj-evil/x.txt"}); d != DecisionPrompt {
		t.Errorf("sibling dir /Users/me/proj-evil must not be covered by rule /Users/me/proj, got %v", d)
	}
}

// AddAllow must persist the exact file path, never its parent directory: a
// per-file approval may not be widened into a whole-directory grant.
func TestPolicyAddAllowDoesNotWidenToDirectory(t *testing.T) {
	p := NewPolicy(config.Permissions{Read: "prompt"})
	p.AddAllow(Plan{Tool: "read_file", Tier: "read", MatchPath: "/proj/x.txt"})

	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/proj/y.txt"}); d != DecisionPrompt {
		t.Errorf("sibling file must still prompt after AddAllow on /proj/x.txt, got %v", d)
	}
	rules := p.Permissions().Allow
	if len(rules) != 1 {
		t.Fatalf("expected exactly one persisted rule, got %d", len(rules))
	}
	if rules[0].PathPrefix != "/proj/x.txt" {
		t.Errorf("rule must persist the exact path, got %q", rules[0].PathPrefix)
	}
	if rules[0].Tool != "read_file" {
		t.Errorf("rule must persist the planned tool, got %q", rules[0].Tool)
	}
}

// A rule grants one tool only; it must not leak across tools on the same path.
func TestPolicyAllowRuleRequiresMatchingTool(t *testing.T) {
	p := NewPolicy(config.Permissions{
		Read:  "prompt",
		Allow: []config.AllowRule{{Tool: "write_file", PathPrefix: "/proj/a.txt"}},
	})

	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/proj/a.txt"}); d != DecisionPrompt {
		t.Errorf("a write_file rule must not authorize read_file, got %v", d)
	}
}

// A malformed/blank persisted rule must never become a blanket grant.
func TestPolicyEmptyPathPrefixNeverMatches(t *testing.T) {
	p := NewPolicy(config.Permissions{
		Read:  "prompt",
		Allow: []config.AllowRule{{Tool: "read_file", PathPrefix: ""}},
	})

	for _, path := range []string{"/etc/passwd", "/proj/a.txt", ""} {
		if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: path}); d != DecisionPrompt {
			t.Errorf("empty PathPrefix must not match %q, got %v", path, d)
		}
	}
}

func TestPolicyTierDefaults(t *testing.T) {
	tests := []struct {
		name  string
		perms config.Permissions
		tier  string
		want  Decision
	}{
		{"read allow", config.Permissions{Read: "allow"}, "read", DecisionAllow},
		{"write allow", config.Permissions{Write: "allow"}, "write", DecisionAllow},
		{"exec allow", config.Permissions{Exec: "allow"}, "exec", DecisionAllow},
		{"exec deny", config.Permissions{Exec: "deny"}, "exec", DecisionDeny},
		{"unknown tier falls back to prompt", config.Permissions{Read: "allow"}, "network", DecisionPrompt},
		{"empty tier falls back to prompt", config.Permissions{Read: "allow"}, "", DecisionPrompt},
		{"unset tier value prompts", config.Permissions{}, "read", DecisionPrompt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPolicy(tc.perms)
			if d := p.Decide(Plan{Tool: "read_file", Tier: tc.tier, MatchPath: "/a/b"}); d != tc.want {
				t.Errorf("tier %q: want %v, got %v", tc.tier, tc.want, d)
			}
		})
	}
}

// The zero value must be the fail-safe outcome, so a Decision that was never
// assigned can only ever mean "ask the user".
func TestPolicyZeroValueDecisionIsPrompt(t *testing.T) {
	var d Decision
	if d != DecisionPrompt {
		t.Errorf("zero-value Decision must be DecisionPrompt, got %v", d)
	}
	if DecisionPrompt != Decision(0) {
		t.Errorf("DecisionPrompt must be Decision(0), got %v", DecisionPrompt)
	}
}
