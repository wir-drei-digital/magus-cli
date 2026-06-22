// internal/acp/agent.go
package acp

import (
	"context"
	"fmt"
	"os"
	"strings"
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
	editorFS bool // editor advertised fs.readTextFile at Initialize
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
	// Record whether the editor can service local file reads. We only advertise
	// read_file to the cloud if it can (see NewSession) — otherwise the cloud
	// would propose a tool the editor cannot fulfil.
	a.mu.Lock()
	a.editorFS = req.ClientCapabilities.Fs.ReadTextFile
	a.mu.Unlock()

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

	a.mu.Lock()
	editor := a.editor
	canRead := a.editorFS
	a.mu.Unlock()

	// Advertise read_file to the cloud only if the editor can service fs reads;
	// otherwise the cloud would propose a tool the editor cannot fulfil.
	var localTools []string
	if canRead {
		localTools = []string{"read_file"}
	}

	chatSID := newID()
	if err := cloud.Send(chat.Hello{
		SessionID:    chatSID,
		Capabilities: chat.Capabilities{LocalTools: localTools},
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

	adv := make(map[string]bool, len(localTools))
	for _, t := range localTools {
		adv[t] = true
	}

	sess := &Session{
		ID:      convID,
		ChatSID: chatSID,
		Cloud:   cloud,
		Editor:  editor,
		Ctx:     context.Background(),
		// Self-evict when the cloud connection closes so dead sessions don't
		// linger in the map for the life of the (long-lived) acp process.
		OnExit: func() { a.mu.Lock(); delete(a.sessions, convID); a.mu.Unlock() },
		Exec: &Executor{
			SessionID:  convID,
			Editor:     editor,
			Advertised: adv,
			Ctx:        context.Background(),
		},
	}
	a.mu.Lock()
	a.sessions[convID] = sess
	a.mu.Unlock()
	go sess.Run()

	return sdk.NewSessionResponse{SessionId: sdk.SessionId(convID)}, nil
}

func (a *Agent) Prompt(ctx context.Context, req sdk.PromptRequest) (sdk.PromptResponse, error) {
	a.mu.Lock()
	sess := a.sessions[string(req.SessionId)]
	a.mu.Unlock()
	if sess == nil {
		return sdk.PromptResponse{}, sdk.NewInvalidParams("unknown session")
	}
	if dropped := droppedBlockKinds(req.Prompt); len(dropped) > 0 {
		fmt.Fprintf(os.Stderr, "magus acp: dropped %d unsupported prompt block(s): %v\n", len(dropped), dropped)
	}
	// Thread the SDK's per-request context so an editor session/cancel (which the
	// SDK signals by cancelling this ctx) returns the prompt promptly.
	stop, err := sess.Prompt(ctx, promptText(req.Prompt))
	if err != nil {
		return sdk.PromptResponse{}, err
	}
	return sdk.PromptResponse{StopReason: sdk.StopReason(stop)}, nil
}

// Cancel returns nil: the SDK cancels the per-request context on session/cancel,
// which unblocks Session.Prompt (see Prompt). Interrupting the cloud-side turn
// itself needs a server cancel frame (deferred to v-next), so the cloud finishes
// the turn in the background.
func (a *Agent) Cancel(_ context.Context, _ sdk.CancelNotification) error { return nil }

// promptText builds the text forwarded to the cloud. Text blocks are
// concatenated; resource_link blocks (ACP baseline, e.g. Zed @-mentions) are
// forwarded as a textual reference so the cloud agent can read_file them.
func promptText(blocks []sdk.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		switch {
		case blk.Text != nil:
			b.WriteString(blk.Text.Text)
		case blk.ResourceLink != nil:
			rl := blk.ResourceLink
			if rl.Name != "" {
				fmt.Fprintf(&b, "\n[referenced file: %s (%s)]\n", rl.Name, rl.Uri)
			} else {
				fmt.Fprintf(&b, "\n[referenced file: %s]\n", rl.Uri)
			}
		}
	}
	return b.String()
}

// droppedBlockKinds lists prompt block kinds the bridge cannot forward (image,
// audio, embedded resource). A conformant editor won't send these (we advertise
// no matching promptCapabilities), but if one does we surface the silent loss.
func droppedBlockKinds(blocks []sdk.ContentBlock) []string {
	var kinds []string
	for _, blk := range blocks {
		switch {
		case blk.Text != nil, blk.ResourceLink != nil:
			// forwarded by promptText
		case blk.Image != nil:
			kinds = append(kinds, "image")
		case blk.Audio != nil:
			kinds = append(kinds, "audio")
		case blk.Resource != nil:
			kinds = append(kinds, "resource")
		}
	}
	return kinds
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
