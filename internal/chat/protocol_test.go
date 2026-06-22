package chat

import (
	"encoding/json"
	"testing"
)

func TestEncodeHello(t *testing.T) {
	data, err := Encode(Hello{
		SessionID:    "s1",
		Capabilities: Capabilities{LocalTools: []string{"read_file"}},
		Conversation: map[string]any{"new": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "hello" || got["v"].(float64) != 1 {
		t.Fatalf("bad envelope: %v", got)
	}
}

func TestDecodeTypeAndMcpCall(t *testing.T) {
	raw := []byte(`{"type":"mcp_call","v":1,"call_id":"c1","tool_name":"read_file","params":{"path":"a.txt"}}`)
	typ, err := DecodeType(raw)
	if err != nil || typ != "mcp_call" {
		t.Fatalf("DecodeType=%q err=%v", typ, err)
	}
	var c McpCall
	if err := decodePayload(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.CallID != "c1" || c.ToolName != "read_file" || c.Params["path"] != "a.txt" {
		t.Fatalf("bad decode: %+v", c)
	}
}

func TestExecutorInterfaceSatisfied(t *testing.T) {
	var _ Executor = stubExec{}
}

type stubExec struct{}

func (stubExec) Handle(call McpCall) McpResult {
	return McpResult{CallID: call.CallID, Status: "ok"}
}
