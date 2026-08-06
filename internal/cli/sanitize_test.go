package cli

import (
	"strings"
	"testing"
)

// A hostile server's whole point is to repaint the terminal: clear the screen,
// home the cursor, and draw a byte-identical copy of the approval prompt. Every
// byte that can drive a terminal has to leave sanitizeStream as inert text.
func TestSanitizeStreamNeutralisesTerminalControl(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"clear screen + home", "\x1b[2J\x1b[H", "\\x1b[2J\\x1b[H"},
		{"carriage return rewinds the line", "typed\rfake", "typed\\x0dfake"},
		{"bare escape", "\x1b", "\\x1b"},
		{"OSC introducer", "\x1b]0;title\x07", "\\x1b]0;title\\x07"},
		{"DEL", "a\x7fb", "a\\x7fb"},
		{"NUL", "a\x00b", "a\\x00b"},
		{"backspace", "a\bb", "a\\x08b"},
		// U+009B is CSI: on a UTF-8 terminal its two encoded bytes drive the
		// same cursor movement ESC-[ does, so surviving JSON decoding as a
		// well-formed rune is not the same as being harmless.
		{"encoded C1 CSI", "2J", "\\u009b2J"},
		{"encoded C1 range starts at U+0080", "", "\\u0080"},
		{"encoded C1 range ends at U+009F", "", "\\u009f"},
		// Raw C1 bytes are not valid UTF-8 on their own, but a terminal reading
		// bytes still sees them; they must not survive either.
		{"raw C1 byte", "a\x9bb", "a\\x9bb"},
		{"lone UTF-8 lead byte", "a\xc2", "a\\xc2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeStream(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizeStream(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := 0; i < len(got); i++ {
				if c := got[i]; c < 0x20 || c >= 0x7f {
					t.Errorf("output byte %d is still non-printable ASCII (%#02x): %q", i, c, got)
				}
			}
		})
	}
}

// The sanitizer sits on the assistant's reply stream, so ordinary prose — in any
// language, with emoji — has to come out exactly as it went in. Anything less
// and the fix would be paid for with mangled output on every turn.
func TestSanitizeStreamPassesNormalTextThrough(t *testing.T) {
	for _, s := range []string{
		"",
		"plain ascii",
		"Grüße aus München",
		"日本語のテキストです",
		"emoji: \U0001f389\U0001f680 and a ZWJ family \U0001f468‍\U0001f469‍\U0001f467",
		"markdown **bold** `code`\nsecond line\twith a tab\n",
		"math: ∑ ∫ √ · × ÷ — – …",
		"U+00A0 ( ) is one past the C1 range and must survive",
	} {
		if got := sanitizeStream(s); got != s {
			t.Errorf("sanitizeStream(%q) mangled normal text: got %q", s, got)
		}
	}
}

// Newline and tab are the two control bytes the stream is allowed to keep: the
// reply is multi-line prose and stripping them would run it all together.
func TestSanitizeStreamKeepsNewlineAndTab(t *testing.T) {
	const in = "line one\nline two\n\tindented\n"
	if got := sanitizeStream(in); got != in {
		t.Fatalf("sanitizeStream(%q) = %q, want it unchanged", in, got)
	}
}

// text.delta arrives in arbitrarily-sized chunks, so the sanitizer is applied
// per chunk with no carry-over state. A C1 control split across a chunk
// boundary must not reassemble into a live control character in the terminal:
// each half is independently inert, so the concatenation is inert too.
func TestSanitizeStreamIsSafePerChunk(t *testing.T) {
	// "\xc2\x9b" is U+009B (CSI) encoded; the server splits it across deltas.
	chunks := []string{"before\xc2", "\x9b2Jafter"}
	var joined strings.Builder
	for _, c := range chunks {
		joined.WriteString(sanitizeStream(c))
	}
	got := joined.String()
	if strings.Contains(got, "") || strings.Contains(got, "\x9b") {
		t.Fatalf("split C1 reassembled into a live control: %q", got)
	}
	if got != "before\\xc2\\x9b2Jafter" {
		t.Fatalf("got %q", got)
	}
}
