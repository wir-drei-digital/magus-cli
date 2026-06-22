# magus chat — CLI Implementation Plan (Plan 2 of 2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `magus-cli` (Go) half of the chat walking skeleton: a `magus chat` command that opens an authenticated WebSocket to the server, streams the agent's reply, and — when the cloud agent proposes a `read_file` — runs every call through a **local enforcement pipeline** (known-tool gate → schema validation → path confinement → human approval/allowlist → size-capped read → audit) before anything touches the filesystem.

**Architecture:** *The server proposes; the CLI disposes.* The CLI is the sole Policy Decision + Enforcement Point. Transport (`internal/chat`) dials `wss://…/cli/chat` with the profile's PAT, runs a single-writer/one-reader loop with heartbeat, and surfaces frames as events. Local tools (`internal/localtool`) implement a `Validate → Plan → Execute` contract; `Plan` does the tool-specific safety work (path confinement) and produces the *client-canonical* description used for approval — enforcing the anti-spoofing invariant (**what you approve === what executes**; the server's wording is never shown). A `Pipeline` orchestrates the six fail-closed steps. The `magus chat` command wires transport ↔ pipeline ↔ a minimal line-based UI.

**Tech Stack:** Go 1.26, cobra, `github.com/coder/websocket`, `pelletier/go-toml/v2`, stdlib (`path/filepath`, `crypto/tls`, `net/http/httptest` for tests).

**Repo:** `/Users/daniel/Development/magus-cli`.

**Spec:** `docs/superpowers/specs/2026-06-02-magus-chat-skeleton-design.md`. **Server half:** Plan 1 (`…/plans/2026-06-02-magus-chat-server-bridge.md`).

## Wire protocol (client side)

JSON text frames, `type` + `v:1`:
- **out** `hello` `{session_id, capabilities:{local_tools:[names]}, conversation:{new}|{resume:id}}`
- **in** `server_hello` `{conversation_id, accepted_tools:[names], server_version}`
- **out** `chat` `{session_id, text}`
- **in** `chat_stream` `{event, data}` (`text.delta|text.done|tool.start|tool.complete|turn.done|error`)
- **in** `mcp_call` `{call_id, tool_name, params}` → run pipeline → **out** `mcp_result` `{call_id, status, result|error}`

## File structure

| File | Responsibility |
|---|---|
| `internal/chat/url.go` (create) | derive `wss://host/cli/chat` from the API base URL; reject plaintext to non-localhost |
| `internal/chat/protocol.go` (create) | frame structs + JSON encode/decode |
| `internal/chat/client.go` (create) | WS dial (Bearer, TLS verify), single-writer send, read/dispatch, ping heartbeat |
| `internal/localtool/confine.go` (create) | path confinement (lexical + symlink) — the security core |
| `internal/localtool/tool.go` (create) | `Tool` interface (`Validate`/`Plan`/`Execute`), `Plan`, `Result`, `Registry` |
| `internal/localtool/readfile.go` (create) | `ReadFile` tool: confine + size-capped read |
| `internal/localtool/policy.go` (create) | allowlist decision (tier defaults + rules) + persist "allow always" |
| `internal/localtool/approval.go` (create) | line-based `TerminalApprover` (anti-spoofing: renders client-canonical only) |
| `internal/localtool/audit.go` (create) | append-only local JSONL audit log |
| `internal/localtool/pipeline.go` (create) | the six fail-closed steps; produces an `mcp_result` |
| `internal/config/permissions.go` (create) + `config.go` (modify) | `[chat.permissions]` schema |
| `internal/cli/chat.go` (create) + `root.go` (modify) | `magus chat` command + session loop |
| `go.mod` (modify) | add `github.com/coder/websocket` |

---

## Task 1: WS URL derivation

**Files:**
- Create: `internal/chat/url.go`
- Test: `internal/chat/url_test.go`
- Modify: `go.mod` (add the websocket dep here so later tasks compile)

- [ ] **Step 1: Write the failing test**

```go
// internal/chat/url_test.go
package chat

import "testing"

func TestWSURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"https becomes wss", "https://magus.digital", "wss://magus.digital/cli/chat", false},
		{"trailing slash trimmed", "https://magus.digital/", "wss://magus.digital/cli/chat", false},
		{"http localhost allowed", "http://localhost:4000", "ws://localhost:4000/cli/chat", false},
		{"http 127.0.0.1 allowed", "http://127.0.0.1:4000", "ws://127.0.0.1:4000/cli/chat", false},
		{"http remote rejected", "http://magus.digital", "", true},
		{"unknown scheme rejected", "ftp://magus.digital", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := WSURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("WSURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("WSURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/`
Expected: FAIL — `undefined: WSURL` (and package may not build yet).

- [ ] **Step 3: Add the websocket dependency**

Run: `go get github.com/coder/websocket@latest`
Expected: `go.mod`/`go.sum` updated.

- [ ] **Step 4: Write the implementation**

```go
// internal/chat/url.go
package chat

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// WSURL derives the chat WebSocket URL from the API base URL (e.g.
// "https://magus.digital" -> "wss://magus.digital/cli/chat"). Plaintext ws://
// is allowed only for localhost; any other plaintext or unknown scheme is an error.
func WSURL(apiBaseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(apiBaseURL, "/"))
	if err != nil {
		return "", fmt.Errorf("parse api url: %w", err)
	}

	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		if !isLocalhost(u.Host) {
			return "", fmt.Errorf("refusing plaintext ws:// to non-localhost host %q (use https)", u.Host)
		}
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported scheme %q in api url", u.Scheme)
	}

	u.Path = "/cli/chat"
	u.RawQuery = ""
	return u.String(), nil
}

func isLocalhost(host string) bool {
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/chat/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/chat/url.go internal/chat/url_test.go
git commit -m "feat(chat): derive chat ws url from api base url"
```

---

## Task 2: Protocol frames

**Files:**
- Create: `internal/chat/protocol.go`
- Test: `internal/chat/protocol_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/chat/protocol_test.go
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

func TestDecodeFrameType(t *testing.T) {
	typ, err := DecodeType([]byte(`{"type":"mcp_call","v":1,"call_id":"c1","tool_name":"read_file","params":{"path":"a.txt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if typ != "mcp_call" {
		t.Fatalf("got %q", typ)
	}
}

func TestDecodeMcpCall(t *testing.T) {
	var c McpCall
	if err := json.Unmarshal([]byte(`{"call_id":"c1","tool_name":"read_file","params":{"path":"a.txt"}}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.CallID != "c1" || c.ToolName != "read_file" || c.Params["path"] != "a.txt" {
		t.Fatalf("bad decode: %+v", c)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestEncode`
Expected: FAIL — `undefined: Encode` etc.

- [ ] **Step 3: Write the implementation**

```go
// internal/chat/protocol.go
package chat

import "encoding/json"

const protocolVersion = 1

type Capabilities struct {
	LocalTools []string `json:"local_tools"`
}

type Hello struct {
	SessionID    string         `json:"session_id"`
	ClientVersion string        `json:"client_version,omitempty"`
	Capabilities Capabilities   `json:"capabilities"`
	Conversation map[string]any `json:"conversation"`
}

type ServerHello struct {
	ConversationID string   `json:"conversation_id"`
	AcceptedTools  []string `json:"accepted_tools"`
	ServerVersion  string   `json:"server_version"`
}

type Chat struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type ChatStream struct {
	Event string         `json:"event"`
	Data  map[string]any `json:"data"`
}

type McpCall struct {
	CallID   string         `json:"call_id"`
	ToolName string         `json:"tool_name"`
	Params   map[string]any `json:"params"`
}

type McpResult struct {
	CallID string         `json:"call_id"`
	Status string         `json:"status"` // "ok" | "error" | "denied"
	Result map[string]any `json:"result,omitempty"`
	Error  *FrameError    `json:"error,omitempty"`
}

type FrameError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// frameType maps a Go frame value to its wire "type".
func frameType(v any) string {
	switch v.(type) {
	case Hello:
		return "hello"
	case Chat:
		return "chat"
	case McpResult:
		return "mcp_result"
	default:
		return ""
	}
}

// Encode wraps an outbound frame value with {type, v} and marshals it.
func Encode(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	envelope["type"] = frameType(v)
	envelope["v"] = protocolVersion
	return json.Marshal(envelope)
}

// DecodeType reads just the "type" discriminator from an inbound frame.
func DecodeType(data []byte) (string, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return "", err
	}
	return head.Type, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chat/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/protocol.go internal/chat/protocol_test.go
git commit -m "feat(chat): wire protocol frame structs + codec"
```

---

## Task 3: Config `[chat.permissions]`

**Files:**
- Create: `internal/config/permissions.go`
- Modify: `internal/config/config.go` (add `Chat` field to `Config`)
- Test: `internal/config/permissions_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/config/permissions_test.go
package config

import "testing"

func TestChatPermissionsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Profiles: map[string]Profile{},
		Chat: ChatConfig{
			Permissions: Permissions{
				Read:  "prompt",
				Write: "deny",
				Exec:  "deny",
				Allow: []AllowRule{{Tool: "read_file", PathPrefix: "/Users/me/proj"}},
			},
		},
	}
	if err := cfg.saveTo(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Chat.Permissions.Write != "deny" {
		t.Errorf("write tier mismatch: %q", loaded.Chat.Permissions.Write)
	}
	if len(loaded.Chat.Permissions.Allow) != 1 || loaded.Chat.Permissions.Allow[0].Tool != "read_file" {
		t.Errorf("allow rule not round-tripped: %+v", loaded.Chat.Permissions.Allow)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestChatPermissions`
Expected: FAIL — `unknown field Chat` / `undefined: ChatConfig`.

- [ ] **Step 3: Add the `Chat` field to `Config`**

In `internal/config/config.go`, extend the `Config` struct:

```go
// Config is the root of the on-disk TOML file.
type Config struct {
	DefaultProfile string             `toml:"default_profile"`
	Profiles       map[string]Profile `toml:"profiles"`
	Chat           ChatConfig         `toml:"chat,omitempty"`
}
```

- [ ] **Step 4: Write the permissions schema**

```go
// internal/config/permissions.go
package config

// ChatConfig holds chat-related on-disk settings.
type ChatConfig struct {
	Permissions Permissions `toml:"permissions,omitempty"`
}

// Permissions is the local policy for cloud-proposed local tools. Tier defaults
// are "prompt" | "allow" | "deny"; an empty value means "prompt" (fail-safe).
// Allow rules are the persisted "allow always" decisions.
type Permissions struct {
	Read  string      `toml:"read,omitempty"`
	Write string      `toml:"write,omitempty"`
	Exec  string      `toml:"exec,omitempty"`
	Allow []AllowRule `toml:"allow,omitempty"`
}

// AllowRule pre-approves a tool for paths/commands under a prefix.
type AllowRule struct {
	Tool       string `toml:"tool"`
	PathPrefix string `toml:"path_prefix,omitempty"`
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/config/`
Expected: PASS (existing config tests still green).

- [ ] **Step 6: Commit**

```bash
git add internal/config/permissions.go internal/config/config.go internal/config/permissions_test.go
git commit -m "feat(chat): add [chat.permissions] config schema"
```

---

## Task 4: Path confinement (security core)

**Files:**
- Create: `internal/localtool/confine.go`
- Test: `internal/localtool/confine_test.go`

> Three layers: a **lexical** check (rejects `../` traversal and absolute-outside), a **symlink** check on existing targets (rejects a symlink inside the root that resolves outside), plus a **nonexistent-leaf ancestor** check (when the leaf does not exist, resolve the deepest existing ancestor and re-verify containment, so a symlinked parent directory pointing outside root cannot be used as a creation target). Nonexistent files pass confinement (with their parent verified) and fail later as "not found".
>
> **Test to add before `write_file` lands:** symlinked-parent / nonexistent-leaf — create `outside := t.TempDir()`, symlink `root/linkdir -> outside`, then assert `Confine(root, "linkdir/new.txt")` returns `ErrEscapesRoot` (the leaf does not exist, but the parent resolves outside root). This guards the deferred `write_file` write-outside-root hole.

- [ ] **Step 1: Write the failing test**

```go
// internal/localtool/confine_test.go
package localtool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfine(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("relative inside root resolves", func(t *testing.T) {
		got, err := Confine(root, "ok.txt")
		if err != nil {
			t.Fatal(err)
		}
		want, _ := filepath.EvalSymlinks(filepath.Join(root, "ok.txt"))
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("dotdot traversal rejected", func(t *testing.T) {
		if _, err := Confine(root, "../../etc/passwd"); err == nil {
			t.Fatal("expected escape error")
		}
	})

	t.Run("absolute outside root rejected", func(t *testing.T) {
		if _, err := Confine(root, "/etc/passwd"); err == nil {
			t.Fatal("expected escape error")
		}
	})

	t.Run("symlink escaping root rejected", func(t *testing.T) {
		outside := t.TempDir()
		secret := filepath.Join(outside, "secret.txt")
		_ = os.WriteFile(secret, []byte("x"), 0o600)
		link := filepath.Join(root, "link")
		if err := os.Symlink(secret, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if _, err := Confine(root, "link"); err == nil {
			t.Fatal("expected escape error for symlink pointing outside root")
		}
	})

	t.Run("nonexistent inside root passes confinement", func(t *testing.T) {
		if _, err := Confine(root, "newfile.txt"); err != nil {
			t.Fatalf("nonexistent-but-contained should pass, got %v", err)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/localtool/ -run TestConfine`
Expected: FAIL — `undefined: Confine`.

- [ ] **Step 3: Write the implementation**

```go
// internal/localtool/confine.go
package localtool

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrEscapesRoot is returned when a path resolves outside the confinement root.
var ErrEscapesRoot = errors.New("path escapes the allowed root")

// Confine resolves path against root and guarantees the result stays inside
// root, defeating "../" traversal, absolute-outside paths, and symlink escapes.
// The returned path is absolute (and symlink-resolved when the target exists).
func Confine(root, path string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = real
	}

	target := path
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, target)
	}
	target = filepath.Clean(target)

	// Layer 1: lexical containment (rejects ../ and absolute-outside).
	if !within(absRoot, target) {
		return "", ErrEscapesRoot
	}

	// Layer 2: symlink containment (only meaningful when the target exists).
	if real, err := filepath.EvalSymlinks(target); err == nil {
		if !within(absRoot, real) {
			return "", ErrEscapesRoot
		}
		return real, nil
	}

	// Layer 3: nonexistent leaf — the lexical check above only proves the
	// *clean* path is inside root, NOT that the path it would be created at
	// stays inside root once symlinks in its existing prefix are resolved.
	// e.g. root/linkdir is a symlink to /outside; root/linkdir/new.txt does
	// not exist, so EvalSymlinks(target) fails and we fall through here, but
	// the parent resolves OUTSIDE root. Resolve the deepest EXISTING ancestor
	// and re-check containment of that resolved ancestor before accepting the
	// lexical target. (Benign for read_file — the leaf must exist — but a
	// write-outside-root hole once the deferred write_file lands.)
	ancestor := filepath.Dir(target)
	for {
		if real, err := filepath.EvalSymlinks(ancestor); err == nil {
			if !within(absRoot, real) {
				return "", ErrEscapesRoot
			}
			break
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			break // reached filesystem root without an existing ancestor
		}
		ancestor = parent
	}

	return target, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/localtool/ -run TestConfine`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/localtool/confine.go internal/localtool/confine_test.go
git commit -m "feat(chat): path confinement against traversal and symlink escape"
```

---

## Task 5: `Tool` contract + `ReadFile` tool

**Files:**
- Create: `internal/localtool/tool.go`
- Create: `internal/localtool/readfile.go`
- Test: `internal/localtool/readfile_test.go`

> `Plan` does the tool-specific safety (confinement) and produces `Display` — the **client-canonical** string shown at approval. `Execute` reads only what `Plan` resolved. This is the anti-spoofing seam.
>
> **Security (TOCTOU):** confinement runs at `Plan` time, but `Execute` runs after the human-approval window. Re-opening by name re-resolves symlinks, so a racing local process could swap a component to point outside root between approval and open. `Execute` therefore opens with `O_NOFOLLOW` on the final component and fstat-compares against the Plan-time inode. A residual race remains on the *path prefix* (an attacker who can swap a parent directory mid-window); we accept it under the single-user dev threat model (the attacker already runs as the user on the same machine) and document it in the Security section. **Portability:** `syscall.O_NOFOLLOW` is POSIX-only (darwin/linux); since `magus` also ships a Windows build (goreleaser), put the `O_NOFOLLOW` open in a build-tagged `readfile_unix.go` with a `readfile_windows.go` fallback (plain `os.Open`, weaker guarantee) so the Windows build still compiles.

- [ ] **Step 1: Write the failing test**

```go
// internal/localtool/readfile_test.go
package localtool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileValidate(t *testing.T) {
	rf := &ReadFile{Root: t.TempDir(), MaxBytes: 1024}
	if err := rf.Validate(map[string]any{}); err == nil {
		t.Error("expected error for missing path")
	}
	if err := rf.Validate(map[string]any{"path": 123}); err == nil {
		t.Error("expected error for non-string path")
	}
	if err := rf.Validate(map[string]any{"path": "a.txt"}); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestReadFilePlanConfinesAndDescribes(t *testing.T) {
	root := t.TempDir()
	rf := &ReadFile{Root: root, MaxBytes: 1024}

	plan, err := rf.Plan(map[string]any{"path": "notes.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Tool != "read_file" || plan.Tier != "read" {
		t.Fatalf("bad plan: %+v", plan)
	}
	if !strings.Contains(plan.Display, filepath.Join(root, "notes.txt")) {
		t.Errorf("display should show the canonical path: %q", plan.Display)
	}

	if _, err := rf.Plan(map[string]any{"path": "../escape"}); err == nil {
		t.Error("expected confinement error in Plan")
	}
}

func TestReadFileExecuteCapsSize(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	rf := &ReadFile{Root: root, MaxBytes: 4}

	plan, err := rf.Plan(map[string]any{"path": "big.txt"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := rf.Execute(plan)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	if res["content"].(string) != "0123" {
		t.Errorf("expected capped content, got %q", res["content"])
	}
	if res["truncated"].(bool) != true {
		t.Errorf("expected truncated=true")
	}
}

func TestReadFileExecuteMissingFile(t *testing.T) {
	rf := &ReadFile{Root: t.TempDir(), MaxBytes: 1024}
	plan, _ := rf.Plan(map[string]any{"path": "nope.txt"})
	if _, err := rf.Execute(plan); err == nil {
		t.Error("expected error reading a missing file")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/localtool/ -run TestReadFile`
Expected: FAIL — `undefined: ReadFile`.

- [ ] **Step 3: Write the implementation**

```go
// internal/localtool/tool.go
package localtool

// Plan is the validated, confined, client-canonical action. Display is what the
// approval prompt shows (anti-spoofing: derived purely client-side); MatchPath
// is what allowlist rules match against.
type Plan struct {
	Tool      string
	Tier      string
	Display   string
	MatchPath string

	path string // internal: resolved path for Execute
}

// Tool is a locally-executable capability the cloud may propose.
type Tool interface {
	Name() string
	Tier() string
	Validate(params map[string]any) error
	Plan(params map[string]any) (Plan, error)
	Execute(plan Plan) (any, error)
}

// Registry maps advertised tool names to their implementations.
type Registry map[string]Tool

func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for n := range r {
		names = append(names, n)
	}
	return names
}
```

```go
// internal/localtool/readfile.go
package localtool

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// ReadFile reads a file on the local machine, confined to Root and capped at MaxBytes.
type ReadFile struct {
	Root     string
	MaxBytes int
}

func (rf *ReadFile) Name() string { return "read_file" }
func (rf *ReadFile) Tier() string { return "read" }

func (rf *ReadFile) Validate(params map[string]any) error {
	p, ok := params["path"].(string)
	if !ok || p == "" {
		return fmt.Errorf("read_file requires a non-empty string %q param", "path")
	}
	return nil
}

func (rf *ReadFile) Plan(params map[string]any) (Plan, error) {
	p, _ := params["path"].(string)
	real, err := Confine(rf.Root, p)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Tool:      rf.Name(),
		Tier:      rf.Tier(),
		Display:   fmt.Sprintf("read_file: %s", real),
		MatchPath: real,
		path:      real,
	}, nil
}

func (rf *ReadFile) Execute(plan Plan) (any, error) {
	// TOCTOU note: `plan.path` is the symlink-resolved path produced by Plan
	// (confinement happened then). Between Plan and Execute lies the human-
	// approval window, and os.Open re-resolves symlinks at open time — a racing
	// local process could swap a now-final component to a symlink pointing
	// outside root after approval but before the open. Open the already-
	// validated object rather than re-resolving the name: open the final
	// component with O_NOFOLLOW so a swapped-in symlink fails instead of being
	// followed, and (defensively) fstat-compare against the Plan-time resolution
	// before reading. Residual race on the *path prefix* is documented in the
	// Security section; the single-user dev threat model bounds severity (an
	// attacker already runs as the user on the same machine).
	f, err := os.OpenFile(plan.path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Confirm the opened object is the same inode Plan resolved (best-effort
	// guard against a prefix-component swap during the approval window).
	if want, err := os.Lstat(plan.path); err == nil {
		if got, err := f.Stat(); err == nil && !os.SameFile(want, got) {
			return nil, ErrEscapesRoot
		}
	}

	limit := rf.MaxBytes
	if limit <= 0 {
		limit = 256 * 1024
	}
	buf, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}

	truncated := false
	if len(buf) > limit {
		buf = buf[:limit]
		truncated = true
	}
	return map[string]any{"content": string(buf), "truncated": truncated}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/localtool/ -run TestReadFile`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/localtool/tool.go internal/localtool/readfile.go internal/localtool/readfile_test.go
git commit -m "feat(chat): tool contract + confined size-capped read_file"
```

---

## Task 6: Policy (allowlist decision + persist)

**Files:**
- Create: `internal/localtool/policy.go`
- Test: `internal/localtool/policy_test.go`

> **Security (allow-rule boundary):** `Decide` matches persisted allow rules on **path-segment boundaries** via `within()` (reused from `confine.go`), never a raw `strings.HasPrefix`. A raw prefix would let "allow always" on `/proj/a.txt` silently auto-approve `/proj/a.txt.bak`, `/proj/a.txtsecrets`, etc. Correspondingly, `AddAllow` persists the **exact resolved file path** as the rule's `PathPrefix` (`within()` also matches the equal case), so an allow-always on `/proj/a.txt` matches only `/proj/a.txt` — not `/proj/a.txt.bak` or any sibling. A per-file approval is never widened into a whole-directory grant.

- [ ] **Step 1: Write the failing test**

```go
// internal/localtool/policy_test.go
package localtool

import (
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/config"
)

func TestPolicyDecide(t *testing.T) {
	perms := config.Permissions{Read: "prompt", Write: "deny", Exec: "deny"}
	p := NewPolicy(perms)

	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/a/b"}); d != DecisionPrompt {
		t.Errorf("read default should prompt, got %v", d)
	}
	if d := p.Decide(Plan{Tool: "write_file", Tier: "write", MatchPath: "/a/b"}); d != DecisionDeny {
		t.Errorf("write default should deny, got %v", d)
	}
}

func TestPolicyAllowRuleMatchesPrefix(t *testing.T) {
	perms := config.Permissions{
		Read:  "prompt",
		Allow: []config.AllowRule{{Tool: "read_file", PathPrefix: "/Users/me/proj"}},
	}
	p := NewPolicy(perms)

	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/Users/me/proj/sub/x.txt"}); d != DecisionAllow {
		t.Errorf("path under allow prefix should allow, got %v", d)
	}
	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/etc/passwd"}); d != DecisionPrompt {
		t.Errorf("path outside allow prefix should fall back to prompt, got %v", d)
	}
}

func TestPolicyAddAllow(t *testing.T) {
	p := NewPolicy(config.Permissions{Read: "prompt"})
	p.AddAllow(Plan{Tool: "read_file", Tier: "read", MatchPath: "/Users/me/proj/x.txt"})

	if d := p.Decide(Plan{Tool: "read_file", Tier: "read", MatchPath: "/Users/me/proj/x.txt"}); d != DecisionAllow {
		t.Errorf("after AddAllow the exact path should allow, got %v", d)
	}
	if len(p.Permissions().Allow) != 1 {
		t.Errorf("expected one persisted allow rule")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/localtool/ -run TestPolicy`
Expected: FAIL — `undefined: NewPolicy`.

- [ ] **Step 3: Write the implementation**

```go
// internal/localtool/policy.go
package localtool

import (
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

// Decision is the outcome of a policy lookup.
type Decision int

const (
	DecisionPrompt Decision = iota
	DecisionAllow
	DecisionDeny
)

// Policy decides whether a planned tool call is allowed, prompts, or is denied,
// based on tier defaults and persisted "allow always" rules.
type Policy struct {
	perms config.Permissions
}

func NewPolicy(perms config.Permissions) *Policy { return &Policy{perms: perms} }

func (p *Policy) Permissions() config.Permissions { return p.perms }

func (p *Policy) Decide(plan Plan) Decision {
	for _, r := range p.perms.Allow {
		// Match on path-segment boundaries, NOT a raw string prefix. A raw
		// strings.HasPrefix lets an "allow always" on /proj/a.txt silently
		// auto-approve /proj/a.txt.bak, /proj/a.txtsecrets, etc. within()
		// (from confine.go) accepts only when MatchPath == prefix or MatchPath
		// is under prefix on a separator boundary.
		if r.Tool == plan.Tool && r.PathPrefix != "" && within(r.PathPrefix, plan.MatchPath) {
			return DecisionAllow
		}
	}
	switch p.tierDefault(plan.Tier) {
	case "allow":
		return DecisionAllow
	case "deny":
		return DecisionDeny
	default:
		return DecisionPrompt
	}
}

// AddAllow persists an "allow always" rule scoped to this exact tool+path.
//
// Decide matches PathPrefix on segment boundaries via within(), and within()
// also matches the equal case (rel == "."). Persisting the exact resolved path
// therefore scopes the rule to that one file: an "allow always" on /proj/a.txt
// matches /proj/a.txt and nothing else — NOT /proj/a.txt.bak, NOT siblings.
// This is the tightest, least-surprising "allow always for THIS file"
// semantics. (A broader "allow this directory" grant would store a parent
// directory as the prefix; we deliberately do not, so a per-file approval is
// never silently widened into a whole-subtree grant.)
func (p *Policy) AddAllow(plan Plan) {
	p.perms.Allow = append(p.perms.Allow, config.AllowRule{Tool: plan.Tool, PathPrefix: plan.MatchPath})
}

func (p *Policy) tierDefault(tier string) string {
	switch tier {
	case "read":
		return p.perms.Read
	case "write":
		return p.perms.Write
	case "exec":
		return p.perms.Exec
	default:
		return ""
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/localtool/ -run TestPolicy`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/localtool/policy.go internal/localtool/policy_test.go
git commit -m "feat(chat): local tool policy with tier defaults + allow rules"
```

---

## Task 7: Terminal approver (anti-spoofing)

**Files:**
- Create: `internal/localtool/approval.go`
- Test: `internal/localtool/approval_test.go`

> The prompt renders **only** `plan.Display` (client-canonical). There is no server-supplied text path into this function — that is the anti-spoofing invariant, asserted by the test.

- [ ] **Step 1: Write the failing test**

```go
// internal/localtool/approval_test.go
package localtool

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func approveWith(t *testing.T, input string) (Decision, string) {
	t.Helper()
	var out bytes.Buffer
	a := &TerminalApprover{In: bufio.NewReader(strings.NewReader(input)), Out: &out}
	d, err := a.Approve(Plan{Tool: "read_file", Tier: "read", Display: "read_file: /Users/me/proj/secret.txt"})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	return d, out.String()
}

func TestApproverDecisions(t *testing.T) {
	if d, _ := approveWith(t, "a\n"); d != DecisionAllow {
		t.Errorf("'a' should allow once, got %v", d)
	}
	if d, _ := approveWith(t, "A\n"); d != DecisionAllowAlways {
		t.Errorf("'A' should allow always, got %v", d)
	}
	if d, _ := approveWith(t, "d\n"); d != DecisionDeny {
		t.Errorf("'d' should deny, got %v", d)
	}
	if d, _ := approveWith(t, "\n"); d != DecisionDeny {
		t.Errorf("empty (default) should deny, got %v", d)
	}
}

func TestApproverPromptShowsCanonicalDisplayOnly(t *testing.T) {
	_, prompt := approveWith(t, "d\n")
	if !strings.Contains(prompt, "/Users/me/proj/secret.txt") {
		t.Errorf("prompt must show the client-canonical display: %q", prompt)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/localtool/ -run TestApprover`
Expected: FAIL — `undefined: TerminalApprover` / `DecisionAllowAlways`.

- [ ] **Step 3: Write the implementation**

First add `DecisionAllowAlways` to the `Decision` set in `policy.go`:

```go
const (
	DecisionPrompt Decision = iota
	DecisionAllow
	DecisionDeny
	DecisionAllowAlways
)
```

Then:

```go
// internal/localtool/approval.go
package localtool

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Approver decides a single prompted tool call.
type Approver interface {
	Approve(plan Plan) (Decision, error)
}

// TerminalApprover prompts on Out and reads a single line from In. It renders
// ONLY plan.Display (the client-canonical action) — never any server-supplied
// description — so what the user approves is exactly what executes.
type TerminalApprover struct {
	In  *bufio.Reader
	Out io.Writer
}

func (a *TerminalApprover) Approve(plan Plan) (Decision, error) {
	fmt.Fprintf(a.Out, "\nThe cloud agent wants to run:\n  %s\n", plan.Display)
	fmt.Fprintf(a.Out, "[a] allow once  [A] allow always  [d] deny (default): ")

	line, err := a.In.ReadString('\n')
	if err != nil && err != io.EOF {
		return DecisionDeny, err
	}
	switch strings.TrimSpace(line) {
	case "a":
		return DecisionAllow, nil
	case "A":
		return DecisionAllowAlways, nil
	default:
		return DecisionDeny, nil
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/localtool/ -run TestApprover`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/localtool/approval.go internal/localtool/policy.go internal/localtool/approval_test.go
git commit -m "feat(chat): line-based approval prompt (client-canonical only)"
```

---

## Task 8: Audit log

**Files:**
- Create: `internal/localtool/audit.go`
- Test: `internal/localtool/audit_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/localtool/audit_test.go
package localtool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	au := &FileAudit{Path: path}

	if err := au.Record(AuditEntry{Tool: "read_file", Display: "read_file: /a/b", Decision: "allow", ConversationID: "c1"}); err != nil {
		t.Fatal(err)
	}
	if err := au.Record(AuditEntry{Tool: "read_file", Display: "read_file: /a/c", Decision: "deny", ConversationID: "c1"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d", len(lines))
	}
	var first AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Decision != "allow" || first.Tool != "read_file" {
		t.Errorf("bad first entry: %+v", first)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/localtool/ -run TestAudit`
Expected: FAIL — `undefined: FileAudit`.

- [ ] **Step 3: Write the implementation**

```go
// internal/localtool/audit.go
package localtool

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AuditEntry is one local tool-invocation decision record.
type AuditEntry struct {
	Tool           string `json:"tool"`
	Display        string `json:"display"`
	Decision       string `json:"decision"` // allow | deny | error
	ConversationID string `json:"conversation_id,omitempty"`
}

// Auditor records tool decisions locally.
type Auditor interface {
	Record(entry AuditEntry) error
}

// FileAudit appends JSONL entries to Path.
type FileAudit struct {
	Path string
}

func (a *FileAudit) Record(entry AuditEntry) error {
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(a.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/localtool/ -run TestAudit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/localtool/audit.go internal/localtool/audit_test.go
git commit -m "feat(chat): local append-only audit log"
```

---

## Task 9: The enforcement pipeline (six fail-closed steps)

**Files:**
- Create: `internal/localtool/pipeline.go`
- Test: `internal/localtool/pipeline_test.go`

> Ties the parts together: known-tool gate → schema validate → plan (confinement) → policy decide (+ approve) → execute → audit. Every failure is fail-closed: `denied` or `error`, never a silent read.

- [ ] **Step 1: Write the failing test**

```go
// internal/localtool/pipeline_test.go
package localtool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

// stubApprover returns a fixed decision.
type stubApprover struct{ d Decision }

func (s stubApprover) Approve(Plan) (Decision, error) { return s.d, nil }

func newPipeline(t *testing.T, approver Approver, perms config.Permissions) (*Pipeline, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := Registry{"read_file": &ReadFile{Root: root, MaxBytes: 1024}}
	p := &Pipeline{
		Registry: reg,
		Policy:   NewPolicy(perms),
		Approver: approver,
		Audit:    &FileAudit{Path: filepath.Join(t.TempDir(), "audit.jsonl")},
	}
	return p, root
}

func TestPipelineUnknownToolDenied(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "exec_command", Params: map[string]any{}})
	if res.Status != "denied" {
		t.Fatalf("expected denied, got %+v", res)
	}
}

func TestPipelineInvalidParamsError(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{}})
	if res.Status != "error" {
		t.Fatalf("expected error, got %+v", res)
	}
}

func TestPipelineConfinementDenied(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "../../etc/passwd"}})
	if res.Status != "denied" {
		t.Fatalf("escape must be denied, got %+v", res)
	}
}

func TestPipelinePolicyDenyShortCircuits(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "deny"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ok.txt"}})
	if res.Status != "denied" {
		t.Fatalf("deny tier must deny, got %+v", res)
	}
}

func TestPipelinePromptDeniedByUser(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionDeny}, config.Permissions{Read: "prompt"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ok.txt"}})
	if res.Status != "denied" {
		t.Fatalf("user-denied must be denied, got %+v", res)
	}
}

func TestPipelineApprovedReadsFile(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllow}, config.Permissions{Read: "prompt"})
	res := p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ok.txt"}})
	if res.Status != "ok" {
		t.Fatalf("expected ok, got %+v", res)
	}
	if res.Result["content"].(string) != "hello" {
		t.Errorf("bad content: %v", res.Result["content"])
	}
}

func TestPipelineAllowAlwaysPersists(t *testing.T) {
	p, _ := newPipeline(t, stubApprover{DecisionAllowAlways}, config.Permissions{Read: "prompt"})
	_ = p.Handle(chat.McpCall{CallID: "1", ToolName: "read_file", Params: map[string]any{"path": "ok.txt"}})
	if len(p.Policy.Permissions().Allow) != 1 {
		t.Errorf("allow-always should have persisted a rule")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/localtool/ -run TestPipeline`
Expected: FAIL — `undefined: Pipeline`.

- [ ] **Step 3: Write the implementation**

```go
// internal/localtool/pipeline.go
package localtool

import "github.com/wir-drei-digital/magus-cli/internal/chat"

// Pipeline is the local Policy Decision + Enforcement Point. Every mcp_call runs
// the six fail-closed steps before anything touches the filesystem.
type Pipeline struct {
	Registry       Registry
	Policy         *Policy
	Approver       Approver
	Audit          Auditor
	ConversationID string

	// OnAllowAlways is called when the user persists an "allow always" rule, so
	// the caller can save the updated config. Optional.
	OnAllowAlways func(*Policy)
}

func (p *Pipeline) Handle(call chat.McpCall) chat.McpResult {
	// 1. Known-tool gate.
	tool, ok := p.Registry[call.ToolName]
	if !ok {
		return p.deny(call, "unknown_tool", "tool not advertised by this client")
	}

	// 2. Schema validation.
	if err := tool.Validate(call.Params); err != nil {
		return p.fail(call, "invalid_params", err.Error())
	}

	// 3. Tool-specific safety (e.g. path confinement) + canonical plan.
	plan, err := tool.Plan(call.Params)
	if err != nil {
		return p.deny(call, "unsafe", err.Error())
	}

	// 4. Policy decision (+ approval).
	switch p.Policy.Decide(plan) {
	case DecisionDeny:
		return p.deny(call, "denied_by_policy", "blocked by local policy", plan)
	case DecisionAllow:
		// proceed
	default: // DecisionPrompt
		decision, aerr := p.Approver.Approve(plan)
		if aerr != nil {
			return p.fail(call, "approval_error", aerr.Error(), plan)
		}
		switch decision {
		case DecisionAllowAlways:
			p.Policy.AddAllow(plan)
			if p.OnAllowAlways != nil {
				p.OnAllowAlways(p.Policy)
			}
		case DecisionAllow:
			// proceed
		default:
			return p.deny(call, "denied_by_user", "the user denied this action", plan)
		}
	}

	// 5. Execute.
	out, err := tool.Execute(plan)
	if err != nil {
		return p.fail(call, "execute_error", err.Error(), plan)
	}

	// 6. Audit + return.
	p.record(plan, "allow")
	result, _ := out.(map[string]any)
	return chat.McpResult{CallID: call.CallID, Status: "ok", Result: result}
}

func (p *Pipeline) deny(call chat.McpCall, code, msg string, plan ...Plan) chat.McpResult {
	p.recordOrTool(call, plan, "deny")
	return chat.McpResult{CallID: call.CallID, Status: "denied", Error: &chat.FrameError{Code: code, Message: msg}}
}

func (p *Pipeline) fail(call chat.McpCall, code, msg string, plan ...Plan) chat.McpResult {
	p.recordOrTool(call, plan, "error")
	return chat.McpResult{CallID: call.CallID, Status: "error", Error: &chat.FrameError{Code: code, Message: msg}}
}

func (p *Pipeline) record(plan Plan, decision string) {
	if p.Audit == nil {
		return
	}
	_ = p.Audit.Record(AuditEntry{Tool: plan.Tool, Display: plan.Display, Decision: decision, ConversationID: p.ConversationID})
}

func (p *Pipeline) recordOrTool(call chat.McpCall, plan []Plan, decision string) {
	if len(plan) == 1 {
		p.record(plan[0], decision)
		return
	}
	if p.Audit != nil {
		_ = p.Audit.Record(AuditEntry{Tool: call.ToolName, Decision: decision, ConversationID: p.ConversationID})
	}
}
```

> Remove the no-op `errors.Is` line from the test once it compiles — it's only there to keep the import block valid before implementation; delete the `errors` import and that `if` block in Step 1's file. (Cleaner: drop both now.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/localtool/`
Expected: PASS (all pipeline + unit tests).

- [ ] **Step 5: Commit**

```bash
git add internal/localtool/pipeline.go internal/localtool/pipeline_test.go
git commit -m "feat(chat): local enforcement pipeline (fail-closed, six steps)"
```

---

## Task 10: WebSocket transport client

**Files:**
- Create: `internal/chat/client.go`
- Test: `internal/chat/client_test.go`

> One reader goroutine surfaces frames on channels; one writer serializes sends; a ticker pings for heartbeat. Tested against a real in-process server via `coder/websocket`'s `Accept` + `httptest`.

- [ ] **Step 1: Write the failing test**

```go
// internal/chat/client_test.go
package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestClientRoundTrip(t *testing.T) {
	// Server: expect Bearer, accept, echo a server_hello, then send an mcp_call,
	// then await the mcp_result and forward it to the test via a channel.
	gotResult := make(chan McpResult, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		// read hello
		_, _, _ = c.Read(ctx)
		// send server_hello
		sh, _ := wrap("server_hello", ServerHello{ConversationID: "conv1", AcceptedTools: []string{"read_file"}})
		_ = c.Write(ctx, websocket.MessageText, sh)
		// send mcp_call
		mc, _ := wrap("mcp_call", McpCall{CallID: "call1", ToolName: "read_file", Params: map[string]any{"path": "a.txt"}})
		_ = c.Write(ctx, websocket.MessageText, mc)
		// read mcp_result
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var res McpResult
		_ = decodePayload(data, &res)
		gotResult <- res
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := Dial(ctx, wsURL, "tok123", "magus-cli/test")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	if err := cli.Send(Hello{SessionID: "s1", Capabilities: Capabilities{LocalTools: []string{"read_file"}}, Conversation: map[string]any{"new": true}}); err != nil {
		t.Fatal(err)
	}

	sawServerHello := false
	for ev := range cli.Events() {
		switch ev.Kind {
		case KindServerHello:
			sawServerHello = true
			if ev.ServerHello.ConversationID != "conv1" {
				t.Errorf("bad conversation id: %v", ev.ServerHello.ConversationID)
			}
		case KindMcpCall:
			_ = cli.Send(McpResult{CallID: ev.McpCall.CallID, Status: "ok", Result: map[string]any{"content": "hi"}})
		}
		if sawServerHello {
			break
		}
	}

	select {
	case res := <-gotResult:
		if res.CallID != "call1" || res.Status != "ok" {
			t.Errorf("server got bad mcp_result: %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never received mcp_result")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestClientRoundTrip`
Expected: FAIL — `undefined: Dial` / `wrap` / `decodePayload` / `Event` kinds.

- [ ] **Step 3: Write the implementation**

Add small helpers to `protocol.go` used by both client and tests:

```go
// (append to internal/chat/protocol.go)

func wrap(typ string, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	env["type"] = typ
	env["v"] = protocolVersion
	return json.Marshal(env)
}

func decodePayload(data []byte, out any) error { return json.Unmarshal(data, out) }
```

Then the client:

```go
// internal/chat/client.go
package chat

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// EventKind discriminates inbound events surfaced to the consumer.
type EventKind int

const (
	KindServerHello EventKind = iota
	KindChatStream
	KindMcpCall
	KindError
	KindClosed
)

type Event struct {
	Kind        EventKind
	ServerHello ServerHello
	ChatStream  ChatStream
	McpCall     McpCall
	Err         error
}

// Client is a chat WebSocket connection. Reads are surfaced on Events();
// writes are serialized through a single goroutine.
type Client struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	send   chan []byte
	events chan Event
}

// Dial connects to wsURL with a Bearer token. TLS verification uses Go defaults
// (no skip-verify). Returns once the connection is open.
func Dial(ctx context.Context, wsURL, token, userAgent string) (*Client, error) {
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	hdr.Set("User-Agent", userAgent)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, fmt.Errorf("ws dial: %w", err)
	}
	conn.SetReadLimit(8 << 20) // 8 MiB

	cctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		conn:   conn,
		ctx:    cctx,
		cancel: cancel,
		send:   make(chan []byte, 16),
		events: make(chan Event, 16),
	}
	go c.writeLoop()
	go c.readLoop()
	return c, nil
}

func (c *Client) Events() <-chan Event { return c.events }

// Send marshals and enqueues an outbound frame.
func (c *Client) Send(frame any) error {
	data, err := Encode(frame)
	if err != nil {
		return err
	}
	select {
	case c.send <- data:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

func (c *Client) Close() {
	c.cancel()
	_ = c.conn.Close(websocket.StatusNormalClosure, "bye")
}

func (c *Client) writeLoop() {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case data := <-c.send:
			if err := c.conn.Write(c.ctx, websocket.MessageText, data); err != nil {
				return
			}
		case <-ping.C:
			ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			_ = c.conn.Ping(ctx)
			cancel()
		}
	}
}

func (c *Client) readLoop() {
	defer close(c.events)
	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			c.emit(Event{Kind: KindClosed, Err: err})
			return
		}
		typ, err := DecodeType(data)
		if err != nil {
			continue
		}
		switch typ {
		case "server_hello":
			var v ServerHello
			_ = decodePayload(data, &v)
			c.emit(Event{Kind: KindServerHello, ServerHello: v})
		case "chat_stream":
			var v ChatStream
			_ = decodePayload(data, &v)
			c.emit(Event{Kind: KindChatStream, ChatStream: v})
		case "mcp_call":
			var v McpCall
			_ = decodePayload(data, &v)
			c.emit(Event{Kind: KindMcpCall, McpCall: v})
		case "error":
			var v FrameError
			_ = decodePayload(data, &v)
			c.emit(Event{Kind: KindError, Err: fmt.Errorf("%s: %s", v.Code, v.Message)})
		}
	}
}

func (c *Client) emit(ev Event) {
	select {
	case c.events <- ev:
	case <-c.ctx.Done():
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chat/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/client.go internal/chat/protocol.go internal/chat/client_test.go
git commit -m "feat(chat): websocket transport client (dial, heartbeat, events)"
```

---

## Task 11: `magus chat` command + session loop

**Files:**
- Create: `internal/cli/chat.go`
- Modify: `internal/cli/root.go` (register the command)
- Test: `internal/cli/chat_test.go`

> The session loop reads one user line, sends `chat`, then consumes events until `turn.done`, printing text deltas and routing each `mcp_call` through the pipeline. Approval reads from the same input stream (the user isn't typing a message mid-turn). Wiring is extracted into `runChat(opts)` so it's testable against a stub server with scripted input.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/chat_test.go
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestRunChatReadFileFlow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "mix.exs"), []byte("app: :magus"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		_, _, _ = c.Read(ctx) // hello
		sh, _ := wrapTest("server_hello", map[string]any{"conversation_id": "c1", "accepted_tools": []string{"read_file"}})
		_ = c.Write(ctx, websocket.MessageText, sh)

		_, _, _ = c.Read(ctx) // chat
		mc, _ := wrapTest("mcp_call", map[string]any{"call_id": "k1", "tool_name": "read_file", "params": map[string]any{"path": "mix.exs"}})
		_ = c.Write(ctx, websocket.MessageText, mc)

		_, data, _ := c.Read(ctx) // mcp_result
		if !strings.Contains(string(data), "app: :magus") {
			t.Errorf("server did not receive file content; got %s", data)
		}
		done, _ := wrapTest("chat_stream", map[string]any{"event": "turn.done", "data": map[string]any{}})
		_ = c.Write(ctx, websocket.MessageText, done)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/cli/chat"

	// Scripted stdin: one message, then "a" to approve the read.
	in := strings.NewReader("what's in mix.exs?\na\n")
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := runChat(ctx, chatOptions{
		WSURL:     wsURL,
		Token:     "tok",
		UserAgent: "magus-cli/test",
		Root:      root,
		In:        in,
		Out:       &out,
		ConfigDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("runChat: %v", err)
	}
}

// wrapTest mirrors chat.Encode for arbitrary maps in this test package.
func wrapTest(typ string, m map[string]any) ([]byte, error) {
	m["type"] = typ
	m["v"] = 1
	return json.Marshal(m)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestRunChat`
Expected: FAIL — `undefined: runChat` / `chatOptions`.

- [ ] **Step 3: Write the implementation**

```go
// internal/cli/chat.go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/localtool"
)

type chatOptions struct {
	WSURL     string
	Token     string
	UserAgent string
	Root      string
	In        io.Reader
	Out       io.Writer
	ConfigDir string // for the audit log location
}

func newChatCmd() *cobra.Command {
	var rootDir string

	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Chat with the cloud agent, with local file access",
		Long: `Open an interactive chat with the Magus cloud agent. The agent can request
local tools (e.g. read_file); each request is shown for your approval before it runs.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			token := config.ResolveToken(cfg, profile)
			if token == "" {
				return fmt.Errorf("no token configured (run `magus login`)")
			}
			wsURL, err := chat.WSURL(config.ResolveAPIURL(cfg, profile, DefaultAPIURL))
			if err != nil {
				return err
			}
			if rootDir == "" {
				if rootDir, err = os.Getwd(); err != nil {
					return err
				}
			}
			confDir, _ := config.DefaultDir()

			return runChat(cmd.Context(), chatOptions{
				WSURL:     wsURL,
				Token:     token,
				UserAgent: "magus-cli/" + Version,
				Root:      rootDir,
				In:        os.Stdin,
				Out:       os.Stdout,
				ConfigDir: confDir,
			})
		},
	}
	cmd.Flags().StringVar(&rootDir, "root", "", "root directory local file tools are confined to (default: cwd)")
	return cmd
}

func runChat(ctx context.Context, opts chatOptions) error {
	cli, err := chat.Dial(ctx, opts.WSURL, opts.Token, opts.UserAgent)
	if err != nil {
		return err
	}
	defer cli.Close()

	reg := localtool.Registry{"read_file": &localtool.ReadFile{Root: opts.Root, MaxBytes: 256 * 1024}}

	// Load persisted permissions for the policy.
	cfg, _ := config.Load()
	reader := bufio.NewReader(opts.In)
	pipeline := &localtool.Pipeline{
		Registry: reg,
		Policy:   localtool.NewPolicy(cfg.Chat.Permissions),
		Approver: &localtool.TerminalApprover{In: reader, Out: opts.Out},
		Audit:    &localtool.FileAudit{Path: filepath.Join(opts.ConfigDir, "chat-audit.jsonl")},
		OnAllowAlways: func(p *localtool.Policy) {
			if c, err := config.Load(); err == nil {
				c.Chat.Permissions = p.Permissions()
				_ = c.Save()
			}
		},
	}

	sessionID := uuid.NewString()
	if err := cli.Send(chat.Hello{
		SessionID:     sessionID,
		ClientVersion: Version,
		Capabilities:  chat.Capabilities{LocalTools: reg.Names()},
		Conversation:  map[string]any{"new": true},
	}); err != nil {
		return err
	}

	for ev := range cli.Events() {
		switch ev.Kind {
		case chat.KindServerHello:
			pipeline.ConversationID = ev.ServerHello.ConversationID
			fmt.Fprint(opts.Out, "> ")
			line, err := reader.ReadString('\n')
			if err != nil {
				return nil // EOF: nothing to send
			}
			if err := cli.Send(chat.Chat{SessionID: sessionID, Text: line}); err != nil {
				return err
			}

		case chat.KindChatStream:
			switch ev.ChatStream.Event {
			case "text.delta":
				if d, ok := ev.ChatStream.Data["delta"].(string); ok {
					fmt.Fprint(opts.Out, d)
				}
			case "turn.done":
				fmt.Fprintln(opts.Out)
				return nil // skeleton: one turn per session
			case "error":
				fmt.Fprintf(opts.Out, "\n[error] %v\n", ev.ChatStream.Data["message"])
				return nil
			}

		case chat.KindMcpCall:
			res := pipeline.Handle(ev.McpCall)
			if err := cli.Send(res); err != nil {
				return err
			}

		case chat.KindError:
			return ev.Err

		case chat.KindClosed:
			return nil
		}
	}
	return nil
}
```

> NOTE 1: `runChat` builds ONE `bufio.Reader` over `opts.In` and shares it with the `TerminalApprover`. Never wrap the same stream in two `bufio.Reader`s — the first buffers ahead and swallows the approval line. The single-reader model is correct here because the agent blocks on the tool call (the user is approving, not typing a new message). In production `opts.In` is `os.Stdin`; in the test it is a scripted `strings.Reader` (message line, then approval line).
> NOTE 2: `config.DefaultDir()` — `defaultDir()` is currently unexported. Export it (rename to `DefaultDir` or add `func DefaultDir() (string, error) { return defaultDir() }`) in `internal/config/config.go` so the command can locate the audit file. Add this as part of this task.
> NOTE 3: `github.com/google/uuid` is already an indirect dep (`go.mod`); promote it to a direct dep via `go get github.com/google/uuid` if `go mod tidy` complains.

- [ ] **Step 4: Register the command**

In `internal/cli/root.go`, add to the `agent` group:

```go
	addInGroup("agent", newChatCmd())
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestRunChat`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/chat.go internal/cli/root.go internal/cli/chat_test.go internal/config/config.go go.mod go.sum
git commit -m "feat(chat): magus chat command + session loop"
```

---

## Task 12: Full verification

- [ ] **Step 1: Build, vet, format, and test everything**

Run:
```bash
gofmt -l internal/ cmd/ && go vet ./... && go build ./... && go test ./...
```
Expected: `gofmt -l` prints nothing; vet/build clean; all tests PASS.

- [ ] **Step 2: Manual end-to-end (with the server from Plan 1)**

1. Start the `magus` server (`mix phx.server`) with Plan 1 deployed.
2. `magus login` (or set `MAGUS_API_TOKEN`/`MAGUS_API_URL`).
3. `cd` into a project and run `magus chat --root .`.
4. Type: `read mix.exs and tell me the app name`.
5. Expect an approval prompt showing `read_file: <abs path>/mix.exs` → press `a`.
6. Expect the streamed answer; confirm `~/.config/magus/chat-audit.jsonl` has an `allow` entry.
7. Re-run and try `d` (deny) → the agent should report it couldn't read the file (server proxy returns a clean "user denied").

- [ ] **Step 3: Commit any fixups**

```bash
git add -A && git commit -m "chore(chat): verification fixups"
```

---

## Security & threat model

- **Threat model:** single-user developer machine. The cloud agent is semi-trusted (its tool proposals are policy-gated and human-approved); a local attacker who already runs code as the same user is largely out of scope, which bounds the severity of the residual races below.
- **Path confinement (Task 4):** three layers — lexical (`../`/absolute-outside), symlink-on-existing-target, and nonexistent-leaf ancestor resolution. The third layer closes the symlinked-parent / nonexistent-leaf write-outside-root hole that lands with the deferred `write_file` (test noted in Task 4).
- **Execute TOCTOU (Task 5):** confinement happens at `Plan` time but `Execute` runs after the human-approval window. `read_file` opens the resolved path with `O_NOFOLLOW` and fstat-compares against the Plan-time inode. **Residual race:** an attacker who can swap a *parent directory* component between approval and open could still redirect the open; this is accepted under the single-user threat model and should be revisited (openat-style fd-relative traversal) if the model ever widens. **POSIX-only:** `syscall.O_NOFOLLOW` is darwin/linux; build-tag a Windows fallback (`magus` ships a Windows binary).
- **Allow-rule boundary (Task 6):** persisted "allow always" rules match on path-segment boundaries via `within()`, never raw `strings.HasPrefix`, and `AddAllow` stores the **exact resolved file path** (`within()` matches the equal case) — so an allow on `/proj/a.txt` matches only `/proj/a.txt`, never `/proj/a.txt.bak` or a sibling. (A whole-directory grant would store a parent dir instead; we don't, to avoid widening a per-file approval.)
- **Anti-spoofing:** the approver only ever renders `plan.Display` (built client-side in `Tool.Plan`); no server-supplied path reaches the prompt (Tasks 5, 7, 9).

## Self-review notes (coverage vs. spec §5.3, §7)

- Transport (WSS, Bearer at dial, TLS verify, heartbeat, events) → Tasks 1, 10. WS URL safety (no plaintext to remote) → Task 1.
- Enforcement pipeline, all six steps fail-closed → Task 9, built from: known-tool gate (Task 9), schema validation (Task 5 `Validate`), path confinement (Task 4), policy + allowlist (Task 6), approval + anti-spoofing (Task 7), size-capped execute (Task 5), audit (Task 8).
- `[chat.permissions]` config + persist "allow always" → Tasks 3, 6, 11.
- `magus chat` command, minimal line UI, per-process isolation (each invocation its own session/policy state) → Task 11.
- **Anti-spoofing invariant** — enforced structurally: the approver only ever receives `plan.Display`, which is built client-side in `Tool.Plan`; no server field reaches it (Tasks 5, 7, 9).
- **Deferred (per spec):** rich Bubble Tea TUI; robust reconnect/resume mid-turn (Task 10 has heartbeat; reconnect is minimal — the skeleton does one turn per session); `write_file`/`exec_command`/`grep`/`list_files` (add as `Tool`s + catalog entries later); sensitive-path denylist (a small `Confine` extension).
```
