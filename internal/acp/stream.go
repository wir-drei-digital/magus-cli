// internal/acp/stream.go
package acp

import (
	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// MapStream converts a chat_stream event into an ACP session update.
// ok=false means "emit nothing" (e.g. text.done, unmapped events).
func MapStream(cs chat.ChatStream) (sdk.SessionUpdate, bool) {
	switch cs.Event {
	case "text.delta":
		if d := str(cs.Data["delta"]); d != "" {
			return sdk.UpdateAgentMessageText(d), true
		}
		return sdk.SessionUpdate{}, false
	case "tool.start":
		id := sdk.ToolCallId(str(cs.Data["event_id"]))
		return sdk.StartToolCall(id, toolTitle(cs.Data), sdk.WithStartStatus(sdk.ToolCallStatusInProgress)), true
	case "tool.complete":
		id := sdk.ToolCallId(str(cs.Data["event_id"]))
		return sdk.UpdateToolCall(id, sdk.WithUpdateStatus(toolStatus(cs.Data))), true
	default:
		return sdk.SessionUpdate{}, false
	}
}

// TurnEnd reports whether the event ends the prompt turn, with any error message.
func TurnEnd(cs chat.ChatStream) (bool, string) {
	switch cs.Event {
	case "turn.done":
		return true, ""
	case "error":
		return true, str(cs.Data["message"])
	default:
		return false, ""
	}
}

func toolTitle(data map[string]any) string {
	if name := str(data["tool_name"]); name != "" {
		return name
	}
	return "tool"
}

func toolStatus(data map[string]any) sdk.ToolCallStatus {
	switch str(data["status"]) {
	case "error", "failed":
		return sdk.ToolCallStatusFailed
	default:
		return sdk.ToolCallStatusCompleted
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
