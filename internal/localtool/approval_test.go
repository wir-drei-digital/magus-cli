package localtool

import (
	"bufio"
	"bytes"
	"errors"
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
	// Input with no trailing newline: ReadString reports io.EOF alongside the
	// partial line. That is a normal end of input, not a read failure — the
	// helper Fatalfs on a non-nil error, so this pins the io.EOF branch.
	if d, _ := approveWith(t, ""); d != DecisionDeny {
		t.Errorf("EOF with no input should deny, got %v", d)
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

var errWrite = errors.New("terminal gone")

// failWriter fails every write, like a closed or broken stdout.
type failWriter struct{}

func (failWriter) Write(p []byte) (int, error) { return 0, errWrite }

// If the prompt cannot be displayed, there is no informed consent to be had:
// approving an action the user never saw would make the anti-spoofing
// invariant vacuous. A write failure must deny and surface the error.
func TestApproverDeniesWhenPromptCannotBeWritten(t *testing.T) {
	a := &TerminalApprover{In: bufio.NewReader(strings.NewReader("a\n")), Out: failWriter{}}
	d, err := a.Approve(Plan{Tool: "read_file", Tier: "read", Display: "read_file: /Users/me/proj/secret.txt"})
	if d != DecisionDeny {
		t.Errorf("write failure must deny, got %v", d)
	}
	if !errors.Is(err, errWrite) {
		t.Errorf("want the write error, got %v", err)
	}
}

// A buffered Out must not leave the prompt sitting in the buffer while Approve
// blocks on the read: the user would see nothing and a scripted answer would be
// honoured against an unseen prompt.
func TestApproverFlushesBufferedOut(t *testing.T) {
	var sink bytes.Buffer
	out := bufio.NewWriter(&sink)
	a := &TerminalApprover{In: bufio.NewReader(strings.NewReader("d\n")), Out: out}
	if _, err := a.Approve(Plan{Tool: "read_file", Tier: "read", Display: "read_file: /Users/me/proj/secret.txt"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !strings.Contains(sink.String(), "/Users/me/proj/secret.txt") {
		t.Errorf("prompt must reach the underlying writer before Approve returns, got %q", sink.String())
	}
	if !strings.Contains(sink.String(), "deny (default)") {
		t.Errorf("answer line must reach the underlying writer too, got %q", sink.String())
	}
}

// A flush failure is a display failure: same fail-closed handling as a write.
func TestApproverDeniesWhenFlushFails(t *testing.T) {
	out := bufio.NewWriter(failWriter{})
	a := &TerminalApprover{In: bufio.NewReader(strings.NewReader("a\n")), Out: out}
	d, err := a.Approve(Plan{Tool: "read_file", Tier: "read", Display: "read_file: /Users/me/proj/secret.txt"})
	if d != DecisionDeny {
		t.Errorf("flush failure must deny, got %v", d)
	}
	if !errors.Is(err, errWrite) {
		t.Errorf("want the flush error, got %v", err)
	}
}
