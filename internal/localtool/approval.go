package localtool

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Approver decides a single prompted tool call.
type Approver interface {
	Approve(plan Plan) (Decision, error)
}

// TerminalApprover prompts on Out and reads a single line from In. It renders
// ONLY plan.Display (the client-canonical action) — never any server-supplied
// description — so what the user approves is exactly what executes.
type TerminalApprover struct {
	In  *bufio.Reader
	Out io.Writer
}

func (a *TerminalApprover) Approve(plan Plan) (Decision, error) {
	fmt.Fprintf(a.Out, "\nThe cloud agent wants to run:\n  %s\n", oneLine(plan.Display))
	fmt.Fprintf(a.Out, "[a] allow once  [A] allow always  [d] deny (default): ")

	line, err := a.In.ReadString('\n')
	if err != nil && err != io.EOF {
		return DecisionDeny, err
	}
	switch strings.TrimSpace(line) {
	case "a":
		return DecisionAllow, nil
	case "A":
		return DecisionAllowAlways, nil
	default:
		return DecisionDeny, nil
	}
}

// oneLine keeps the prompt's line discipline at the render point: a Display
// carrying a newline could otherwise print a fake answer line and leave the
// real question scrolled off, and a CR or ESC could overwrite or repaint what
// was already shown. Anything non-printable (or invalid UTF-8) is therefore
// quoted and escaped, so the action always occupies exactly one line.
//
// Defence in depth, deliberately duplicated from displayPath (readfile.go):
// ReadFile builds its Display from an already-escaped path, but Display is a
// plain string on Plan and a future tool may be less careful. The invariant
// belongs at the point that writes to the terminal, not only at each producer.
func oneLine(s string) string {
	if !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool { return !strconv.IsPrint(r) }) {
		return strconv.Quote(s)
	}
	return s
}
