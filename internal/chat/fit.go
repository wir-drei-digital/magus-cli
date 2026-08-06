// internal/chat/fit.go
package chat

import "unicode/utf8"

// maxResultFrameBytes bounds the encoded size of an outbound mcp_result frame.
// The server caps inbound frames at 1MB and CLOSES the connection on oversize;
// 768KiB leaves comfortable headroom for the envelope and future fields.
//
// The budget is on the ENCODED size, not the raw file: JSON string escaping
// inflates content up to 6× (every control byte becomes a 6-byte \u00XX), so a
// 256KiB read can reach ~1.5MB on the wire — and the files that escape worst
// are exactly the ones that would otherwise kill the connection.
const maxResultFrameBytes = 768 * 1024

// FitMcpResult returns res with res.Result["content"] truncated (and
// result["truncated"] set) as needed so the encoded frame fits the budget.
// A frame that is oversized without a shrinkable string content becomes a
// fail-closed error result — better a clean tool error than the server closing
// the whole connection.
//
// The caller's Result map is never modified; a truncated result gets a copy.
func FitMcpResult(res McpResult) McpResult {
	content, hasContent := res.Result["content"].(string)

	// An encoding is never smaller than the raw content it carries, so content
	// longer than the budget cannot possibly fit — skip encoding a frame that
	// may be tens of megabytes only to learn that.
	if !hasContent || len(content) <= maxResultFrameBytes {
		data, err := Encode(res)
		if err != nil {
			// Unencodable payload: leave it alone and let Send surface the
			// marshal error rather than inventing a size verdict.
			return res
		}
		if len(data) <= maxResultFrameBytes {
			return res
		}
	}
	if !hasContent {
		return oversizedResult(res.CallID)
	}
	if len(content) > maxResultFrameBytes {
		content = content[:maxResultFrameBytes]
	}

	// Binary search for the longest content prefix that still fits. Encoded size
	// is monotonic in the prefix length (each candidate is cut at a rune
	// boundary, so a shorter candidate's encoding is a prefix of a longer one's),
	// which makes the search exact in ~20 encodes instead of guessing at trim
	// amounts — and it keeps the maximum content the budget allows, where a
	// subtract-the-overage loop would throw the whole file away as soon as the
	// overage exceeded the content length (i.e. on escape-heavy files).
	lo, hi, best := 0, len(content), -1
	for lo <= hi {
		mid := (lo + hi) / 2
		cand := truncatedResult(res, cutAtRuneBoundary(content[:mid]))
		data, err := Encode(cand)
		if err != nil {
			// Same call as the fast path above: an unencodable payload is a
			// marshal bug, not a size verdict — let Send report it as one.
			return res
		}
		if len(data) <= maxResultFrameBytes {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best < 0 {
		// Not even empty content fits: the rest of the result is the problem.
		return oversizedResult(res.CallID)
	}
	return truncatedResult(res, cutAtRuneBoundary(content[:best]))
}

// truncatedResult copies res with content replaced and truncated flagged, so the
// caller's map is left untouched (aliasing here would surprise callers and tests).
func truncatedResult(res McpResult, content string) McpResult {
	out := res
	out.Result = make(map[string]any, len(res.Result)+1)
	for k, v := range res.Result {
		out.Result[k] = v
	}
	out.Result["content"] = content
	out.Result["truncated"] = true
	return out
}

func oversizedResult(callID string) McpResult {
	return McpResult{
		CallID: callID,
		Status: "error",
		Error:  &FrameError{Code: "oversized_result", Message: "tool result exceeds the 1MB frame limit"},
	}
}

// cutAtRuneBoundary drops a trailing partial UTF-8 sequence, so cutting content
// at an arbitrary byte offset never leaves half a rune (json.Marshal would
// replace it with U+FFFD, which both corrupts the last character and INFLATES
// the encoding). It inspects only the tail: content read from a binary file is
// invalid UTF-8 throughout and is otherwise passed through byte for byte.
func cutAtRuneBoundary(s string) string {
	// A rune is at most 4 bytes, so the start of the final sequence is within
	// the last 4 bytes; anything longer is invalid data we leave as it is.
	for i := len(s) - 1; i >= 0 && i >= len(s)-utf8.UTFMax; i-- {
		if !utf8.RuneStart(s[i]) {
			continue
		}
		if r, size := utf8.DecodeRuneInString(s[i:]); r == utf8.RuneError && size <= 1 {
			return s[:i] // incomplete (or invalid) final sequence
		}
		return s
	}
	return s
}
