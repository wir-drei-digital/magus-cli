// internal/acp/stream_test.go
package acp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

func TestMapStreamTextDelta(t *testing.T) {
	up, ok := MapStream(chat.ChatStream{Event: "text.delta", Data: map[string]any{"delta": "Hel"}})
	if !ok {
		t.Fatal("expected an update for text.delta")
	}
	// The SDK update is opaque here; assert it round-trips to JSON containing the text.
	if !updateContains(t, up, "Hel") {
		t.Errorf("update should carry the delta text")
	}
}

func TestMapStreamEmptyDeltaSkipped(t *testing.T) {
	if _, ok := MapStream(chat.ChatStream{Event: "text.delta", Data: map[string]any{"delta": ""}}); ok {
		t.Error("empty delta should produce no update")
	}
}

func TestMapStreamToolStartAndComplete(t *testing.T) {
	up, ok := MapStream(chat.ChatStream{Event: "tool.start", Data: map[string]any{"event_id": "e1", "tool_name": "read_file"}})
	if !ok {
		t.Error("tool.start should map")
	}
	// The local read tool carries the ACP read kind for editor rendering.
	if !updateContains(t, up, `"kind":"read"`) {
		t.Error("read_file tool.start should carry kind:read")
	}
	// Other (cloud-side) tools stay generic.
	other, ok := MapStream(chat.ChatStream{Event: "tool.start", Data: map[string]any{"event_id": "e2", "tool_name": "brain_search"}})
	if !ok {
		t.Error("non-read tool.start should still map")
	}
	if updateContains(t, other, `"kind":"read"`) {
		t.Error("non-read tools must not be tagged kind:read")
	}
	if _, ok := MapStream(chat.ChatStream{Event: "tool.complete", Data: map[string]any{"event_id": "e1", "status": "success"}}); !ok {
		t.Error("tool.complete should map")
	}
}

func TestMapStreamUnmapped(t *testing.T) {
	if _, ok := MapStream(chat.ChatStream{Event: "text.done", Data: map[string]any{}}); ok {
		t.Error("text.done should not produce an update")
	}
	if _, ok := MapStream(chat.ChatStream{Event: "thinking.chunk"}); ok {
		t.Error("unknown events should not produce an update")
	}
}

func TestTurnEnd(t *testing.T) {
	if ended, _ := TurnEnd(chat.ChatStream{Event: "turn.done"}); !ended {
		t.Error("turn.done should end the turn")
	}
	ended, msg := TurnEnd(chat.ChatStream{Event: "error", Data: map[string]any{"message": "boom"}})
	if !ended || msg != "boom" {
		t.Errorf("error should end the turn with message, got ended=%v msg=%q", ended, msg)
	}
	// An error event must NEVER yield an empty message — empty is the success
	// sentinel in Session.Prompt, so a malformed cloud error would otherwise
	// surface as a successful end_turn.
	for _, cs := range []chat.ChatStream{
		{Event: "error"},                                        // no data at all
		{Event: "error", Data: map[string]any{}},                // no message key
		{Event: "error", Data: map[string]any{"message": ""}},   // empty message
		{Event: "error", Data: map[string]any{"message": 1234}}, // non-string message
	} {
		if ended, msg := TurnEnd(cs); !ended || msg == "" {
			t.Errorf("TurnEnd(%+v) = (%v, %q), want (true, non-empty)", cs, ended, msg)
		}
	}
	if ended, _ := TurnEnd(chat.ChatStream{Event: "text.delta"}); ended {
		t.Error("text.delta should not end the turn")
	}
}

// updateContains marshals a SessionUpdate and checks for substr (test helper).
func updateContains(t *testing.T, up any, substr string) bool {
	t.Helper()
	b, err := jsonMarshal(up)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	return contains(string(b), substr)
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
func contains(s, sub string) bool       { return strings.Contains(s, sub) }
