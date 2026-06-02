# magus chat — Walking Skeleton Design (Sub-project #1)

Date: 2026-06-02
Status: Approved design (spec). Input for the implementation plan.
Supersedes for this sub-project: the open questions in `docs/superpowers/chat-feature-architecture-handoff.md`.

Repos:
- `magus-cli` — `/Users/daniel/Development/magus-cli` (Go 1.26, cobra). The CLI.
- `magus` — `/Users/daniel/Development/magus` (Phoenix/LiveView, Ash, Jido agents). The cloud app. **Requires server work too.**

---

## 1. Goal & scope

Prove the **reverse tunnel** end to end: a cloud agent invokes **one local tool** (`read_file`) on the user's machine, under a zero-trust, human-in-the-loop security model. This is the foundation; everything else (more tools, rich TUI, multiplayer chat) is incremental on top.

**Guiding security principle:** this feature is *remote file access driven by an LLM that follows untrusted instructions*. The thing calling local tools is **not trustworthy** — neither the LLM (hijackable by injected content) nor the server (a single high-value target whose compromise would reach every connected machine). Therefore **the server proposes; the CLI disposes.**

### In scope (v1)

- Raw WSS transport: CLI client + server `WebSock` handler, authenticated at the HTTP upgrade.
- The 6-message wire protocol (§4).
- **One** local tool: `read_file`, with path confinement + read-tier approval + allowlist.
- Per-turn tool injection scoped to the calling connection (§5), with fail-closed routing.
- Minimal line-based CLI interaction (stream output + single-keypress approval).
- Single-owner `Conversation` reuse (existing `ConversationAgent` + persistence).
- Two-sided audit.

### Out of scope (deferred)

- Other catalog tools: `write_file`, `exec_command`, `grep`, `list_files`, `save_to_brain` (defined as catalog entries, not built).
- Full multiplayer chat (multiple live participants in one conversation); two live connections to one conversation. **But the architecture is built multiplayer-correct** — see §5 and §7.
- Daemon multiplexing of many sessions over one socket.
- Sophisticated mid-turn resume (basic reconnect only).
- Protocol codegen; rich Bubble Tea TUI (→ later sub-project); `exec_command` sandboxing.

---

## 2. Architecture overview

### Component map (skeleton only)

| | `magus-cli` (Go) | `magus` (Phoenix) |
|---|---|---|
| **New** | WS transport client; local-tool registry + executor; `read_file` impl w/ path confinement; approval/allowlist engine; audit log; `magus chat` command | upgrade auth plug; `MagusWeb.ChatSocket` (WebSock); connection `Registry`; `Magus.Agents.Tools.Remote.ReadFile` proxy action; PubSub→socket stream forwarder |
| **Reused** | config/profiles/PAT, `api.Client`, `output` | `ApiTokenAuthPlug` logic, `ConversationAgent` + `ReactStrategy` (per-run `run_tools`/`run_tool_context`), PubSub `agents:{conversation_id}`, persistence |

### Concurrency model

**Per process.** Each `magus chat` invocation is its own process opening its own outbound socket: **N terminals = N independent sockets = N isolated server sessions.** No client-side multiplexing. v1 binding is **1:1:1** — connection ↔ conversation ↔ capability set. The server keys session state by **connection (+ conversation)** and allows multiple concurrent connections per PAT. Approval/allowlist "allow once" state is per-process, so it never leaks across terminals.

### End-to-end sequence (the "hello world": *"what's in mix.exs?"*)

```
terminal ── magus chat ──┐
                         │ 1. WSS upgrade  (Authorization: Bearer <PAT>)
                         │    hello{client_version, session_id, tools:["read_file"], conversation:new}
                         ▼
              ┌──────────────────────────────┐
              │ upgrade plug: auth (reuse     │
              │ ApiTokenAuthPlug) or 401      │
              │ ChatSocket:                   │ 2. create/load single-owner Conversation
              │  - register session in Registry│   (caller_session_id → this pid)
              │  - server_hello{conversation_id, accepted_tools:["read_file"]}
              └──────────┬───────────────────┘    (accepted = advertised ∩ KNOWN catalog)
   3. chat{text} ───────►│  drive turn as ai.react.query WITH:
                         │     run_tools=[Remote.ReadFile], run_tool_context=%{caller_session_id}
                         │  ReactStrategy → LLM
                         │ 4. text.delta on PubSub agents:{id} ──► forwarded as chat_stream (broadcast)
   5. LLM calls read_file│  Task.async_stream → Remote.ReadFile.run(%{path}, ctx)
                         │  ctx.caller_session_id → Registry → THIS socket pid (NOT conversation_id)
                         │  no live conn → {:error, :no_local_connection} (fail-closed)
        ◄── mcp_call{call_id, "read_file", {path}} ── (proxy blocks ≤ timeout < tool_timeout_ms)
   6. CLI enforcement pipeline (§5.2): known-tool → schema → path confine → APPROVE → execute(size-cap)
        ── mcp_result{call_id, status:ok|error|denied, …} ──►
                         │ 7. proxy returns tool result → loop continues → final answer
        ◄── chat_stream(text) ── (broadcast)
   8. exit / socket drop │  drop Registry binding; in-flight calls fail-closed
```

The proxy `run/2` blocking on the round-trip is legitimate: tools already run in `Task.async_stream` with `tool_timeout_ms: 120_000` (`react/runner.ex`), and the per-run context flows into `run/2`.

---

## 3. Transport & auth

- **WSS only.** Plain `ws://` rejected except `localhost` in dev. TLS verification always on (no skip-verify).
- **Server:** a `WebSock` handler (`MagusWeb.ChatSocket`) on a new route, behind an **authenticated upgrade plug** that extracts `Authorization: Bearer <PAT>`, validates it with the existing `ApiTokenAuthPlug` logic, assigns `current_user`/scope/workspace (or halts `401` before any socket state), then `WebSockAdapter.upgrade/4`. Inherits the token's scope/workspace binding.
- **Client:** Go, `coder/websocket` (context-aware; `gorilla/websocket` acceptable). Single writer goroutine (the lib requires one concurrent writer), a read/dispatch loop, ping heartbeat (~25s), basic reconnect with backoff.
- **Auth placement** is the HTTP upgrade header, **not** a post-upgrade `hello` frame (a deliberate change from the handoff doc): rejects unauthenticated connections before socket state exists, mirrors `/api/v2`, and a Go client can set handshake headers (browsers can't — irrelevant here).

---

## 4. Wire protocol

JSON text frames over WSS. Every frame has `type` and protocol version `v` (int). Tool calls correlate by server-generated `call_id` (UUID).

| Type | Dir | Purpose | Key fields |
|---|---|---|---|
| `hello` | CLI→srv | advertise capabilities + session intent | `client_version`, `session_id`, `capabilities.local_tools[]` (names only), `conversation:{new}` or `{resume:"<id>"}` |
| `server_hello` | srv→CLI | ack; close the capability loop | `conversation_id`, `accepted_tools[]` (advertised ∩ known catalog), `server_version` |
| `chat` | CLI→srv | user message | `session_id`, `text` |
| `chat_stream` | srv→CLI | streamed output + lifecycle | `event`: `text.delta`/`text.done`/`tool.start`/`tool.complete`/`turn.done`/`error`; `data` |
| `mcp_call` | srv→CLI | tool invocation request | `call_id`, `tool_name`, `params` |
| `mcp_result` | CLI→srv | tool result | `call_id`, `status`: `ok`/`error`/`denied`; `result` or `error:{code,message}` |

- `chat_stream` events map from the existing PubSub signals on `agents:{conversation_id}` (verify exact signal names/payloads — §8).
- Heartbeat: WS ping/pong (lib-level), CLI pings ~25s.
- **Schema source of truth (v1):** one shared protocol section in this doc, hand-mirrored as Go structs and Elixir structs. Codegen is premature for 6 message types.

---

## 5. Tool model & security

### 5.1 Representation: fixed catalog of pre-compiled proxy modules

The server ships a **known, finite** set of proxy `Jido.Action` modules (v1: just `Magus.Agents.Tools.Remote.ReadFile`; future: `Remote.WriteFile`, `Remote.ExecCommand`, `Remote.ListFiles`, `Remote.Grep`). Each is a real action with a proper schema; its `run/2` performs the reverse round-trip. A finite, known capability set — reviewed on **both** ends — is what makes human approval and audit tractable, and prevents a compromised server from inventing capabilities.

A new *kind* of tool requires a server deploy + CLI release; the catalog is a shared contract. (The eventual "arbitrary CLI-advertised tools" north star — generalizing registration to accept name+schema+handler specs — is explicitly **not** v1: it touches shared agent infra and accepts a wire-defined capability set, both of which fight the security posture.)

### 5.2 Injection: per-turn, scoped to the calling connection

**Local tools are injected per turn via `run_tools` + `run_tool_context`, NOT registered at handshake.** Confirmed by `react_strategy.ex:526–542,590`: `run_tools` *replaces* `config[:tools]` for that run (and becomes the LLM-facing tool schemas via `effective_actions_by_name`); `run_tool_context` merges into the tool execution context. Both are transient per-run state (cleared on reset, `~:1527`).

When `ChatSocket` drives a turn for a `chat` frame received on connection **C**:

```
ai.react.query{
  tools:        [Remote.ReadFile, …],        # caller C's advertised ∩ catalog  → run_tools
  tool_context: %{caller_session_id: "<C>"}  # C's identity                      → run_tool_context
}
```

Two invariants:

1. **Routing key = the turn's caller-connection, never `conversation_id`.** `Remote.ReadFile.run/2` resolves `ctx.caller_session_id → pid` via the connection `Registry`. No live connection → `{:error, :no_local_connection}` immediately (fail-closed). This is what guarantees "run on the actual caller, not other participants."
2. **Caller identity is server-attributed.** `caller_session_id` is stamped by the socket the frame *arrived on*, from its own session — **never** read from client-supplied data.

**Why this is the multiplayer-correct model:** a `Conversation` has one shared agent. A turn initiated by a participant with no live CLI connection (e.g. a web user) carries no `run_tools` and no `caller_session_id` → the LLM isn't offered local tools, and any stray call fails closed. Capability and routing **follow the turn's caller, not the conversation.** As a bonus, because nothing is registered into the shared/persisted agent config, the agent's 5-minute hibernation/thaw cannot re-expose local tools.

### 5.3 CLI enforcement pipeline (Policy Decision + Enforcement Point)

The server is a policy-agnostic relay for local tools — it only *proposes* via `mcp_call`. The **CLI is the sole PEP/PDP.** Every `mcp_call` runs this pipeline, **fail-closed at every step**:

1. **Known-tool gate** — is `tool_name` in the set *this CLI advertised*? If not → `denied`. (A compromised server can't invent capabilities.)
2. **Schema validation** — validate `params` against the CLI's *own* local schema. Malformed → `denied`.
3. **Path confinement** (`read_file`) — canonicalize (resolve symlinks → absolute); confine to the launch root (or `--root`/config). Anything escaping (`../`, outside absolutes, symlinks pointing out) → `denied`. Optional sensitive-path denylist (`.env`, `.ssh`, …) → warn/deny.
4. **Approval gate** (Claude-Code-style) — check the allowlist; if not pre-approved, **prompt showing the CLI's canonical params** with `[a] allow once · [A] allow always (persist) · [d] deny`.
   - **Anti-spoofing invariant:** the prompt renders the *CLI's* resolved interpretation of the call, never any server-supplied description. **What you approve === what executes.**
5. **Execute** — read the confined file, **size-capped** (default ~256 KB, configurable; cap + truncation marker; no unbounded slurp).
6. **Audit (two-sided)** — CLI appends a local audit line (tool, canonical params, decision, ts, conversation_id); server logs the emitted `mcp_call` via `ActivityLog`.

### 5.4 Allowlist (`config.toml`)

Tiered defaults + rule-based "allow always". "Allow once" is in-memory per session (per-process → isolated across terminals).

```toml
[chat.permissions]
read  = "prompt"   # tier default: prompt | allow | deny
write = "prompt"
exec  = "prompt"

[[chat.permissions.allow]]   # added by "allow always"
tool = "read_file"
path_prefix = "/Users/daniel/Development/magus-cli"
```

Tool risk tiers: *read* (`read_file`, `list_files`, `grep`) · *write* (`write_file`) · *exec* (`exec_command`).

### 5.5 Threat → control

| Risk | Control |
|---|---|
| Prompt-injection → tool abuse | Injection can *ask*; the approval gate means it can't *execute* unseen. Tiered defaults + human-in-loop. |
| Server compromise → fleet RCE | Pipeline steps 1–4 enforced CLI-side regardless of the wire. Worst case a hostile server can only *prompt* you — blunted by the anti-spoofing invariant. |
| Exfiltration via read | Root confinement + sensitive denylist + consent + audit. **Honest caveat:** once read, content reaches the cloud — confinement + consent is the control; treat the agent as a remote reader. |
| Path escape | Canonicalization + root confinement (exec deferred). |
| Multiplayer routing leak | Caller-connection routing (§5.2), server-attributed identity, fail-closed; never route by `conversation_id` alone. |

---

## 6. Server implementation (`magus`)

| Module | Responsibility |
|---|---|
| upgrade plug | extract Bearer → validate (reuse `ApiTokenAuthPlug` logic) → assign user/scope/workspace or halt `401`; then `WebSockAdapter.upgrade` |
| `MagusWeb.ChatSocket` (WebSock) | `handle_in`: `hello` → create/load conversation + register session in Registry + reply `server_hello`; `chat` → drive a turn (inject `run_tools` + `run_tool_context`); `mcp_result` → reply to the waiting proxy. `handle_info`: PubSub `agents:{id}` → encode `chat_stream`; `{:mcp_call,…}` from a proxy → push. `terminate`: drop Registry binding |
| connection `Registry` | `caller_session_id → connection pid`; the proxy looks up the socket, the socket holds `call_id → from` to reply on `mcp_result`. Bind to **pid** → fail-closed if absent. Monitor the pid so a `:DOWN` aborts in-flight calls |
| `Magus.Agents.Tools.Remote.ReadFile` | proxy `Jido.Action` (`name: "read_file"`, real `path` schema). `run(%{path}, ctx)`: `ctx.caller_session_id` → Registry; no conn → immediate `{:error, :no_local_connection}`; else round-trip via `GenServer.call` to the socket (timeout < `tool_timeout_ms`); map `denied`/`error` to LLM-friendly results. **Relays only — enforces no policy** (the CLI does). `denied`/timeout are **non-retryable** |
| stream forwarder | turns PubSub signals into `chat_stream`. **Caller-scoping rule:** assistant **text** broadcasts to all participant connections; tool-call **events/params/approvals** are emitted only on the socket whose run produced them (filter by `run_id`/`request_id`). In single-player v1 it's one connection — no extra cost — but the rule is baked in |

- **Conversation:** `conversation:new` creates a fresh single-owner `Conversation`; `resume:"<id>"` loads it **only if the token-user owns it** (else reject). Reuse the existing `ConversationAgent` (id `conv:<uuid>`) creation path.
- **Injection wiring** (verify — §8): inject per-turn `tools`/`tool_context` via direct `ai.react.query`, or via `message.user` + `InboundPlugin`, or the `set_run_tools`/`set_run_tool_context` signals — whichever composes with the Magus plugin chain.

---

## 7. CLI implementation (`magus-cli`)

Follows the existing `internal/` layout.

| Package | Responsibility |
|---|---|
| `internal/chat` | WS transport: dial WSS (+ Bearer header, TLS verify on), single-writer send queue, read/dispatch loop, ping heartbeat, basic reconnect/backoff, in-flight `call_id` tracking |
| `internal/localtool` | `Tool` interface (`Name`/`Schema`/`Validate`/`Execute`); registry (v1: `read_file`); the §5.3 enforcement pipeline; allowlist matching + interactive approval + persist; audit log |
| `internal/cli/chat.go` | `magus chat` command; flags `--root`, `--resume`; wires transport ↔ registry ↔ UI |
| `internal/config` | extend with `[chat.permissions]` (reuse existing package) |

**UI (v1):** minimal **line-based** loop — print `text.delta` inline; single-keypress approval prompt. Because the agent *blocks* on the tool round-trip, the stream naturally pauses for approval (no redraw complexity). **Bubble Tea rich TUI is a later sub-project.**

**Multiplayer-prep on the client:** nothing extra in v1 (one connection), but the CLI only ever acts on `mcp_call`s for tools *it* advertised and always runs the full pipeline — so it can never be made to act on another participant's behalf.

---

## 8. Error handling & edge cases

- **Tool timeout:** proxy round-trip bounded *below* `tool_timeout_ms` so it returns a clean error before the runner kills the Task. Surface "tool timed out" to the LLM.
- **User deny:** `mcp_result{denied}` → proxy returns a clear "user denied" the LLM can adapt to; marked **non-retryable** (don't let `tool_max_retries` re-ask a denied op).
- **Socket drops mid-tool-call:** monitored pid `:DOWN` → proxy errors immediately (fail-closed); runner continues or fails the turn.
- **Socket drops mid-turn (no pending call):** basic reconnect with backoff. v1 may drop in-flight streamed output; the conversation is persisted, so on reconnect the user can re-fetch history / re-ask. Sophisticated resume deferred.
- **Concurrent tool calls** (`Task.async_stream`): multiple `mcp_call`s in flight; approvals serialize in the line UI. Acceptable for v1.
- **Unknown/unadvertised tool from server:** `denied` (defense). **Malformed frames:** log + ignore, or close with a protocol-error code. **Oversized file:** size-cap + truncation marker. **Reconnect storms:** backoff.

---

## 9. Testing strategy

**CLI (Go) unit:** path confinement (traversal / symlink-escape / abs-outside-root / sensitive denylist), schema validation, allowlist matching, size cap, policy decisions + persist; **anti-spoofing** (approval prompt derives only from CLI-canonical params, ignores any server `description`).
**CLI integration:** against a stub WS server — full `read_file` round-trip with scripted approval; reconnect/heartbeat; frame encode/decode round-trips.

**Server (Elixir) unit:** upgrade auth (valid / invalid / expired / scope / workspace); `hello` → `accepted_tools` intersection (known vs unknown names); `Remote.ReadFile.run` (no-conn fail-closed / success / denied / timeout / non-retryable).
**Server integration:** simulated client → `chat` → agent (mocked LLM) → `mcp_call` → `mcp_result` → `chat_stream`; Registry bind/unbind + pid `:DOWN`; **multiplayer safety** (two connections, routing by caller-connection not conversation; a non-CLI caller's turn offers no tools); stream **caller-scoping** (tool.* only on the initiating socket).

**End-to-end (manual for the skeleton):** `magus chat` against a dev server, ask "what's in mix.exs", approve, see the answer.

---

## 10. Open / verify items (prioritized)

- **🟠 Injection wiring** — confirm *how* `ChatSocket` injects per-turn `tools`+`tool_context` (direct `ai.react.query` vs `message.user`+`InboundPlugin` vs `set_run_tools`/`set_run_tool_context`) and which composes with the Magus plugin chain.
- **🟠 Round-trip vs runner** — confirm a `run/2` that blocks on a remote round-trip composes with `Task.async_stream` + retries (`tool_max_retries: 1`); ensure `denied`/timeout are non-retryable.
- **🟠 Stream signal mapping** — exact PubSub signal names/payloads (read `StreamingPlugin` / `ToolEventPlugin`) → `chat_stream` events; confirm `tool.*` carry `run_id`/`request_id` for caller-scoped filtering.
- **🟡 Resume ownership** — enforce token-user owns the resumed conversation.
- **🟡 Token scope** — does chat require `read` scope? Local-tool *exec* is gated CLI-side regardless.
- **🟢 Hibernation** — resolved by per-turn injection (no shared/persisted local-tool state). Add a regression test confirming a thawed agent exposes no local tools.

---

## 11. Key code references

`magus`:
- `lib/magus/agents/conversation_agent.ex` — agent definition, plugins, hibernation/thaw.
- `lib/magus/agents/strategies/react_strategy.ex` — `run_tools`/`run_tool_context` (`:526–542,590`), `ai.react.query` schema (`tools`/`tool_context` optional), tool config helpers.
- `lib/magus/agents/strategies/react/runner.ex` — `Task.async_stream` tool execution, `tool_timeout_ms`, `tool_context_for`.
- `lib/magus_web/endpoint.ex` — socket declarations (add the chat route).
- `MagusWeb.Api.Plugs.ApiTokenAuthPlug` — auth to reuse at the upgrade.
- `lib/magus/agents/tools/brain/*` — existing `Jido.Action` tool pattern (schema + `run/2` + context).

`magus-cli`:
- `internal/api/client.go` — Bearer auth, `{data}` envelope, `*api.Error`.
- `internal/config/*` — profiles, active brain, env overrides (extend for `[chat.permissions]`).
- `internal/mcp/tools.go` — existing mcp-go tool-definition shape (reuse the *shape*, not the transport).
- `internal/output/*` — rendering.
