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

	// OnExit, if set, is called once when Run returns (i.e. the cloud
	// connection closed) so the owner can evict this now-dead session.
	OnExit func()

	mu       sync.Mutex
	turnDone chan turnResult
}

type turnResult struct{ errMsg string }

// Run consumes cloud events until the connection closes: it forwards stream
// updates to the editor, dispatches mcp_call to the executor, and signals turn
// completion to a waiting Prompt. Blocking on the executor (permission round-
// trip) naturally pauses the stream — acceptable for v1's single in-flight tool.
func (s *Session) Run() {
	if s.OnExit != nil {
		defer s.OnExit()
	}
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
			s.signalTurn(turnResult{errMsg: closeMsg(ev)})
		}
	}
	s.signalTurn(turnResult{errMsg: "connection closed"})
}

// closeMsg surfaces the real transport/server error (server_hello rejection,
// protocol error, socket read error) instead of a generic "connection closed".
func closeMsg(ev chat.Event) string {
	if ev.Err != nil {
		return ev.Err.Error()
	}
	return "connection closed"
}

// Prompt sends the user's text as a chat turn and blocks until the turn ends.
// It rejects re-entrant calls while a turn is in flight, and unblocks if ctx is
// cancelled before a terminal event arrives. ctx is the SDK's per-request
// context, which the SDK cancels on session/cancel — so an editor cancel
// returns the prompt. (The cloud turn itself keeps running until it ends
// naturally; interrupting it needs a server-side cancel frame, deferred to v-next.)
func (s *Session) Prompt(ctx context.Context, text string) (string, error) {
	s.mu.Lock()
	if s.turnDone != nil {
		s.mu.Unlock()
		return "", fmt.Errorf("a prompt turn is already in flight")
	}
	ch := make(chan turnResult, 1)
	s.turnDone = ch
	s.mu.Unlock()

	if err := s.Cloud.Send(chat.Chat{SessionID: s.ChatSID, Text: text}); err != nil {
		s.clearTurn()
		return "", err
	}

	select {
	case res := <-ch:
		if res.errMsg != "" {
			return "", fmt.Errorf("turn ended: %s", res.errMsg)
		}
		return "end_turn", nil
	case <-ctx.Done():
		s.clearTurn()
		return "", ctx.Err()
	}
}

// clearTurn resets the in-flight marker so the session can accept a later Prompt.
func (s *Session) clearTurn() {
	s.mu.Lock()
	s.turnDone = nil
	s.mu.Unlock()
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
