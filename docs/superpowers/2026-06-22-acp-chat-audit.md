# magus ACP + Chat Audit

**Date:** 2026-06-22
**Branch:** `feat/acp-cli`
**Auditor:** multi-agent adversarial review

## Scope

This audit covers the ACP adapter and chat transport work on `feat/acp-cli`, reviewed at two different maturity levels:

- **Built code (reviewed as code):**
  - `internal/acp/` — the ACP agent bridge (`agent.go`, `session.go`, `executor.go`, `stream.go`, `ports.go`) plus tests.
  - `internal/chat/` — the cloud chat WebSocket transport (`client.go`, `protocol.go`, `url.go`) plus tests.
- **Plans (reviewed as design documents, NOT yet built):**
  - The chat CLI pipeline — `internal/localtool` enforcement pipeline and the `magus chat` Bubble Tea TUI (`docs/superpowers/plans/2026-06-02-magus-chat-cli.md`).
  - The magus Phoenix **server bridge** — the cloud-side WebSocket handler, `Remote.ReadFile` proxy, and per-turn tool injection (`docs/superpowers/plans/2026-06-02-magus-chat-server-bridge.md`).
  - Cross-cutting specs: `docs/superpowers/specs/2026-06-22-magus-acp-adapter-design.md`, `docs/superpowers/specs/2026-06-02-magus-chat-skeleton-design.md`.

The server bridge is **not yet implemented** in the magus Phoenix repo. Contract issues between the CLI and server are therefore design-time findings, not live defects against a running server.

## Methodology

The review used a multi-agent adversarial workflow:

- **7 finder dimensions** scanned the code and plans independently (protocol contract, security/path handling, error propagation, concurrency/lifecycle, plan-vs-reality drift, spec conformance, test coverage).
- Every candidate finding was handed to a dedicated **refute-by-default verifier** — the verifier's job was to disprove the finding, not confirm it. 7 finders × 7 verification passes ≈ **49 agents**.

**Result:** 42 findings, **0 fully refuted**. Severity after verification:

| Severity | Count |
|---|---|
| Critical | 0 |
| High | 0 |
| Medium | 5 |
| Low | 31 |
| Info | 6 |

No critical or high-severity issues. The medium findings are concentrated in error propagation (built code) and a handful of plan-vs-reality drifts (server bridge) that would have produced confusing failures or compile errors when the bridge is built.

---

## Findings

### 1. Built ACP / transport code (medium + low)

| # | Sev | Finding | Location |
|---|---|---|---|
| 1.1 | Medium | Cloud/server error messages were swallowed and surfaced to the editor as a generic `"connection closed"`. The error frame's `code`/`message` were never decoded into `Event.Err`, and the session had no path to surface socket read errors. | `chat/client.go` (error-frame decode), `acp/session.go` (`closeMsg`) |
| 1.2 | Medium | Dead cancel `select`: `Prompt` blocked on a turn using `context.Background()`, so the `<-ctx.Done()` arm could never fire — an editor `session/cancel` could not return the prompt. | `acp/agent.go` `Prompt`, `acp/session.go` `Prompt` |
| 1.3 | Low | `promptText` dropped `resource_link` blocks (e.g. Zed `@`-mentions) with no log, silently losing user-referenced context. | `acp/agent.go` `promptText` |
| 1.4 | Low | Failed/denied `read_file` results rendered as **completed** tool calls in the editor timeline (`tool.complete` always mapped to `completed`/`failed` from a `status` the cloud may not send for denials). | `acp/stream.go` `toolStatus` |
| 1.5 | Low | `Initialize` ignored `ClientCapabilities.fs.readTextFile` — the bridge advertised `read_file` to the cloud unconditionally, even to an editor that cannot service local reads. | `acp/agent.go` `Initialize` |
| 1.6 | Low | Sessions were never reclaimed: entries lived in the agent's `sessions` map for the life of the long-lived `magus acp` process, even after the cloud connection closed. | `acp/agent.go`, `acp/session.go` |

### 2. CLI ↔ server protocol contract (low)

| # | Sev | Finding | Detail |
|---|---|---|---|
| 2.1 | Low | `mcp_result` error detail dropped on the wire vs. the server's reader. The CLI places the error in the frame's top-level `error{}` field (`McpResult.Error *FrameError`), but the server bridge plan reads only `result`. Error detail would be lost server-side. | `chat/protocol.go` `McpResult`; server-bridge plan Task 7 |
| 2.2 | Low | The top-level `error` frame is not formalized in spec §4 (the wire-protocol surface). The CLI both emits and decodes it, but the spec describes it only narratively in §13, leaving the contract under-specified. | spec §4 / §13 |
| 2.3 | Low | `tool.complete` summary key drift: the CLI/spec stream-mapping reads one key for the tool summary while the server side is described with a different key (`output_summary` vs `summary`). The summary would silently not render. | spec §6; server-bridge plan Task 8 |

### 3. Server-bridge plan vs. real magus codebase (medium + info)

| # | Sev | Finding | Detail |
|---|---|---|---|
| 3.1 | Medium | Local-tool injection bypasses the `supports_tools?` gate. The plan appends resolved local-tool modules to the agent toolset without re-checking the model/agent tool-support gate, so tools could be injected for a conversation whose agent does not support tools. | server-bridge plan Tasks 4/5 |
| 3.2 | Medium | Task 8 calls a non-existent function `Magus.Chat.list_messages!/2`. The real interface is `message_history!`. (The plan hedges this in a NOTE, but the task body still uses the wrong name.) | server-bridge plan Task 8, line ~1075 |
| 3.3 | Medium | Task 10 reads `__jido_strategy_opts__/0`, which is **not exported** by `use Jido.Agent`. Use the public `strategy_opts()` accessor (or a source-level assertion). (Plan hedges this in a NOTE.) | server-bridge plan Task 10, line ~1220 |
| 3.4 | Low | "Mirror `SseStreamer`" reads the wrong error fields — the PubSub→`chat_stream` mapping copies error-shape assumptions that do not match `SseStreamer.handle_payload/4`'s actual payload fields. | server-bridge plan Task 8 |
| 3.5 | Low | Task 2 timeout note is imprecise about the ReAct runner's behavior (the runner enforces `timeout: :infinity` and retries only specific error types); the proxy's self-timeout description should state this precisely so the fail-closed/non-retryable contract is unambiguous. | server-bridge plan Task 2 |
| 3.6 | Info | `ApiTokenAuthPlug` assigns only `current_user` / `current_token`. The token also carries `scope` and an optionally-bound `workspace_id`; the plan should not assume a loaded workspace is available from the assigns. | server-bridge plan Task 9 |

### 4. Planned chat security — fix before building `internal/localtool` (low)

These are design-time hardening items in the chat-CLI plan's `Confine`/policy core. They are not live (the pipeline is unbuilt), but should be fixed in the plan before `internal/localtool` is written.

| # | Sev | Finding | Detail |
|---|---|---|---|
| 4.1 | Low | Allowlist raw-prefix escape. `AllowRule.PathPrefix` matching on a raw string prefix lets a sibling/suffix path slip through (e.g. prefix `/a/b` matches `/a/bc`). Match on a path-boundary, not a string prefix. | chat-cli plan Task 6 `policy.go` |
| 4.2 | Low | `Confine` TOCTOU: the design checks containment, then the read happens later against the same path — a check-then-use gap where the target can change (e.g. a symlink swapped in) between check and read. | chat-cli plan Task 4 `confine.go` |
| 4.3 | Low | Nonexistent-leaf parent-symlink: a path whose final component does not exist passes confinement (by design), but its parent directory could be a symlink escaping the root. Latent for `read_file`; becomes exploitable once `write_file` lands. | chat-cli plan Task 4 `confine.go` |

### 5. Lower / known issues (brief)

- No reconnect/backoff on the chat WS despite the spec mentioning resilience; the skeleton does one turn per session.
- `Client.Send` is fire-and-forget once enqueued; a write failure cancels the connection but the caller already saw success.
- WS ping errors are discarded (`_ = c.conn.Ping(...)`).
- `WSURL` localhost handling: portless `[::1]` and any userinfo in the API URL are not specially handled.
- `cwd` is not retained on the session (spec §5 says it should be), so cloud-side context that depends on it is unavailable.
- Executor seam location — the `chat.Executor` interface lives in the transport package; consider whether the seam belongs closer to the ACP layer.
- `magus acp` is undocumented in the README.
- In-protocol auth is a dead-end (`Authenticate` returns method-not-found); auth is PAT-only via `magus login`. Acceptable for v1 but worth noting.
- Test gaps: no coverage for socket-read-error propagation paths, summary-key rendering, or the cancel round-trip beyond the unit level (full e2e needs the server bridge).

---

## Resolution status

### Fixed in this PR (commit `a245485`)

**Built-code fixes:**

- **Error propagation (1.1):** `chat/client.go` now decodes the `error` frame's `code`/`message` into `Event.Err` (`frameErr`), and surfaces socket read errors on `KindClosed`. `acp/session.go`'s `closeMsg` reports the real reason instead of a generic `"connection closed"`.
- **Live cancel (1.2):** `Session.Prompt` now takes the SDK's per-request context and `select`s on `ctx.Done()`, so an editor `session/cancel` returns the prompt promptly. (The cloud-side turn still finishes in the background — see Deferred.)
- **fs gating (1.5):** `Initialize` records `ClientCapabilities.fs.readTextFile`; `read_file` is advertised to the cloud only when the editor reports `fs.readTextFile`. Otherwise no local tools are offered (graceful degrade).
- **resource_link forwarding + dropped-block log (1.3):** `promptText` forwards `resource_link` blocks as a textual file reference the cloud agent can `read_file`; `droppedBlockKinds` emits a stderr diagnostic for unsupported image/audio/embedded blocks.
- **Session self-eviction (1.6):** sessions remove themselves from the agent map via `OnExit` when their `Run` pump exits.

**Plan / spec patches:**

- **Server-bridge plan corrections (3.x):** fixed `message_history!` (3.2), `strategy_opts()` (3.3), the `supports_tools?` gate note (3.1), the `SseStreamer` field mapping (3.4), the Task 2 timeout wording (3.5), and the `ApiTokenAuthPlug` assigns note (3.6).
- **Chat-CLI plan security fixes (4.x):** path-boundary allowlist matching (4.1), `Confine` TOCTOU guidance (4.2), and the nonexistent-leaf parent-symlink note (4.3) added before `internal/localtool` is written.
- **Spec §4 formalization (2.2):** the top-level `error` frame is now part of the wire-protocol surface.
- **ACP spec sync:** spec §4–§6 reconciled with the built bridge behavior (capabilities, stream mapping, summary key 2.3, mcp_result error field 2.1).

### Deferred (tracked)

- **Server cancel frame + true cloud interrupt** — `Cancel` currently only unblocks the local prompt; interrupting the cloud-side turn needs a server cancel frame.
- **Reconnect / backoff** on the chat WS.
- **Persistent "allow always"** policy storage in `internal/localtool`.
- **`write_file` / terminal tools** — add as `Tool`s + catalog entries; **harden `Confine` first** (the nonexistent-leaf parent-symlink case 4.3 becomes exploitable with writes).
- **Full end-to-end test** — blocked on the server bridge being built.
- **README documentation** for `magus acp`.

---

## Second adversarial review round (post-fix)

After the fixes above, a second multi-agent review (6 units: 3 adversarial code lenses over the built-code commit + 3 doc verifiers over the plan/spec patches, high/critical findings refute-verified) checked the work itself. The built-code concurrency was confirmed **race-free** by reasoning plus `go test -race -count=3` and a 50× stress of the cancel/re-entrant paths; the server-plan corrections were re-verified accurate against the real magus code (the `mcp_result` 5-tuple is consistent at all sites; `message_history!`, `strategy_opts()`, the `SseStreamer` field caveat, and the `safe_execute_module` timeout note all check out). It surfaced a few items, now fixed:

- **`session/cancel` conformance (medium):** the threaded-ctx cancel made `Agent.Prompt` return a JSON-RPC error (`-32800`) instead of the ACP-idiomatic `PromptResponse{StopReason: cancelled}`. Fixed — `Prompt` now returns `StopReason: cancelled` (nil error) when the per-request ctx is cancelled (matches the SDK reference agent). Test added.
- **`resource_link` when fs unavailable (low):** `promptText` injected a "referenced file" hint even when `read_file` wasn't advertised (editor lacks `fs.readTextFile`), implying a fetch the cloud can't perform. Fixed — the hint is gated on `canRead`; otherwise the block is reported as dropped.
- **Allowlist guarantee (low, doc):** the chat-CLI plan's `AddAllow` stored the **parent directory**, contradicting its own "cannot leak to `a.txt.bak`" guarantee (it actually granted the whole dir). Fixed — `AddAllow` now stores the **exact resolved file path** (`within()` matches the equal case), so an allow-always scopes to that one file; doc guarantee corrected.
- **`O_NOFOLLOW` portability (info, doc):** noted that `syscall.O_NOFOLLOW` is POSIX-only and the planned `read_file` `Execute` needs a build-tagged Windows fallback (magus ships a Windows binary).

~~Still deferred: a cancelled cloud-side turn keeps running, so a stale `turn.done` could complete a subsequent prompt.~~ **Closed in the Codex round below** (session draining); the server cancel frame itself remains deferred and would shorten the drain window.

## Codex review round (2026-07-26)

A second-opinion review by Codex (GPT-5.x, session `019f9fb6-8458-7f11-833b-22e6f4d5d82d`) over the built Go code surfaced 5 findings; all verified and fixed:

1. **High — stale-turn signal leak, escalated to reachable-in-normal-flow.** Verified against the SDK source: the SDK cancels the prior prompt's ctx **whenever a new `session/prompt` arrives on the same session** (not just on explicit `session/cancel`), so `clearTurn`-on-cancel let the old cloud turn's `turn.done` complete the *next* prompt. Fixed with a **draining** state: a cancelled turn keeps the session busy (new prompts rejected with a clear error) until its terminal event arrives; any terminal event clears the drain (server is single-flight per conversation). Test: cancel → reject-while-draining → drain → next prompt completes on its own terminal only.
2. **Medium — `session/close` unimplemented.** Now implemented and advertised (`sessionCapabilities.close`): map removal + cloud WS close (unblocks any in-flight prompt, ends the pump). `OnExit` eviction made identity-safe.
3. **Medium — empty cloud `error` message masqueraded as success.** `TurnEnd` returned `(true, "")` for an `error` event with a missing/empty `message` — and empty is the success sentinel. Now substitutes "cloud turn failed". (Genuinely new find — none of the prior review rounds caught it.)
4. **Medium — inbound frames never validated `v`.** The chat client now validates the `{type, v}` envelope (`decodeHead`); a parseable frame with the wrong version surfaces as a descriptive `KindError` (failing the handshake fast) instead of being processed as v1; the `NewSession` handshake also surfaces `Event.Err` detail now.
5. **Low — `tool.start` lacked `kind:"read"`.** `read_file` tool-call timeline entries now carry the ACP read kind; cloud-side tools stay generic.

All fixes tested (`go test ./...` green; `-race -count=3` clean on `internal/acp` + `internal/chat`).

## Magus-side re-verification (2026-07-26)

~550 commits landed in the magus Phoenix repo after the original verification, touching essentially every file the server-bridge plan cites. A three-agent re-verification (turn-driving/injection seam; ReAct runner internals; signals/auth/infra) checked every plan claim against current `main` (`7cd5dc4f`). The bridge itself remains **unbuilt**. Results:

**One critical drift (fixed in the plan):** `safe_execute_module/4` no longer discards the per-tool timeout — since 2026-07-08 it **enforces** the wall-clock (`Task.yield` + brutal kill; 120s on the ConversationAgent path, 15s default). A brutal-killed proxy would return a **retryable** `{:error, %{type: :timeout}}`, defeating the non-retryable design — and the 15s default is *below* the proxy's 30s self-timeout. Fix applied to the plan: `Remote.ReadFile` now exports `execution_timeout_ms/0 → :infinity` (a runner-honored per-tool override) so the proxy's self-timeout is again the sole bound. Spec §6/§8/§10 updated to match.

**Everything else holds** (with line drift): `send_user_message` metadata threading, `Dispatcher.build_signal_data/3`, the Preflight insertion point (now ~152-153), `run_tools`/`run_tool_context` per-turn semantics + hibernation guarantee, the `supports_tools?` gate (`build_tools` now 6-arity), retry semantics, all six broadcast signal types with exact field names, `summarize_tool_result` (moved to `support/persistence.ex`, behavior identical — the denied-reads-render-as-success caveat stands), the SseStreamer field mismatch (still unfixed — caveat stays), and the auth plug + token loading (plus new: query-level expiry/revocation, `created_via: :cli_login` provenance).

**New facts folded into the plan:** a Phoenix Channel chat transport (`ConversationChannel`) now coexists on the same `agents:{id}` topic (raw-WS path still justified for the Go CLI; noted); new signal families (`thinking.chunk`, `turn.empty`, `turn.keepalive`, `tool.step.*`, …) that the mapper's catch-all correctly drops (forwarding `thinking.chunk` is a v-next candidate); `websock_adapter` is already a transitive dep; the supervision tree is now composed (`base_children/0`, Registry goes after PubSub) and routes must land in `core_router.ex` `core_routes()` before the SPA catch-all; the runner now injects `__event_id__` into tool context — forwarding it in `mcp_call` would close the permission-dialog↔timeline correlation gap; a second (resume-path) `ai.react.query` builder exists and is deliberately not augmented; `message_history!` returns a plain list by default (not a keyset page); conversation read broadened to members/grantees (ownership rejection intact).

## Server bridge — BUILT (2026-07-26)

The magus Phoenix server bridge (`2026-06-02-magus-chat-server-bridge.md`) has since been **implemented** on the magus branch `feat/cli-chat-bridge` (13 commits) via the same subagent-driven process: all 10 plan tasks + the ACP-parity test, each individually spec+quality reviewed, plus a final whole-branch review. The full suite is green (compile `--warnings-as-errors`, `format --check-formatted`, 38 tests across `remote/` + `web/cli/` + `cli/` + dispatcher). `GET /cli/chat` is live and PAT-authenticated. The final whole-branch review's one Important finding — the connection Registry key was the raw client `session_id` (a global namespace; a dropped-then-reclaimed key could cross tenants) — was fixed by namespacing the routing key as `"<user_id>:<session_id>"` (contained in `ChatSocket`; proxy/injection/dispatcher unchanged). Documented follow-ups: reap the `pending` map on abandoned calls; return the turn's message id so the CLI can correlate `turn.done` under concurrent drivers; consider requiring write-scope (v1 allows any valid token); tolerate `nil` `turn.done` message_id on the CLI.

### Protocol sync (2026-08-06)

The server bridge landed a "Breaking protocol changes" round (magus PR #29) that hardened the wire contract. Five points, and where the CLI stands on each:

| Contract point | CLI status |
| --- | --- |
| `hello` exactly once per connection; reconnect = new socket + `conversation.resume`; a second hello gets `already_initialized` | Already compliant — both front-ends send one hello per connection. |
| Any client `session_id` is ignored (the server routes by authenticated user id + its own conversation id); clients may keep sending it | No change — our `session_id` still correlates frames on this side. |
| Write-scoped tokens required: read-scoped → HTTP **403** `insufficient_scope`, invalid/expired → **401** | `chat.Dial` now maps both statuses to an actionable message (`dialErr`) instead of "expected handshake response status code 101 but got 403". |
| New error codes `not_ready`, `bad_frame`, `already_initialized`; 60s idle receive timeout with a ping at least once a minute | Already covered — generic error-frame handling surfaces any code, and the write loop pings every 25s. |
| **Inbound frames capped at 1MB; an oversize frame CLOSES the connection** (no error frame) | Enforced transport-side: `chat.Client.Send` runs every `mcp_result` through `FitMcpResult` (768KiB encoded budget), truncating `result.content` at a rune boundary and setting `result.truncated`, or failing closed with an `oversized_result` tool error. |

The frame budget is on the **encoded** size, which is the whole point: JSON escaping inflates content up to 6× (every control byte becomes a 6-byte `\u00XX`), so `magus chat`'s 256KiB raw ReadFile cap could still produce a ~1.5MB frame, and the ACP bridge forwarded editor reads with no cap at all. Enforcing at `Send` means neither front-end can forget; `internal/acp`'s executor applies the same fit at the producer so the invariant does not depend on which `CloudConn` is wired in.

## Known residual risks (2026-08-06 final review)

The final whole-branch review of `feat/chat-tui` closed four findings (server text is now sanitized before it reaches the terminal, audit-write failures are surfaced to the user, and the audit log's one verbatim server-supplied field is bounded). Three residuals were deliberately **parked** rather than fixed, and are recorded here so they are decisions rather than oversights.

### 1. Terminal type-ahead can pre-answer an approval prompt

`magus chat` reads the user's messages and their approval answers from one shared `bufio.Reader` over stdin. That sharing is correct and necessary — two readers over the same stream would have the first buffer ahead and swallow the approval line — but it does **not** make the approval prompt un-front-runnable, and the code comment that used to claim it did ("safe because the agent blocks on the tool call") was wrong.

The attack: a hostile (or compromised) server streams a convincing **fake** approval prompt as ordinary `text.delta` chat text. The user answers it. Their keystrokes sit in the **tty's own input queue** — below any buffering this process controls — until the **real** prompt that follows reads them, so the real prompt is answered by a human who never saw it.

Bounds, both of which have to hold for this to be worth anything to an attacker:

- **Fail-safe by default.** `TerminalApprover` treats only `a` (allow once) and `A` (allow always) as consent; every other byte, including empty input and EOF, denies. Type-ahead that is not exactly one of those two characters denies the call.
- **Small blast radius.** The only tool in the registry is `read_file`: read-only, confined to the already-approved `--root`, capped at 256 KiB, and written to `chat-audit.jsonl` either way. There is no write or exec tool to escalate into.
- **The convincing part is now harder.** Painting a byte-identical fake prompt needs terminal control sequences (clear screen, cursor home, line rewind). `sanitizeStream` (`internal/cli/sanitize.go`) renders ESC, the rest of C0, DEL, and the C1 range U+0080–U+009F as inert `\xNN`/`\u00NN` text at all four points server strings reach the terminal, so the forgery can no longer repaint the screen — only append plausible-looking text below the real output.

**Planned fix:** raw-mode terminal input in the deferred rich TUI, which flushes pending tty bytes (`tcflush`/`TCIFLUSH`) immediately before rendering an approval prompt, so nothing typed before the prompt existed can answer it. That needs `golang.org/x/term`, a module dependency this build deliberately does not add yet.

### 2. Outbound chat frames are unbudgeted

`chat.Client.Send` enforces the server's 1 MB inbound frame limit for `mcp_result` frames only (`FitMcpResult`, 768 KiB encoded budget). The `chat` frame carrying the user's own message is **not** budgeted. Per the server's wire contract an oversize frame **closes the connection with no error frame**, so pasting a >1 MB prompt ends the session with nothing on screen but `[connection closed]` — an unexplained failure the user cannot distinguish from a network drop.

Not a security issue (the user is the only one who can trigger it, on their own session) and not reachable by typing — it needs a paste or a piped stdin. **Follow-up:** a local length check on the outbound message with a clear "message too large" error, before the frame is sent.

### 3. Audit-log privacy modes are unix-only

`FileAudit` treats securing the log as part of recording: every `Record` re-applies `0o600` to the file and creates the parent directory `0o700`, and a failed `Chmod` is a `Record` error, not a warning. Those modes mean what they say on unix. On Windows, `os.Chmod` maps only to the read-only bit — the file's ACL is inherited from its parent directory — so `0o600` **does not** make the log owner-only there, and `MkdirAll(0o700)` likewise does not restrict the directory. magus ships a Windows binary, so this is live.

The log's contents are absolute local filesystem paths plus decisions, on a machine the user already controls; the exposure is to other local accounts on a shared Windows box. Left as-is rather than papered over, and stated here so the guarantee is not over-read. Closing it properly needs a build-tagged Windows path that sets a DACL (`golang.org/x/sys/windows`) — another dependency this build does not add. Note the same caveat already recorded for `syscall.O_NOFOLLOW` portability in the second review round above.

## Historical note (pre-build)

The server bridge was **unbuilt** at the time of the original audit. The CLI↔server contract findings (§2) and the plan-vs-reality drifts (§3) are therefore design-time corrections to the plan, not live defects against a running server. They should be resolved before, and verified during, implementation of the bridge.
