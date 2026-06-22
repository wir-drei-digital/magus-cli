// internal/acp/session.go
package acp

import (
	"context"
	"fmt"
	"sync"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// Session bridges one ACP session to one cloud Conversation over one WS.
//
//   - ID      is the ACP sessionId (== cloud conversation_id); used for editor calls.
//   - ChatSID is the chat connection session_id (caller id); used for cloud chat frames.
type Session struct {
	ID      string
	ChatSID string
	Cloud   CloudConn
	Editor  EditorConn
	Exec    chat.Executor
	Ctx     context.Context

	mu       sync.Mutex
	turnDone chan turnResult
}

type turnResult struct{ errMsg string }

// Run consumes cloud events until the connection closes: it forwards stream
// updates to the editor, dispatches mcp_call to the executor, and signals turn
// completion to a waiting Prompt. Blocking on the executor (permission round-
// trip) naturally pauses the stream — acceptable for v1's single in-flight tool.
func (s *Session) Run() {
	for ev := range s.Cloud.Events() {
		switch ev.Kind {
		case chat.KindChatStream:
			if up, ok := MapStream(ev.ChatStream); ok {
				_ = s.Editor.SessionUpdate(s.Ctx, sdk.SessionNotification{
					SessionId: sdk.SessionId(s.ID),
					Update:    up,
				})
			}
			if ended, errMsg := TurnEnd(ev.ChatStream); ended {
				s.signalTurn(turnResult{errMsg: errMsg})
			}
		case chat.KindMcpCall:
			if s.Exec != nil {
				_ = s.Cloud.Send(s.Exec.Handle(ev.McpCall))
			}
		case chat.KindClosed, chat.KindError:
			s.signalTurn(turnResult{errMsg: "connection closed"})
		}
	}
	s.signalTurn(turnResult{errMsg: "connection closed"})
}

// Prompt sends the user's text as a chat turn and blocks until the turn ends.
// It rejects re-entrant calls while a turn is in flight, and unblocks if the
// session context is cancelled before a terminal event arrives.
func (s *Session) Prompt(text string) (string, error) {
	s.mu.Lock()
	if s.turnDone != nil {
		s.mu.Unlock()
		return "", fmt.Errorf("a prompt turn is already in flight")
	}
	ch := make(chan turnResult, 1)
	s.turnDone = ch
	s.mu.Unlock()

	if err := s.Cloud.Send(chat.Chat{SessionID: s.ChatSID, Text: text}); err != nil {
		s.mu.Lock()
		s.turnDone = nil
		s.mu.Unlock()
		return "", err
	}

	select {
	case res := <-ch:
		if res.errMsg != "" {
			return "", fmt.Errorf("turn ended: %s", res.errMsg)
		}
		return "end_turn", nil
	case <-s.Ctx.Done():
		s.mu.Lock()
		s.turnDone = nil
		s.mu.Unlock()
		return "", s.Ctx.Err()
	}
}

func (s *Session) signalTurn(r turnResult) {
	s.mu.Lock()
	ch := s.turnDone
	s.turnDone = nil
	s.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}
