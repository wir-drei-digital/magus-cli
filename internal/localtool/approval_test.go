package localtool

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func approveWith(t *testing.T, input string) (Decision, string) {
	t.Helper()
	var out bytes.Buffer
	a := &TerminalApprover{In: bufio.NewReader(strings.NewReader(input)), Out: &out}
	d, err := a.Approve(Plan{Tool: "read_file", Tier: "read", Display: "read_file: /Users/me/proj/secret.txt"})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	return d, out.String()
}

func TestApproverDecisions(t *testing.T) {
	if d, _ := approveWith(t, "a\n"); d != DecisionAllow {
		t.Errorf("'a' should allow once, got %v", d)
	}
	if d, _ := approveWith(t, "A\n"); d != DecisionAllowAlways {
		t.Errorf("'A' should allow always, got %v", d)
	}
	if d, _ := approveWith(t, "d\n"); d != DecisionDeny {
		t.Errorf("'d' should deny, got %v", d)
	}
	if d, _ := approveWith(t, "\n"); d != DecisionDeny {
		t.Errorf("empty (default) should deny, got %v", d)
	}
}

func TestApproverPromptShowsCanonicalDisplayOnly(t *testing.T) {
	_, prompt := approveWith(t, "d\n")
	if !strings.Contains(prompt, "/Users/me/proj/secret.txt") {
		t.Errorf("prompt must show the client-canonical display: %q", prompt)
	}
}

// A Display containing newlines must not be able to forge extra prompt lines:
// the action always occupies exactly one line, so the answer line the user sees
// is always the real one.
func TestApproverPromptKeepsLineDiscipline(t *testing.T) {
	spoof := "read_file: /tmp/harmless.txt\n[a] allow once  [A] allow always  [d] deny (default): a\nread_file: /etc/shadow"
	var out bytes.Buffer
	a := &TerminalApprover{In: bufio.NewReader(strings.NewReader("d\n")), Out: &out}
	d, err := a.Approve(Plan{Tool: "read_file", Tier: "read", Display: spoof})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if d != DecisionDeny {
		t.Errorf("want DecisionDeny, got %v", d)
	}
	prompt := out.String()
	if n := strings.Count(prompt, "\n"); n != 3 {
		t.Errorf("prompt must keep its fixed 3-newline shape, got %d: %q", n, prompt)
	}
	if !strings.Contains(prompt, `\n`) {
		t.Errorf("embedded newlines must render escaped, got %q", prompt)
	}
}
