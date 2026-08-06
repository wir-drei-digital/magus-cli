package cli

import (
	"strings"
	"unicode/utf8"
)

// sanitizeStream makes server-controlled text safe to write to a terminal.
//
// Everything the cloud sends is attacker-controlled from this side's point of
// view, and a terminal is not a text sink — it is an interpreter. A single
// "\x1b[2J\x1b[H" clears the screen and homes the cursor, after which the server
// can paint a byte-identical copy of the local approval prompt and harvest the
// answer. That is the concrete exploit chain behind the type-ahead residual
// documented at the reader in chat.go, so the escape sequences that make it
// possible are neutralised at every point server text reaches Out.
//
// What survives: '\n' and '\t' (the reply is multi-line prose; stripping them
// would run it together) and every printable rune, byte for byte — emoji, CJK,
// accents and typographic punctuation all pass through untouched.
//
// What does not: ESC and the rest of C0, DEL, and the C1 range U+0080-U+009F.
// C1 matters because a UTF-8 terminal decodes "\xc2\x9b" as CSI and acts on it
// exactly as it would on ESC-[; it reaches here either as that well-formed rune
// (JSON decoding preserves it) or as a raw 0x80-0x9f byte. Both forms, and any
// other invalid UTF-8 byte, are rendered as visible "\xNN"/"\u00NN" text rather
// than dropped: escaping is as inert as stripping and does not silently rewrite
// what the server said.
//
// Chunking: text.delta arrives in server-chosen pieces and this function is
// stateless, so each delta is sanitized independently. A multi-byte rune split
// across two deltas is therefore escaped byte-wise instead of rendered — which
// is the safe direction, and is why a C1 control split across a delta boundary
// cannot reassemble: its raw halves are each escaped where they land. (In
// practice the split cannot happen: deltas arrive as JSON strings, so each one
// is already a complete, valid-UTF-8 value by the time it gets here.)
func sanitizeStream(s string) string {
	if !needsSanitizing(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		if c := s[i]; c < utf8.RuneSelf {
			switch {
			case c == '\n' || c == '\t':
				b.WriteByte(c)
			case c < 0x20 || c == 0x7f:
				writeByteEscape(&b, c)
			default:
				b.WriteByte(c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			// Not valid UTF-8 — a raw C1 byte, a truncated sequence, or junk.
			writeByteEscape(&b, s[i])
			i++
		case r >= 0x80 && r <= 0x9f:
			writeC1Escape(&b, r)
			i += size
		default:
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

// needsSanitizing reports whether s contains anything sanitizeStream would
// change, so the common case (ordinary text) returns the input string itself
// rather than a rebuilt copy. Any non-ASCII byte takes the slow path; the slow
// path still reproduces valid non-C1 runes byte for byte.
func needsSanitizing(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\t' {
			continue
		}
		if c < 0x20 || c >= 0x7f {
			return true
		}
	}
	return false
}

const hexDigits = "0123456789abcdef"

// writeByteEscape renders one byte as the literal text \xNN.
func writeByteEscape(b *strings.Builder, c byte) {
	b.WriteString(`\x`)
	b.WriteByte(hexDigits[c>>4])
	b.WriteByte(hexDigits[c&0xf])
}

// writeC1Escape renders a decoded U+0080-U+009F rune as the literal text \u00NN.
func writeC1Escape(b *strings.Builder, r rune) {
	b.WriteString(`\u00`)
	b.WriteByte(hexDigits[(r>>4)&0xf])
	b.WriteByte(hexDigits[r&0xf])
}
