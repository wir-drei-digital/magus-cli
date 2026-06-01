# magus chat — Architecture Handoff

Date: 2026-06-01
Status: Pre-planning context (NOT a spec yet). Input for a fresh brainstorming/planning session.

Purpose: seed planning for `magus chat` (cloud agent + local tool execution via a reverse-tunnel MCP over WebSocket). As of `v0.4.0` the brain page/search surface is done; chat was explicitly deferred as "a bigger feature on its own."

Repos:
- `magus-cli` — `/Users/daniel/Development/magus-cli` (Go 1.26, cobra). The CLI.
- `magus` — `/Users/daniel/Development/magus` (Phoenix/LiveView, Ash, Jido agents). The cloud app. **The chat feature requires work HERE too, not just in the CLI.**

---

## 1. Current magus-cli (post v0.4.0)

Plain cobra CLI. No TUI, no WebSocket yet. Packages under `internal/`:

| Package | Responsibility |
|---|---|
| `cmd/magus/main.go` | Entry point; prints `err.Error()` on failure (root cmd has `SilenceErrors/SilenceUsage`). |
| `internal/cli` | Cobra commands, grouped (data/auth/agent/system). `loadClient()` (in `brain.go`) builds an `api.Client` from the active profile. Global flags: `--profile --json --quiet --api-url`. |
| `internal/api` | HTTP client for the cloud `/api/v2` REST API. `client.go` (Bearer auth, `{data}` envelope, `{error:{code,message,details}}`), `pages.go`, `search.go`, `brains.go`, `errors.go` (`*api.Error`). |
| `internal/config` | Profiles + active brain in `~/.config/magus/config.toml` (override `MAGUS_CONFIG_DIR`); `MAGUS_API_TOKEN`/`MAGUS_API_URL` env overrides. |
| `internal/brain` | Pure shared helpers (`ApplyFindReplace`). Neutral package importable by both `cli` and `mcp`. |
| `internal/mcp` | Bundled **stdio MCP server** (`magus mcp`), `mark3labs/mcp-go`. Exposes brain tools (`page_*`, `brain_*`, `brain_search`) to a local MCP client. |
| `internal/output` | JSON / table / quiet rendering. |
| `internal/skill`, `internal/update` | Embedded skill install; self-update. |

**Auth:** PAT (Bearer). `magus login` opens the browser to the server's `/cli/authorize`, which mints an `ApiToken` and redirects to a localhost callback with the plaintext token; stored per profile. Server validates via `MagusWeb.Api.Plugs.ApiTokenAuthPlug` on `/api/v2`. Tokens carry `scope` (read/write) and an optional `workspace_id` binding.

**Release:** push tag `v*` → GitHub Actions + goreleaser → cross-platform binaries; `magus update` self-updates from the latest GitHub release.

---

## 2. Critical mental model: two OPPOSITE "MCP" directions

"MCP" shows up twice in this product with opposite directions. Do not conflate them.

**A. Existing `magus mcp` — stdio MCP SERVER.**
A *local* MCP client (Claude Desktop / Cursor) connects to the CLI; the CLI exposes *cloud* brain tools; the tools call the cloud REST API.

```
local MCP client  ─stdio→  CLI (MCP server)  ─HTTPS→  cloud /api/v2
```

**B. Chat feature — reverse-tunnel MCP over WebSocket.**
The CLI opens an *outbound* WebSocket to the cloud; the *cloud agent* is the MCP client and invokes *local* tools (`read_file`, `exec_command`, …) on the user's machine; the CLI executes them locally and returns results. Chat streams flow down the same socket.

```
cloud agent (MCP client)  ─WebSocket→  CLI  ─→  local fs / shell / api.Client
```

The mcp-go **tool-definition shape** (name + JSON schema + handler) is reusable, but the **transport** (outbound multiplexed WS) and **direction** (local tools advertised TO the cloud) are new. The bundled stdio server and the chat WS client are distinct surfaces that may share local-tool definitions but serve opposite consumers.

---

## 3. Target architecture (from the product plan)

Single multiplexed WebSocket. CLI initiates the outbound connection; one socket carries both chat streams and tool invocations; the local tool registry is advertised on the handshake.

### Message protocol (define once, mirror on both sides)

- `hello` — handshake: `client_version`, `auth_token` (the PAT), `capabilities.local_tools[]` (name + schema).
- `chat` — user → cloud: the message + session id.
- `mcp_call` — cloud → CLI: `tool_name`, `params`, `call_id`.
- `mcp_result` — CLI → cloud: `call_id`, `result`, `error`.
- `chat_stream` — cloud → CLI: streamed assistant chunks.

### CLI side (new)

- **Transport:** WebSocket client (gorilla/websocket or coder/websocket), reconnect, heartbeat, request/response correlation by `call_id`, concurrent tool execution.
- **Local tool registry + executor:** `read_file`, `write_file`, `list_files`, `grep`, `exec_command` (allowlist + confirmation), `save_to_brain` (**reuses `api.Client` → `CreatePage`/`UpdatePageBody`; direct synergy with v0.4.0**).
- **Security:** tool allowlist; explicit user confirmation for destructive ops (`write_file`, `exec_command`); the PAT authenticates the socket; nothing runs that the user did not enable.
- **TUI:** Bubble Tea + Lipgloss — stream rendering, tool-call display + approval prompts, input box.
- **Session state:** a chat session maps to a server-side Conversation.

### Server side (magus Phoenix app — REQUIRED)

- **WS endpoint:** a Phoenix Channel or WebSock handler, authenticated with the same PAT (reuse the `ApiTokenAuthPlug` logic at socket connect).
- **Agent bridge** between the WS and the existing Jido agent:
  - Inbound `chat` drives the `ConversationAgent` (the app already turns `message.user` into `ai.react.query`).
  - The agent must see the CLI-advertised local tools as callable. The app **already supports dynamic tool registration via the `ai.react.register_tool` signal** — on handshake, register the local tools for that conversation's agent. Each such tool's "execution" is a round-trip: emit `mcp_call` down the WS, await `mcp_result`. **(To verify: that register_tool supports a handler whose execution is an async remote round-trip, plus timeout/cancellation semantics.)**
  - Stream agent output (`text.chunk` / `tool.start` / `tool.complete` signals on PubSub topic `agents:{conversation_id}`) down the WS as `chat_stream`.
- **Audit:** log local tool invocations (the app has `ActivityLogPlugin` / control-room patterns).

Server touchpoints (in the `magus` repo): `lib/magus_web/router.ex` (add the WS route), `MagusWeb.Api.Plugs.ApiTokenAuthPlug` (auth reuse), `Magus.Agents.*` (`ConversationAgent`, `ReactStrategy`, plugins, `Signals`), the `ai.react.register_tool` signal, PubSub `agents:{conversation_id}`, `lib/magus/agents/tools/` (existing tool pattern).

---

## 4. Reusable vs new

**Reuse:** config/auth (PAT, profiles, base URL), `api.Client` + Bearer, the mcp-go tool-definition shape, `output` rendering, `save_to_brain` via the v0.4.0 page API, and the app's existing agent loop (ConversationAgent / ReactStrategy / streaming signals / dynamic tool registration).

**New:** WS transport + reconnect/mux, the chat message protocol (shared schema), local tool implementations + security/confirmation, the Bubble Tea TUI, and the server WS endpoint + agent↔WS bridge + remote local-tool registration + audit.

---

## 5. This spans two repos — decompose before building

Suggested sub-projects (each its own spec → plan):

1. **Wire protocol + WS transport** — CLI client + server endpoint + auth handshake. Foundation. Goal: a trivial chat round-trip end to end.
2. **Server agent bridge** — drive `ConversationAgent` from the socket, stream output back, register CLI-advertised local tools dynamically, route `mcp_call`/`mcp_result`.
3. **Local tool layer (CLI)** — registry, executor, initial tool set, security/confirmation. Start with `save_to_brain` (reuses the existing client) and `read_file`.
4. **TUI (CLI)** — Bubble Tea chat UI, streaming, tool-approval UX.

**Recommended first milestone:** transport + a minimal chat round-trip with ONE tool (e.g. `read_file`) working end to end, before expanding the tool set or polishing the TUI. Prove the reverse tunnel and the agent bridge first; everything else is incremental.

---

## 6. Open questions for the planning session

- **Transport choice:** Phoenix Channels (free reconnect/heartbeat/topics, but the Go client must speak the Phoenix socket protocol) vs a plain WebSocket endpoint (simpler framing, but you build mux/reconnect yourself).
- **Protocol schema source of truth:** where does the canonical message schema live, and how is it kept in sync across Go and Elixir (codegen, shared doc)?
- **Conversation mapping:** does a chat session create a `Conversation` row and inherit existing features (history, naming, memory extraction, multiplayer)?
- **Remote tool registration:** confirm `ai.react.register_tool` can register a tool whose execution awaits a socket round-trip; define timeout/cancellation/error propagation.
- **Security model:** allowlist source (config vs per-session prompt), confirmation UX in a TUI, `exec_command` sandboxing, audit storage + retention.
- **Reconnect / mid-turn drops:** the app already has mid-turn agent hibernation + `Magus.Agents.Recovery`; decide how a dropped socket interacts (resume vs restart the turn).
- **TUI scope for v1:** minimal (single conversation, plain stream) vs rich (history pane, tool timeline).

---

## 7. Starting the fresh session

Suggested kickoff: invoke the brainstorming skill, point it at this doc, and decide the boundary for the FIRST sub-project (likely #1, the transport + round-trip). Expect to explore the `magus` repo's `Magus.Agents` + WS/channel options early, since the server bridge is the higher-risk half.
