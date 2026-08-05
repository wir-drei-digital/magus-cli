package localtool

import (
	"bufio"
	"errors"
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
//
// Out contract: the prompt must be on screen before the answer is read, or the
// user would be answering a question they never saw. Approve therefore checks
// every write, and if Out is buffered (anything with a `Flush() error` method,
// e.g. *bufio.Writer) it flushes before reading. An unwritable or unflushable
// Out is a denial, never a silent approval.
type TerminalApprover struct {
	In  *bufio.Reader
	Out io.Writer
}

func (a *TerminalApprover) Approve(plan Plan) (Decision, error) {
	// Fail closed on display errors: a prompt the user never saw cannot be
	// consent, so "could not ask" must never read as "allowed".
	if err := a.prompt(plan); err != nil {
		return DecisionDeny, err
	}

	line, err := a.In.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
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

// prompt renders the question and guarantees it has actually left for the
// terminal before the caller reads the answer.
func (a *TerminalApprover) prompt(plan Plan) error {
	if _, err := fmt.Fprintf(a.Out, "\nThe cloud agent wants to run:\n  %s\n", oneLine(plan.Display)); err != nil {
		return err
	}
	if _, err := fmt.Fprint(a.Out, "[a] allow once  [A] allow always  [d] deny (default): "); err != nil {
		return err
	}
	// A buffered Out (bufio.Writer wrapping os.Stdout) would otherwise hold the
	// whole prompt — the answer line has no trailing newline, so nothing
	// triggers an implicit flush — and the user would stare at a blank screen
	// while Approve blocks on the read.
	if f, ok := a.Out.(interface{ Flush() error }); ok {
		if err := f.Flush(); err != nil {
			return err
		}
	}
	return nil
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
