// internal/acp/agent_test.go
package acp

import (
	"context"
	"errors"
	"testing"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

func TestInitializeReportsVersionAndName(t *testing.T) {
	a := New("tok", "https://magus.digital", "magus-cli/test")
	resp, err := a.Initialize(context.Background(), sdk.InitializeRequest{ProtocolVersion: sdk.ProtocolVersion(sdk.ProtocolVersionNumber)})
	if err != nil {
		t.Fatal(err)
	}
	if int(resp.ProtocolVersion) != sdk.ProtocolVersionNumber {
		t.Errorf("protocol version = %d", resp.ProtocolVersion)
	}
	if resp.AgentInfo == nil || resp.AgentInfo.Name != "magus" {
		t.Errorf("expected agent name magus, got %+v", resp.AgentInfo)
	}
	if resp.AgentCapabilities.SessionCapabilities.Close == nil {
		t.Error("agent must advertise sessionCapabilities.close (CloseSession is implemented)")
	}
}

func TestInitializeNegotiatesUnsupportedVersionDown(t *testing.T) {
	// ACP negotiation: if the agent does not support the client's requested
	// version, it must answer with its own latest supported version (the
	// client then decides whether to proceed). We support exactly version 1,
	// so any request — older or newer — must be answered with 1.
	a := New("tok", "https://magus.digital", "ua")
	for _, requested := range []int{0, sdk.ProtocolVersionNumber, 99} {
		resp, err := a.Initialize(context.Background(), sdk.InitializeRequest{ProtocolVersion: sdk.ProtocolVersion(requested)})
		if err != nil {
			t.Fatalf("Initialize(v=%d): %v", requested, err)
		}
		if int(resp.ProtocolVersion) != sdk.ProtocolVersionNumber {
			t.Errorf("Initialize(v=%d) answered %d, want %d", requested, resp.ProtocolVersion, sdk.ProtocolVersionNumber)
		}
	}
}

func TestNewSessionWithoutTokenIsAuthRequired(t *testing.T) {
	a := New("", "https://magus.digital", "ua")
	_, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp"})
	if err == nil {
		t.Fatal("expected an auth-required error without a token")
	}
}

func TestNewSessionDialsAndMapsConversation(t *testing.T) {
	cloud := newFakeCloud()
	a := New("tok", "https://magus.digital", "ua")
	a.SetEditor(&fakeEditor{})
	a.Dial = func(_ context.Context, _, _, _ string) (CloudConn, error) { return cloud, nil }

	// Editor advertises fs.readTextFile at Initialize, so read_file is offered.
	_, _ = a.Initialize(context.Background(), sdk.InitializeRequest{
		ClientCapabilities: sdk.ClientCapabilities{Fs: sdk.FileSystemCapabilities{ReadTextFile: true}},
	})

	// Feed the server_hello the NewSession handshake awaits.
	cloud.events <- chat.Event{Kind: chat.KindServerHello, ChatStream: chat.ChatStream{}, ServerHello: chat.ServerHello{ConversationID: "conv-xyz", AcceptedTools: []string{"read_file"}}}

	resp, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if string(resp.SessionId) != "conv-xyz" {
		t.Errorf("sessionId = %q, want conv-xyz", resp.SessionId)
	}

	// A hello frame must have been sent advertising read_file.
	select {
	case f := <-cloud.sent:
		h, ok := f.(chat.Hello)
		if !ok || len(h.Capabilities.LocalTools) != 1 || h.Capabilities.LocalTools[0] != "read_file" {
			t.Fatalf("expected hello advertising read_file, got %+v", f)
		}
	default:
		t.Fatal("no hello frame sent")
	}
}

func TestNewSessionWithoutFsAdvertisesNoLocalTools(t *testing.T) {
	cloud := newFakeCloud()
	a := New("tok", "https://magus.digital", "ua")
	a.SetEditor(&fakeEditor{})
	a.Dial = func(_ context.Context, _, _, _ string) (CloudConn, error) { return cloud, nil }

	// No Initialize (or fs.readTextFile=false): the bridge must NOT advertise read_file.
	cloud.events <- chat.Event{Kind: chat.KindServerHello, ServerHello: chat.ServerHello{ConversationID: "conv-1"}}

	if _, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	select {
	case f := <-cloud.sent:
		h, ok := f.(chat.Hello)
		if !ok {
			t.Fatalf("expected hello frame, got %+v", f)
		}
		if len(h.Capabilities.LocalTools) != 0 {
			t.Errorf("expected no local tools when editor lacks fs, got %v", h.Capabilities.LocalTools)
		}
	default:
		t.Fatal("no hello frame sent")
	}
}

func TestNewSessionDialErrorPropagates(t *testing.T) {
	a := New("tok", "https://magus.digital", "ua")
	a.SetEditor(&fakeEditor{})
	a.Dial = func(_ context.Context, _, _, _ string) (CloudConn, error) { return nil, errors.New("dial boom") }
	if _, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp"}); err == nil {
		t.Fatal("expected dial error to propagate")
	}
}

func TestNewSessionTimesOutWaitingForServerHello(t *testing.T) {
	// Cloud completes the dial but never sends server_hello and never closes;
	// NewSession must fail promptly via the bounded handshake timeout rather
	// than blocking forever on the events channel.
	orig := handshakeTimeout
	handshakeTimeout = 20 * time.Millisecond
	defer func() { handshakeTimeout = orig }()

	cloud := newFakeCloud() // events channel is empty and stays open
	a := New("tok", "https://magus.digital", "ua")
	a.SetEditor(&fakeEditor{})
	a.Dial = func(_ context.Context, _, _, _ string) (CloudConn, error) { return cloud, nil }

	done := make(chan error, 1)
	go func() {
		_, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp"})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error when server_hello is never sent")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("NewSession hung waiting for server_hello")
	}
}

func TestNewSessionEmptyConversationIDIsError(t *testing.T) {
	cloud := newFakeCloud()
	a := New("tok", "https://magus.digital", "ua")
	a.SetEditor(&fakeEditor{})
	a.Dial = func(_ context.Context, _, _, _ string) (CloudConn, error) { return cloud, nil }

	// server_hello with an empty conversation id must be rejected.
	cloud.events <- chat.Event{Kind: chat.KindServerHello, ServerHello: chat.ServerHello{ConversationID: ""}}

	if _, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp"}); err == nil {
		t.Fatal("expected an error when server_hello carries an empty conversation_id")
	}
}

func TestPromptCancelledReturnsStopReasonCancelled(t *testing.T) {
	a := New("tok", "https://magus.digital", "ua")
	cloud := newFakeCloud()
	sess := &Session{ID: "c1", ChatSID: "s1", Cloud: cloud, Editor: &fakeEditor{}, Ctx: context.Background()}
	go sess.Run()
	a.mu.Lock()
	a.sessions["c1"] = sess
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	type res struct {
		resp sdk.PromptResponse
		err  error
	}
	got := make(chan res, 1)
	go func() {
		resp, err := a.Prompt(ctx, sdk.PromptRequest{SessionId: "c1", Prompt: []sdk.ContentBlock{sdk.TextBlock("hi")}})
		got <- res{resp, err}
	}()

	// Wait for the chat frame (turn in flight), then cancel.
	select {
	case <-cloud.sent:
	case <-time.After(time.Second):
		t.Fatal("no chat frame sent")
	}
	cancel()

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("cancel should resolve cleanly, got error %v", r.err)
		}
		if r.resp.StopReason != sdk.StopReasonCancelled {
			t.Errorf("stopReason = %q, want %q", r.resp.StopReason, sdk.StopReasonCancelled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return on cancel")
	}
}

func TestCloseSessionDisposesAndFreesCloud(t *testing.T) {
	cloud := newFakeCloud()
	a := New("tok", "https://magus.digital", "ua")
	a.SetEditor(&fakeEditor{})
	a.Dial = func(_ context.Context, _, _, _ string) (CloudConn, error) { return cloud, nil }
	cloud.events <- chat.Event{Kind: chat.KindServerHello, ServerHello: chat.ServerHello{ConversationID: "conv-close"}}

	ns, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := a.CloseSession(context.Background(), sdk.CloseSessionRequest{SessionId: ns.SessionId}); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	// The session is gone: a prompt for it must be rejected.
	if _, err := a.Prompt(context.Background(), sdk.PromptRequest{SessionId: ns.SessionId, Prompt: []sdk.ContentBlock{sdk.TextBlock("hi")}}); err == nil {
		t.Error("Prompt after CloseSession must fail with unknown session")
	}

	// The cloud connection was closed (fakeCloud.Close closes the events chan),
	// which also ends the Run pump.
	select {
	case _, open := <-cloud.events:
		if open {
			t.Error("expected the cloud events channel to be closed")
		}
	case <-time.After(time.Second):
		t.Error("cloud connection was not closed")
	}

	// Closing an unknown session errors.
	if _, err := a.CloseSession(context.Background(), sdk.CloseSessionRequest{SessionId: "nope"}); err == nil {
		t.Error("CloseSession on an unknown session must error")
	}
}

func TestUnsupportedMethodsReturnMethodNotFound(t *testing.T) {
	a := New("tok", "https://magus.digital", "ua")
	if _, err := a.SetSessionMode(context.Background(), sdk.SetSessionModeRequest{}); err == nil {
		t.Error("SetSessionMode should be unsupported")
	}
	if _, err := a.Authenticate(context.Background(), sdk.AuthenticateRequest{}); err == nil {
		t.Error("Authenticate should be unsupported")
	}
}
