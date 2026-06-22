// internal/acp/session_test.go
package acp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// fakeCloud feeds scripted events and records sent frames.
type fakeCloud struct {
	events chan chat.Event
	sent   chan any
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{events: make(chan chat.Event, 16), sent: make(chan any, 16)}
}
func (f *fakeCloud) Send(frame any) error      { f.sent <- frame; return nil }
func (f *fakeCloud) Events() <-chan chat.Event { return f.events }
func (f *fakeCloud) Close()                    { close(f.events) }

func TestSessionPromptStreamsAndCompletes(t *testing.T) {
	cloud := newFakeCloud()
	ed := &fakeEditor{}
	s := &Session{ID: "conv1", ChatSID: "sid1", Cloud: cloud, Editor: ed, Ctx: context.Background()}
	go s.Run()

	done := make(chan string, 1)
	go func() {
		sr, _ := s.Prompt("hello")
		done <- sr
	}()

	// The prompt should have sent a chat frame.
	select {
	case f := <-cloud.sent:
		c, ok := f.(chat.Chat)
		if !ok || c.Text != "hello" || c.SessionID != "sid1" {
			t.Fatalf("expected chat frame, got %+v", f)
		}
	case <-time.After(time.Second):
		t.Fatal("no chat frame sent")
	}

	// Stream a text delta then end the turn.
	cloud.events <- chat.Event{Kind: chat.KindChatStream, ChatStream: chat.ChatStream{Event: "text.delta", Data: map[string]any{"delta": "Hi"}}}
	cloud.events <- chat.Event{Kind: chat.KindChatStream, ChatStream: chat.ChatStream{Event: "turn.done"}}

	select {
	case sr := <-done:
		if sr != "end_turn" {
			t.Errorf("stopReason = %q, want end_turn", sr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt never completed")
	}

	if len(ed.updates) == 0 {
		t.Error("expected at least one session update forwarded to the editor")
	}
}

func TestSessionPromptRejectsReentrantCall(t *testing.T) {
	cloud := newFakeCloud()
	ed := &fakeEditor{}
	s := &Session{ID: "conv1", ChatSID: "sid1", Cloud: cloud, Editor: ed, Ctx: context.Background()}
	go s.Run()

	// First prompt: no turn.done is ever sent, so it stays in flight.
	go func() { _, _ = s.Prompt("first") }()

	// Wait for the first prompt's chat frame so we know its turnDone is set.
	select {
	case <-cloud.sent:
	case <-time.After(time.Second):
		t.Fatal("first prompt never sent a chat frame")
	}

	// Second prompt must be rejected promptly while the first is in flight.
	type result struct {
		sr  string
		err error
	}
	got := make(chan result, 1)
	go func() {
		sr, err := s.Prompt("second")
		got <- result{sr, err}
	}()

	select {
	case r := <-got:
		if r.err == nil {
			t.Fatalf("second prompt should error while one is in flight, got sr=%q err=nil", r.sr)
		}
		if !strings.Contains(r.err.Error(), "already in flight") {
			t.Errorf("error = %v, want it to mention \"already in flight\"", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("re-entrant prompt did not return promptly")
	}
}

func TestSessionDispatchesMcpCallToExecutor(t *testing.T) {
	cloud := newFakeCloud()
	ed := &fakeEditor{permOptionID: "allow", fileContent: "data"}
	s := &Session{
		ID: "conv1", ChatSID: "sid1", Cloud: cloud, Editor: ed, Ctx: context.Background(),
		Exec: &Executor{SessionID: "conv1", Editor: ed, Advertised: map[string]bool{"read_file": true}, Ctx: context.Background()},
	}
	go s.Run()

	cloud.events <- chat.Event{Kind: chat.KindMcpCall, McpCall: chat.McpCall{CallID: "c1", ToolName: "read_file", Params: map[string]any{"path": "a.txt"}}}

	select {
	case f := <-cloud.sent:
		res, ok := f.(chat.McpResult)
		if !ok || res.Status != "ok" || res.Result["content"] != "data" {
			t.Fatalf("expected ok mcp_result, got %+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("executor result never sent back to cloud")
	}
}
