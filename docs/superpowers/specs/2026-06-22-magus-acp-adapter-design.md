# magus acp — ACP Agent Adapter Design (Sub-project)

Date: 2026-06-22
Status: Approved design (spec). Input for the implementation plan.

Relationship to the chat feature: this is an **additive third front-end** onto the
same cloud-agent bridge that `magus chat` uses. It reuses the chat **server bridge**
(`docs/superpowers/plans/2026-06-02-magus-chat-server-bridge.md`) and the chat CLI
**transport** (`internal/chat`) unchanged, and introduces exactly **one** shared
refactor in the CLI (the Executor seam, §3). It does **not** relitigate the chat
plans.

Repos:
- `magus-cli` — `/Users/daniel/Development/magus-cli` (Go 1.26, cobra). All ACP work lives here.
- `magus` — `/Users/daniel/Development/magus` (Phoenix/LiveView, Ash, Jido agents). The cloud app. **No ACP-specific server work for this skeleton** (see §10).

Reference: Agent Client Protocol — <https://agentclientprotocol.com>. Go SDK: `github.com/coder/acp-go-sdk` **v0.13.5** (latest; ACP protocol version 1 — verified current 2026-07-26, see §12).

---

## 1. Goal & scope

Prove the **ACP bridge** end to end: an editor (Zed, or any ACP client) launches
`magus acp` as a subprocess, the user prompts the magus **cloud agent** through the
editor's chat panel, the reply streams back into the editor, and when the cloud agent
wants to read a local file the **editor** services it (`fs/read_text_file`) behind its
own permission prompt. One tool, one round-trip — mirroring the chat skeleton's
"prove the tunnel first" philosophy.

**Mental model:** in ACP terms the editor is the **Client** and `magus acp` is the
**Agent** (the subprocess). But the magus *intelligence* lives in the cloud, so
`magus acp` is really a **bridge**: an ACP agent toward the editor and a chat
WebSocket client toward the cloud (§2).

### In scope (v1 walking skeleton)

- `magus acp` command speaking ACP (JSON-RPC 2.0 over stdio) via the Go SDK.
- ACP methods: `initialize`, `session/new`, `session/prompt`; outbound `session/update` notifications.
- Reuse of `internal/chat` (WS transport + wire protocol) to reach the cloud bridge.
- `chat_stream → session/update` mapping for **text** (`agent_message_chunk`) and **tool calls** (`tool_call` / `tool_call_update`).
- **One** local tool: `read_file`, executed by delegating to the editor's `fs/read_text_file`, gated by `session/request_permission`.
- The **Executor seam** (§3): a one-method interface the chat `localtool.Pipeline` already satisfies, so the editor-backed executor slots in without forking the read loop.
- Auth from the existing PAT/profile (§8).

### Out of scope (deferred)

- Other tools: `write_file` (→ `fs/write_text_file`), `exec_command`/terminal (→ `terminal/*`), `list_files`, `grep`. The advertised local-tool set is at most `["read_file"]` (and empty when the editor lacks `fs.readTextFile`, §5).
- `session/load` (resume an existing conversation).
- The server-side cancel frame that would interrupt the in-flight cloud turn (§10, §13). The editor-facing `session/cancel` *is* handled to the extent of returning the local prompt promptly (§13).
- Persistent "allow always" mapping into a CLI allowlist; v1 supports allow-once / reject only.
- Passthrough of ACP-configured MCP servers from `session/new` `mcpServers`.
- Rich `tool_call` content (diffs, locations beyond a path), `session/set_mode`, `available_commands_update`.
- The Bubble Tea TUI — that is the *terminal* front-end (rest of chat Plan 2), independent of this work.

---

## 2. Architecture overview

### The two-sided bridge

```
editor (ACP Client) ──stdio JSON-RPC──► magus acp ──WSS (internal/chat)──► cloud agent
                                         ├─ ACP agent-side   (toward the editor)
                                         └─ chat WS client    (toward the cloud)
```

`magus acp` runs one process that simultaneously:

- **Speaks ACP agent-side to the editor** over its own stdin/stdout, using
  `acp.NewAgentSideConnection(agent, os.Stdout, os.Stdin)` from the Go SDK. The editor
  drives it with `initialize` / `session/new` / `session/prompt`; the bridge pushes
  `session/update` notifications and makes client calls (`session/request_permission`,
  `fs/read_text_file`).
- **Speaks chat WS client-side to the cloud** by reusing `internal/chat` (the chat CLI
  transport): dials `wss://…/cli/chat` with the profile PAT, sends `hello` / `chat` /
  `mcp_result`, and receives `server_hello` / `chat_stream` / `mcp_call`.

The bridge owns the translation in both directions (§6, §7). The cloud is **blind** to
whether the local peer is a terminal TUI or an editor — it sees the same chat protocol.

### Component map (skeleton only)

| | `magus-cli` (Go) | `magus` (Phoenix) |
|---|---|---|
| **New** | `magus acp` command; `internal/acp` (agent, session, stream-map, editor-backed executor); the `Executor` interface extraction | — (nothing) |
| **Reused** | config/profiles/PAT; `internal/chat` transport + wire protocol; the chat **server bridge** end to end | `ChatSocket`, `ConversationAgent`, per-turn local-tool injection, `Remote.ReadFile` proxy, PubSub→`chat_stream` |

### Process / concurrency model

Mirrors `magus chat`: **1:1:1** — one ACP session ↔ one chat WS connection ↔ one cloud
`Conversation`. The editor typically runs one `magus acp` process; each `session/new`
opens its own WS to the cloud. No client-side socket multiplexing. Session state is
keyed by ACP `sessionId`, which maps to the cloud `conversation_id` returned in
`server_hello` (§5). Each session runs a `Run` pump that consumes cloud events; when
that pump exits (the cloud connection closed) the session **self-evicts** from the
agent's session map (`OnExit`), so dead sessions don't accumulate over the life of the
long-lived `magus acp` process.

---

## 3. The Executor seam — the only shared change to the chat plans

In the chat CLI plan, an inbound `mcp_call` is turned into an `mcp_result` by
`localtool.Pipeline.Handle(chat.McpCall) chat.McpResult`. We extract a one-method
interface that `Pipeline` **already satisfies**:

```go
// internal/localtool/executor.go (new — Pipeline already matches this)
type Executor interface {
    Handle(call chat.McpCall) chat.McpResult
}
```

The chat read/dispatch loop calls `executor.Handle(call)` regardless of front-end:

- **Terminal** front-end (`magus chat`) injects `*localtool.Pipeline` — confine + terminal approval + size cap + audit (the full §5.3 chat pipeline).
- **Editor** front-end (`magus acp`) injects `*acp.Executor` — delegate to the editor's `fs/read_text_file` behind `session/request_permission`.

This is the **entire** coupling between the two front-ends. It is additive: the chat CLI
plan gains one small interface that its `Pipeline` already conforms to, and the place
that wires the WS read loop takes an `Executor` instead of a concrete `*Pipeline`.
Everything else in `internal/acp` is new and self-contained.

---

## 4. ACP surface we implement

The Go SDK provides the JSON-RPC framing, dispatch, and typed payloads; we implement the
SDK's `Agent` interface and call client methods on the returned connection. Exact Go
method names/signatures are pinned by the **library eval** (§12); the ACP methods are:

**Agent-side (we handle; the editor calls):**
- `initialize` — negotiate protocol version + capabilities. We advertise prompt
  capabilities (plain text for v1) and report whether auth is satisfied (§8).
- `session/new` — create a session: dial the cloud, `hello{conversation:{new}}`, return a `sessionId`.
- `session/prompt` — forward the user's prompt to the cloud as `chat{text}`; resolve when the turn completes (§5).
- `session/cancel` (notification) — the SDK cancels the per-request context, which unblocks the in-flight `session/prompt` so it returns promptly; the cloud-side turn still runs to completion (a true mid-turn interrupt needs a server cancel frame, deferred) (§13).
- `authenticate`, `session/load`, `session/set_mode`, `logout` — **not implemented** in v1 (return the SDK's not-supported error / omit from capabilities).

**Client-side (we call; the editor handles):**
- `session/update` (notification) — stream agent output and tool-call lifecycle to the editor (§6).
- `session/request_permission` — ask the editor to approve a tool call before it runs (§7).
- `fs/read_text_file` — the editor reads the file (respecting unsaved buffers) and returns its content (§7).

---

## 5. Session lifecycle & mapping

| ACP (editor ⇄ bridge) | Chat WS (bridge ⇄ cloud) | Notes |
|---|---|---|
| `initialize` | — | No cloud call. Report capabilities + auth status. |
| `session/new{cwd, mcpServers}` | `hello{capabilities.local_tools:[…], conversation:{new}}` → `server_hello{conversation_id}` | `sessionId := conversation_id`. `local_tools` is `["read_file"]` **only if** the editor advertised `fs.readTextFile` at `initialize`; otherwise it is empty (graceful degrade — the cloud is never offered a tool the editor cannot service). `cwd` retained on the session for context; path authority stays with the editor (§7). `mcpServers` ignored in v1. |
| `session/prompt{prompt}` | `chat{session_id, text}` | One prompt = one cloud turn. The `prompt` request resolves when the turn ends. |
| (stream) | `chat_stream{…}` | Mapped to `session/update` (§6). |
| (tool round-trip) | `mcp_call` → `mcp_result` | Mapped to editor `fs`/permission calls (§7). |
| `prompt` result `{stopReason: end_turn}` | `chat_stream{event:"turn.done"}` | `turn.done` resolves the pending `prompt`. `error` → `prompt` resolves/errors per SDK. |

The bridge extracts the prompt text from the ACP `prompt` content blocks
(`promptText`): text blocks are concatenated, and `resource_link` blocks (the ACP
baseline, e.g. Zed `@`-mentions) are forwarded as a textual reference
(`[referenced file: <name> (<uri>)]`) so the cloud agent can `read_file` them.
`image` / `audio` / embedded-`resource` blocks are dropped, and the bridge writes a
stderr diagnostic naming the dropped block kinds (a conformant editor won't send
these — the bridge advertises no matching prompt capabilities).

---

## 6. Streaming: `chat_stream` → `session/update`

The cloud streams the same `chat_stream` events the chat skeleton defines (sourced from
PubSub `agents:{conversation_id}` on the server). The bridge maps them to ACP
`session/update` notifications:

| `chat_stream.event` | `session/update` | Payload mapping |
|---|---|---|
| `text.delta` | `agent_message_chunk` | `data.delta` → text content chunk |
| `text.done` | (none) | terminal text already streamed; no-op (or final chunk reconcile) |
| `tool.start` | `tool_call` | new tool call: `toolCallId` from `event_id`, `kind:"read"`, `title` from tool/inputs, `status:"in_progress"` |
| `tool.complete` | `tool_call_update` | same `toolCallId`, `status:"completed"`/`"failed"` from `status`, optional summary |
| `turn.done` | (resolves `prompt`) | resolve the pending `session/prompt` with `stopReason:end_turn` |
| `error` | (resolves `prompt` with error) | surface the cloud's reason |

A server-sent `error` frame is decoded by the chat client into a real Go error
(`code: message`, with graceful fallbacks when a field is absent) and carried on
`Event.Err`; the session propagates that reason to the pending prompt instead of a
generic "connection closed". A transport-level close likewise surfaces the
underlying read error when one is present.

**This mapping is the single source of tool-call *visibility*.** Every tool the cloud
agent runs — cloud-side brain tools *and* the local `read_file` — has an agent-side
`tool.start`/`tool.complete` lifecycle on the stream, so every tool appears in the editor
timeline from here. The §7 executor deliberately does **not** emit its own
`tool_call`/`tool_call_update`; it only adds the **permission interaction** and the
**file read** for local tools. This avoids double-reporting the same call and needs no
correlation between the stream's `event_id` and the round-trip's `call_id` (which are
distinct identifiers the CLI cannot reliably match in v1).

---

## 7. Tool execution: `mcp_call` → editor, via the ACP executor

When the cloud agent invokes the local tool, the chat bridge delivers an
`mcp_call{call_id, "read_file", {path}}`. The injected `acp.Executor.Handle` does
**permission + read only** — tool-call timeline entries come from §6, not here:

1. **Known-tool gate** — is `tool_name` in the set this bridge actually advertised?
   That set is `["read_file"]` only when the editor reported `fs.readTextFile` at
   `initialize` (§5); when it did not, the advertised set is empty and every proposed
   tool is denied. If `tool_name` is not in the advertised set → `mcp_result{status:"denied"}`.
   *(A compromised cloud cannot invent a capability — this gate is preserved from the chat pipeline.)*
2. **Request permission** — call `session/request_permission{sessionId, toolCall, options:[allow_once, reject_once]}`, where `toolCall` is built inline from the call (`kind:"read"`, `title:"Read <path>"`). On `reject` / `cancelled` → `mcp_result{status:"denied"}`.
3. **Delegate the read** — on allow, call `fs/read_text_file{sessionId, path}`. The editor resolves the path against the workspace, honors unsaved buffers, and enforces its own sandbox.
4. **Return** — `mcp_result{status:"ok", result:{content}}` back over the WS; on editor error → `mcp_result{status:"error"}`. The agent's resulting `tool.complete` (via §6) moves the editor's timeline entry to its terminal state.

### Security model — a deliberate, sound shift

| | Terminal mode (`magus chat`) | Editor mode (`magus acp`) |
|---|---|---|
| Known-tool gate | CLI | **CLI** (preserved) |
| Path confinement | CLI (`localtool.Confine`) | **Editor** (owns the fs sandbox) |
| Approval UX | CLI terminal prompt | **Editor** (`session/request_permission`) |
| Anti-spoofing | CLI renders canonical params | Editor renders the tool call it will service |

In ACP mode the **editor is the trusted local party** — which is exactly ACP's trust
model (the Client manages the environment and resource access). The bridge keeps the
known-tool gate (so the cloud can only *propose* advertised tools) and delegates
confinement + approval to the editor. CLI-side defense-in-depth confinement is possible
later but is **not** in the skeleton. Worst case, a hostile cloud can only ask the
editor to read a path the user must approve.

---

## 8. Auth

Reuse the existing PAT/profile machinery (`internal/config`, `api.Client` Bearer). No new
auth surface.

- `initialize` reports auth status. If the active profile has a token → report
  authenticated (advertise no `authMethods`, or an already-satisfied method).
- If **no** token is present → return the ACP "auth required" path and instruct the user
  to run `magus login` (v1 does **not** implement an in-protocol `authenticate` browser
  flow; that is deferred). The chat WS dial uses the same PAT the rest of the CLI uses.

---

## 9. CLI implementation (`magus-cli`)

Follows the existing `internal/` layout and reuses `internal/chat` verbatim.

| Package / file | Responsibility |
|---|---|
| `internal/cli/acp.go` (create) + `root.go` (modify) | `magus acp` command; load profile + token; build the stdio ACP connection; flags `--profile` (global), `--api-url` (global) |
| `internal/acp/agent.go` (create) | implements the SDK `Agent` interface: `Initialize` (records `fs.readTextFile`), `NewSession` (advertises `read_file` only if the editor can service reads), `Prompt` (threads the per-request ctx; logs dropped prompt blocks), `Cancel` (relies on ctx cancellation) |
| `internal/acp/session.go` (create) | per-session state: `sessionId`↔`conversation_id`, the session's `chat.Client`, the pending-prompt resolver; the `Run` pump self-evicts the session from the agent map on exit (`OnExit`) |
| `internal/acp/stream.go` (create) | pure `chat_stream` → `session/update` mapping (table in §6) |
| `internal/acp/executor.go` (create) | `acp.Executor` implementing `localtool.Executor`: the §7 pipeline (gate → permission → `fs/read_text_file` → result) |
| `internal/localtool/executor.go` (create) | the `Executor` interface (Pipeline already satisfies it) — the shared seam (§3) |
| `go.mod` (modify) | add the ACP Go SDK |

`internal/chat` (transport + wire protocol) is consumed as-is. The only change outside
`internal/acp` is the `Executor` interface extraction and wiring the WS read loop to call
`Executor.Handle` (which the chat plan can adopt directly).

---

## 10. Server (`magus`) — nothing for the skeleton

ACP reuses the chat **server bridge (Plan 1)** end to end: the `/cli/chat` WS endpoint,
auth-at-upgrade, single-owner `Conversation`, per-turn caller-scoped `read_file`
injection, `mcp_call`/`mcp_result` round-trip, and PubSub→`chat_stream`. The cloud does
not distinguish an editor peer from a TUI peer.

The **one** future server touch is a `cancel` frame to back ACP `session/cancel` (interrupt
the in-flight agent turn). It is **deferred** (§13) and additive when added.

---

## 11. Dependencies & build order

1. **Chat server bridge (Plan 1)** — hard prerequisite. ACP adds ~no server work.
2. **`internal/chat` transport** — chat Plan 2 Tasks 1 (WS URL), 2 (protocol frames), 10 (WS client). ACP reuses these and **not** the rest of Plan 2 (the `localtool` pipeline, the TUI).
3. **`internal/acp`** + the `Executor` interface extraction (this spec).

The terminal TUI (rest of chat Plan 2) is independent and can be built in parallel; the
`Executor` seam lets both land without conflict.

---

## 12. Library choice — RESOLVED: `coder/acp-go-sdk` (eval passed)

Shipped on **`github.com/coder/acp-go-sdk` v0.13.5** — typed agent/client plumbing,
agent-side connection helper (`acp.NewAgentSideConnection`), extension-method support.
The eval confirmed everything the plan needed (all method surfaces with usable Go
types; the `Agent` interface shape is compile-asserted in `agent.go`).

**Protocol currency (verified 2026-07-26):** v0.13.5 is the latest SDK release, and
ACP **protocol version 1** is the current MAJOR version — new features (e.g.
`session/delete`, additional workspace roots, richer content types) arrive as
*capabilities*, not version bumps, so staying on version 1 is current, not lagging.
Version negotiation: the agent always answers with its latest supported version (1),
which is the specified behavior when a client requests a version the agent lacks
(tested in `TestInitializeNegotiatesUnsupportedVersionDown`). `session/delete` is
behind the SDK's optional `AgentExperimental` interface and gated on a
`sessionCapabilities.delete` we do not advertise — correctly absent, not missing.

(Original fallbacks, kept for the record: `github.com/ironpark/acp-go` (unofficial),
or a hand-rolled JSON-RPC-over-stdio handler.)

---

## 13. Error handling & edge cases

- **Auth missing:** `initialize` returns auth-required; the editor surfaces "run `magus login`".
- **Cloud WS dial fails** (in `session/new`): return a `session/new` error; the editor shows it.
- **Permission denied / cancelled:** `tool_call_update{failed}` + `mcp_result{denied}`; the cloud turn adapts (the chat skeleton already marks denials non-retryable).
- **Editor `fs/read_text_file` error** (missing file, outside workspace): `mcp_result{error}` with the editor's message; the turn continues.
- **`session/cancel` in v1:** the SDK cancels the prompt's per-request context, and `Session.Prompt` selects on it — so an editor cancel returns the pending `session/prompt` promptly. The **cloud-side turn still runs to completion**: interrupting it needs a server cancel frame (deferred). A hard editor cancel may also drop the WS, which fails any in-flight `mcp_call` closed-side. Documented limitation.
- **WS drop mid-turn:** the pending `session/prompt` resolves with an error; the conversation is persisted server-side, so the user can re-prompt. (Resume is `session/load`, deferred.)
- **Unknown/unadvertised tool from cloud:** `denied` (known-tool gate).
- **`resource_link` prompt blocks:** forwarded to the cloud as a textual reference (`[referenced file: …]`) so the cloud can `read_file` them (§5).
- **`image` / `audio` / embedded-`resource` prompt blocks:** dropped, with a stderr diagnostic naming the dropped block kinds. Unmapped `chat_stream` types are ignored.

---

## 14. Testing strategy

**Unit (Go):**
- `internal/acp/stream.go` — table tests for every `chat_stream` → `session/update` mapping (§6), including no-op/unmapped events.
- `internal/acp/executor.go` — `mcp_call` → (`request_permission`, `fs/read_text_file`) → `mcp_result`, against a fake ACP client connection: allow path, reject path, editor-error path, and the **unknown-tool gate**.
- `internal/acp/agent.go` — `Initialize` (auth present vs absent), `NewSession` (dials + maps `conversation_id`), `Prompt` (forwards `chat`, resolves on `turn.done`), against a stub `chat.Client`.

**Integration (Go):**
- `magus acp` driven by a **stub ACP client** over an in-process stdio pipe + a **stub chat WS server** (mirroring the chat CLI plan's `httptest` approach) that scripts `server_hello` then an `mcp_call`. Assert the full `prompt → read_file → answer` round-trip with a scripted permission grant, and that `fs/read_text_file` is the path used (not local disk).

**Manual end-to-end:**
- Configure Zed's external-agent setting to launch `magus acp` against a dev cloud; prompt "what's in mix.exs?", approve the read, see the answer stream in. (Document the exact Zed config in the plan.)

---

## 15. Open questions / risks

- **SDK maturity & protocol currency** — *resolved.* Eval passed; shipped on v0.13.5 (latest) at protocol version 1 (current MAJOR — see §12, verified 2026-07-26). Re-check the SDK releases when picking up new capabilities (e.g. a future `session/delete`).
- **Cancellation** — *partially addressed.* An editor `session/cancel` now returns the local `session/prompt` promptly (the per-request ctx unblocks `Session.Prompt`). The **cloud-side turn is still not interruptible mid-flight** — that needs a server cancel frame, which remains deferred. Revisit with the `write_file`/terminal tools where long-running ops make a true interrupt matter more.
- **`resource_link` prompt blocks** — *addressed.* Forwarded to the cloud as a textual reference so the cloud can `read_file` them (§5). `image`/`audio`/embedded-`resource` blocks are dropped with a stderr diagnostic; richer multimodal forwarding stays out of scope.
- **fs capability gating** — *addressed.* `read_file` is advertised only when the editor reported `fs.readTextFile` at `initialize`; an editor without it gets an empty local-tool set (graceful degrade) rather than a tool the bridge cannot service.
- **Permission granularity** — ACP offers allow-once/always/reject-once/always; v1 maps only allow-once/reject. Persisting "allow always" into a CLI allowlist (bridging to `[chat.permissions]`) is deferred.
- **Permission-dialog ↔ timeline correlation** — the `toolCall` built inline for `session/request_permission` (keyed by `call_id`) is not correlated with the streamed `tool_call` timeline entry (keyed by the stream's `event_id`), so an editor *may* show the permission prompt and the timeline item as loosely associated. Cosmetic only in v1; unifying needs a shared identifier surfaced by the server.
- **`session/new` cwd vs cloud path semantics** — the cloud agent proposes a `path`; the editor resolves it. If a cloud-proposed relative path is ambiguous against the editor's workspace root, the editor's resolution wins; confirm Zed's `fs/read_text_file` path handling during the e2e.
- **Brain tools** — cloud-side brain tools (`page_*`, `brain_search`, `save_to_brain`) run entirely in the cloud and only need `tool_call` visibility (§6); they never round-trip to the editor. Confirm none are modeled as *local* tools in the bridge.

---

## 16. Key code references

`magus-cli`:
- `internal/chat/*` (chat Plan 2) — WS transport, wire protocol frames, `Client`/`Event`. Reused as-is.
- `internal/localtool/pipeline.go` (chat Plan 2) — `Pipeline.Handle(chat.McpCall) chat.McpResult`; the type the `Executor` interface is extracted from.
- `internal/config/*` — profiles, active brain, PAT, env overrides. Reused for auth.
- `internal/api/client.go` — Bearer auth pattern (reference).
- `internal/cli/root.go` — command registration + global flags.

External:
- Agent Client Protocol spec — <https://agentclientprotocol.com>.
- `github.com/coder/acp-go-sdk` — candidate Go SDK (§12).

Chat feature docs this builds on:
- `docs/superpowers/specs/2026-06-02-magus-chat-skeleton-design.md` — the chat walking skeleton.
- `docs/superpowers/plans/2026-06-02-magus-chat-server-bridge.md` — the server bridge (prerequisite).
- `docs/superpowers/plans/2026-06-02-magus-chat-cli.md` — the chat CLI (source of `internal/chat` + `localtool`).
