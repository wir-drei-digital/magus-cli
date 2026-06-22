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
	if _, ok := MapStream(chat.ChatStream{Event: "tool.start", Data: map[string]any{"event_id": "e1", "tool_name": "read_file"}}); !ok {
		t.Error("tool.start should map")
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
