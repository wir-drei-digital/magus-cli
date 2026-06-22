// internal/acp/agent_test.go
package acp

import (
	"context"
	"errors"
	"testing"

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

func TestNewSessionDialErrorPropagates(t *testing.T) {
	a := New("tok", "https://magus.digital", "ua")
	a.SetEditor(&fakeEditor{})
	a.Dial = func(_ context.Context, _, _, _ string) (CloudConn, error) { return nil, errors.New("dial boom") }
	if _, err := a.NewSession(context.Background(), sdk.NewSessionRequest{Cwd: "/tmp"}); err == nil {
		t.Fatal("expected dial error to propagate")
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
