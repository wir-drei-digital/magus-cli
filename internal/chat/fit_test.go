// internal/chat/fit_test.go
package chat

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func encodedLen(t *testing.T, res McpResult) int {
	t.Helper()
	data, err := Encode(res)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return len(data)
}

func TestFitMcpResultLeavesSmallResultAlone(t *testing.T) {
	res := McpResult{CallID: "c1", Status: "ok", Result: map[string]any{"content": "hello", "path": "a.txt"}}
	got := FitMcpResult(res)

	if got.Status != "ok" || got.Result["content"] != "hello" || got.Result["path"] != "a.txt" {
		t.Fatalf("small result was modified: %+v", got)
	}
	if _, ok := got.Result["truncated"]; ok {
		t.Errorf("truncated must be absent when nothing was cut: %+v", got.Result)
	}
}

func TestFitMcpResultTruncatesLongContent(t *testing.T) {
	content := strings.Repeat("a", 900*1024)
	got := FitMcpResult(McpResult{CallID: "c1", Status: "ok", Result: map[string]any{"content": content}})

	if n := encodedLen(t, got); n > maxResultFrameBytes {
		t.Fatalf("encoded frame is %d bytes, budget is %d", n, maxResultFrameBytes)
	}
	if got.Status != "ok" {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if got.Result["truncated"] != true {
		t.Errorf("truncated = %v, want true", got.Result["truncated"])
	}
	out, ok := got.Result["content"].(string)
	if !ok {
		t.Fatalf("content is not a string: %T", got.Result["content"])
	}
	if !strings.HasPrefix(content, out) {
		t.Error("truncated content is not a prefix of the original")
	}
	// Cheap-to-escape content should keep nearly the whole budget, not be
	// thrown away wholesale.
	if len(out) < maxResultFrameBytes/2 {
		t.Errorf("kept only %d of %d content bytes; the budget was barely used", len(out), len(content))
	}
}

func TestFitMcpResultHandlesEscapeHeavyContent(t *testing.T) {
	// Every ESC byte JSON-encodes as the 6-byte : 300KiB of raw content
	// becomes 1.8MB on the wire. This is the case that closes the connection.
	content := strings.Repeat("\x1b", 300*1024)
	res := McpResult{CallID: "c1", Status: "ok", Result: map[string]any{"content": content}}
	if encodedLen(t, res) <= maxResultFrameBytes {
		t.Fatal("test premise broken: the escape-heavy frame already fits")
	}

	got := FitMcpResult(res)
	if n := encodedLen(t, got); n > maxResultFrameBytes {
		t.Fatalf("encoded frame is %d bytes, budget is %d", n, maxResultFrameBytes)
	}
	if got.Status != "ok" {
		t.Fatalf("status = %q, want ok (content is shrinkable)", got.Status)
	}
	if got.Result["truncated"] != true {
		t.Errorf("truncated = %v, want true", got.Result["truncated"])
	}
	out, _ := got.Result["content"].(string)
	if len(out) < 100*1024 {
		t.Fatalf("kept only %d content bytes; ~128KiB of ESC fits the budget", len(out))
	}
	if !strings.HasPrefix(content, out) {
		t.Error("truncated content is not a prefix of the original")
	}
}

func TestFitMcpResultCutsAtRuneBoundary(t *testing.T) {
	// Multi-byte runes: the cut must never leave half a rune behind (json would
	// swap it for U+FFFD and the model would see a corrupted last character).
	content := strings.Repeat("héllo wörld — ✓ ", 80*1024)
	got := FitMcpResult(McpResult{CallID: "c1", Status: "ok", Result: map[string]any{"content": content}})

	out, _ := got.Result["content"].(string)
	if out == "" {
		t.Fatal("all content was dropped")
	}
	if !utf8.ValidString(out) {
		t.Error("truncated content is not valid UTF-8")
	}
	if !strings.HasPrefix(content, out) {
		t.Error("truncated content is not a prefix of the original")
	}
	if n := encodedLen(t, got); n > maxResultFrameBytes {
		t.Fatalf("encoded frame is %d bytes, budget is %d", n, maxResultFrameBytes)
	}
}

func TestFitMcpResultSurvivesInvalidUTF8Content(t *testing.T) {
	// A binary file read as a string is invalid UTF-8 from end to end; trimming
	// must neither panic nor require overall validity.
	content := strings.Repeat("\xff\xfe\x00\x80", 300*1024)
	got := FitMcpResult(McpResult{CallID: "c1", Status: "ok", Result: map[string]any{"content": content}})

	if n := encodedLen(t, got); n > maxResultFrameBytes {
		t.Fatalf("encoded frame is %d bytes, budget is %d", n, maxResultFrameBytes)
	}
	if got.Status == "ok" {
		if out, _ := got.Result["content"].(string); !strings.HasPrefix(content, out) {
			t.Error("truncated content is not a prefix of the original")
		}
	}
}

func TestFitMcpResultFailsClosedWithoutShrinkableContent(t *testing.T) {
	cases := []struct {
		name   string
		result map[string]any
	}{
		{"no content key", map[string]any{"rows": strings.Repeat("x", 900*1024)}},
		{"content is not a string", map[string]any{"content": 42, "blob": strings.Repeat("x", 900*1024)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FitMcpResult(McpResult{CallID: "c1", Status: "ok", Result: tc.result})
			if got.Status != "error" {
				t.Fatalf("status = %q, want error: %+v", got.Status, got)
			}
			if got.Error == nil || got.Error.Code != "oversized_result" {
				t.Fatalf("error = %+v, want code oversized_result", got.Error)
			}
			if got.CallID != "c1" {
				t.Errorf("call id = %q, want c1", got.CallID)
			}
			if n := encodedLen(t, got); n > maxResultFrameBytes {
				t.Errorf("error frame is %d bytes, budget is %d", n, maxResultFrameBytes)
			}
		})
	}
}

func TestFitMcpResultDoesNotMutateCaller(t *testing.T) {
	content := strings.Repeat("a", 900*1024)
	in := map[string]any{"content": content}
	res := McpResult{CallID: "c1", Status: "ok", Result: in}

	_ = FitMcpResult(res)

	if _, ok := in["truncated"]; ok {
		t.Error("FitMcpResult added truncated to the caller's map")
	}
	if in["content"].(string) != content {
		t.Error("FitMcpResult replaced the caller's content")
	}
}
