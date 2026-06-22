// internal/acp/agent.go
package acp

import (
	"context"
	"fmt"
	"sync"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
)

// handshakeTimeout bounds how long NewSession waits for the cloud's server_hello
// after the WS upgrade completes. Package-level so tests can shorten it.
var handshakeTimeout = 30 * time.Second

// Compile-time assertion that *Agent satisfies the SDK's Agent interface. This
// surfaces any drift in the method set / stub signatures here, rather than only
// when the agent is wired into NewAgentSideConnection (Task 8).
var _ sdk.Agent = (*Agent)(nil)

// Agent implements sdk.Agent: it bridges an ACP editor to the magus cloud agent.
type Agent struct {
	token  string
	apiURL string
	ua     string

	// Dial opens a cloud chat connection; injectable for tests. Defaults to chat.Dial.
	Dial func(ctx context.Context, wsURL, token, ua string) (CloudConn, error)

	mu       sync.Mutex
	editor   EditorConn
	sessions map[string]*Session
}

// New builds an Agent. Call SetEditor after constructing the SDK connection.
func New(token, apiURL, userAgent string) *Agent {
	return &Agent{
		token:    token,
		apiURL:   apiURL,
		ua:       userAgent,
		sessions: map[string]*Session{},
		Dial: func(ctx context.Context, wsURL, token, ua string) (CloudConn, error) {
			return chat.Dial(ctx, wsURL, token, ua)
		},
	}
}

// SetEditor stores the back-reference to the ACP connection (which is created
// from this agent), so sessions can push updates and request permissions.
func (a *Agent) SetEditor(e EditorConn) {
	a.mu.Lock()
	a.editor = e
	a.mu.Unlock()
}

func (a *Agent) Initialize(_ context.Context, req sdk.InitializeRequest) (sdk.InitializeResponse, error) {
	return sdk.InitializeResponse{
		ProtocolVersion:   sdk.ProtocolVersion(sdk.ProtocolVersionNumber),
		AgentInfo:         &sdk.Implementation{Name: "magus"},
		AgentCapabilities: sdk.AgentCapabilities{LoadSession: false},
		AuthMethods:       []sdk.AuthMethod{},
	}, nil
}

func (a *Agent) NewSession(ctx context.Context, req sdk.NewSessionRequest) (sdk.NewSessionResponse, error) {
	if a.token == "" {
		return sdk.NewSessionResponse{}, sdk.NewAuthRequired("no magus token; run `magus login` first")
	}
	wsURL, err := chat.WSURL(a.apiURL)
	if err != nil {
		return sdk.NewSessionResponse{}, err
	}
	cloud, err := a.Dial(ctx, wsURL, a.token, a.ua)
	if err != nil {
		return sdk.NewSessionResponse{}, fmt.Errorf("connect to magus: %w", err)
	}

	chatSID := newID()
	if err := cloud.Send(chat.Hello{
		SessionID:    chatSID,
		Capabilities: chat.Capabilities{LocalTools: []string{"read_file"}},
		Conversation: map[string]any{"new": true},
	}); err != nil {
		cloud.Close()
		return sdk.NewSessionResponse{}, err
	}

	// Await server_hello (first inbound event) to learn the conversation id.
	// Bound the wait: a server that upgrades the WS but never sends server_hello
	// (and never closes) must not hang session creation or leak the connection.
	timer := time.NewTimer(handshakeTimeout)
	defer timer.Stop()

	var ev chat.Event
	select {
	case e, ok := <-cloud.Events():
		if !ok || e.Kind != chat.KindServerHello {
			cloud.Close()
			return sdk.NewSessionResponse{}, fmt.Errorf("did not receive server_hello")
		}
		ev = e
	case <-ctx.Done():
		cloud.Close()
		return sdk.NewSessionResponse{}, fmt.Errorf("awaiting server_hello: %w", ctx.Err())
	case <-timer.C:
		cloud.Close()
		return sdk.NewSessionResponse{}, fmt.Errorf("timed out awaiting server_hello")
	}

	convID := ev.ServerHello.ConversationID
	if convID == "" {
		cloud.Close()
		return sdk.NewSessionResponse{}, fmt.Errorf("server_hello missing conversation_id")
	}

	a.mu.Lock()
	editor := a.editor
	a.mu.Unlock()

	sess := &Session{
		ID:      convID,
		ChatSID: chatSID,
		Cloud:   cloud,
		Editor:  editor,
		Ctx:     context.Background(),
		Exec: &Executor{
			SessionID:  convID,
			Editor:     editor,
			Advertised: map[string]bool{"read_file": true},
			Ctx:        context.Background(),
		},
	}
	a.mu.Lock()
	a.sessions[convID] = sess
	a.mu.Unlock()
	go sess.Run()

	return sdk.NewSessionResponse{SessionId: sdk.SessionId(convID)}, nil
}

func (a *Agent) Prompt(_ context.Context, req sdk.PromptRequest) (sdk.PromptResponse, error) {
	a.mu.Lock()
	sess := a.sessions[string(req.SessionId)]
	a.mu.Unlock()
	if sess == nil {
		return sdk.PromptResponse{}, sdk.NewInvalidParams("unknown session")
	}
	stop, err := sess.Prompt(promptText(req.Prompt))
	if err != nil {
		return sdk.PromptResponse{}, err
	}
	return sdk.PromptResponse{StopReason: sdk.StopReason(stop)}, nil
}

// Cancel is a v1 no-op (no server cancel path yet); the turn runs to completion.
func (a *Agent) Cancel(_ context.Context, _ sdk.CancelNotification) error { return nil }

// promptText concatenates the text content blocks of a prompt.
func promptText(blocks []sdk.ContentBlock) string {
	var out string
	for _, b := range blocks {
		if b.Text != nil {
			out += b.Text.Text
		}
	}
	return out
}

// --- Unsupported methods (advertise nothing; reject cleanly) ----------------

func (a *Agent) Authenticate(context.Context, sdk.AuthenticateRequest) (sdk.AuthenticateResponse, error) {
	return sdk.AuthenticateResponse{}, sdk.NewMethodNotFound("authenticate")
}
func (a *Agent) Logout(context.Context, sdk.LogoutRequest) (sdk.LogoutResponse, error) {
	return sdk.LogoutResponse{}, sdk.NewMethodNotFound("logout")
}
func (a *Agent) CloseSession(context.Context, sdk.CloseSessionRequest) (sdk.CloseSessionResponse, error) {
	return sdk.CloseSessionResponse{}, sdk.NewMethodNotFound("session/close")
}
func (a *Agent) ListSessions(context.Context, sdk.ListSessionsRequest) (sdk.ListSessionsResponse, error) {
	return sdk.ListSessionsResponse{}, sdk.NewMethodNotFound("session/list")
}
func (a *Agent) ResumeSession(context.Context, sdk.ResumeSessionRequest) (sdk.ResumeSessionResponse, error) {
	return sdk.ResumeSessionResponse{}, sdk.NewMethodNotFound("session/resume")
}
func (a *Agent) SetSessionConfigOption(context.Context, sdk.SetSessionConfigOptionRequest) (sdk.SetSessionConfigOptionResponse, error) {
	return sdk.SetSessionConfigOptionResponse{}, sdk.NewMethodNotFound("session/set_config_option")
}
func (a *Agent) SetSessionMode(context.Context, sdk.SetSessionModeRequest) (sdk.SetSessionModeResponse, error) {
	return sdk.SetSessionModeResponse{}, sdk.NewMethodNotFound("session/set_mode")
}
