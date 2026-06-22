# ACP — Server-Side Plan (`magus` Phoenix app)

> **For agentic workers:** the skeleton-critical server work is **not new** — it is the existing chat server bridge plan. This document is the *ACP-specific* server plan: it (1) declares that bridge the spine, (2) proves it is front-end-agnostic, (3) gives an ACP-readiness verification checklist, and (4) specifies the one genuinely-new (deferred) delta — the `cancel` frame.

**Goal:** Ensure the `magus` cloud bridge serves an **ACP editor peer** identically to a terminal peer, and define the only server change ACP eventually wants beyond the chat skeleton (turn cancellation).

**Repo:** `/Users/daniel/Development/magus` (Phoenix 1.8, Bandit, Ash, Jido / `jido_ai` ReAct).

**Specs:** ACP adapter — `magus-cli/docs/superpowers/specs/2026-06-22-magus-acp-adapter-design.md`. Chat skeleton — `magus-cli/docs/superpowers/specs/2026-06-02-magus-chat-skeleton-design.md`.

**Depends on:** `magus-cli/docs/superpowers/plans/2026-06-02-magus-chat-server-bridge.md` (**"Plan 1"** below) — the 10-task TDD plan that builds the bridge. ACP adds **nothing** to Plan 1 for the skeleton.

---

## 1. The spine: what Plan 1 already delivers (reused verbatim)

Plan 1 builds the entire server half the ACP bridge needs. No part of it is ACP-specific; all of it is reused as-is:

| Plan 1 task | Delivers | Why ACP needs it |
|---|---|---|
| 1 | `websock_adapter` dep + `Magus.Cli.ConnectionRegistry` | per-connection routing for local tools |
| 2 | `Remote.ReadFile` proxy `Jido.Action` (self-timeout, fail-closed, terminal results) | the `read_file` the editor will service |
| 3 | `Remote.Catalog` (fixed name→module, drops unknown) | known-tool set, zero-trust |
| 4 | `Remote.Injection.augment/2` + Preflight wiring | per-turn, caller-scoped tool injection |
| 5 | `Dispatcher.build_signal_data/3` threads `caller_session_id` + `local_tools` | carries the editor connection's identity into the turn |
| 6 | `ChatSocket` `init` + `hello` (register, conversation, `server_hello`) | session setup the ACP `NewSession` maps onto |
| 7 | `ChatSocket` `mcp_call` push + `mcp_result` routing | the tool round-trip the editor fulfils |
| 8 | `ChatSocket` `chat` driving + PubSub→`chat_stream` mapping | streams the agent reply the editor renders |
| 9 | Upgrade controller + `/cli/chat` route (auth at upgrade) | the endpoint `magus acp` dials |
| 10 | Hibernation regression test + full verification | local tools never persist into base agent config |

**Action: execute Plan 1 unchanged.** When it is green, the ACP CLI (built per `2026-06-22-magus-acp-cli.md`) can run a real end-to-end smoke against it.

---

## 2. Why the bridge is front-end-agnostic (the key claim)

ACP requires no server changes because **the cloud never learns whether the local peer is a terminal TUI or an editor.** The contract is the wire protocol, and both front-ends speak it identically:

- The peer dials `/cli/chat`, authenticates the **same PAT** at the HTTP upgrade (Plan 1 Task 9). An editor-launched `magus acp` and an interactive `magus chat` present the identical `Authorization: Bearer` header.
- The peer sends the **same `hello`** advertising `local_tools: ["read_file"]`; the server intersects with the catalog and replies `server_hello` (Plan 1 Task 6). Identical for both.
- The peer sends the **same `chat`** frames; the server drives the `ConversationAgent` (Plan 1 Task 8). Identical.
- The server proposes tools via the **same `mcp_call`** and awaits the **same `mcp_result`** (Plan 1 Tasks 2, 7). Whether the result came from a terminal approval + local `File.read` (chat) or an editor `session/request_permission` + `fs/read_text_file` (acp) is **invisible** to the server — it sees only `{status, result}`.
- The server streams the **same `chat_stream`** events (Plan 1 Task 8). Whether they render in a Bubble Tea pane or as ACP `session/update` notifications is the peer's concern.

The CLI's §7 security model (editor as the trusted local party) lives **entirely peer-side**. The server stays a policy-agnostic relay (chat skeleton §5.3) for both.

**Conclusion:** the only correct server action for the ACP skeleton is "build Plan 1." This plan adds verification (§3) and one deferred delta (§4).

---

## 3. ACP-readiness verification checklist

After Plan 1 is green, add **one** server test asserting the bridge behaves identically for an ACP-shaped peer. This is the only net-new test this plan mandates for the skeleton; everything else is Plan 1's own suite.

**File:** `test/magus_web/cli/chat_socket_acp_parity_test.exs` (create).

- [ ] **Step 1: Write the parity test**

The ACP bridge sends byte-identical frames to `magus chat` (it is the same wire protocol). Assert the server processes an "ACP-style" exchange with no special-casing: a `hello` advertising `read_file`, a `chat`, and an `mcp_result` whose `result` came from an editor (the server cannot tell). Reuse the harness style of Plan 1 Tasks 6-8.

```elixir
# test/magus_web/cli/chat_socket_acp_parity_test.exs
defmodule MagusWeb.Cli.ChatSocketAcpParityTest do
  use Magus.DataCase, async: true
  import Magus.Generators

  alias MagusWeb.Cli.ChatSocket

  @registry Magus.Cli.ConnectionRegistry

  defp initial_state(user),
    do: %{user: user, session_id: nil, conversation_id: nil, accepted_tools: [], pending: %{}}

  test "an ACP peer's hello is accepted identically to a terminal peer's" do
    user = generate(user())
    {:ok, state} = ChatSocket.init(initial_state(user))
    sid = "acp-#{System.unique_integer([:positive])}"

    frame =
      Jason.encode!(%{
        "type" => "hello",
        "v" => 1,
        "session_id" => sid,
        # An editor advertises the SAME capability shape as the TUI.
        "capabilities" => %{"local_tools" => ["read_file"]},
        "conversation" => %{"new" => true}
      })

    assert {:push, {:text, json}, new_state} =
             ChatSocket.handle_in({frame, [opcode: :text]}, state)

    reply = Jason.decode!(json)
    assert reply["type"] == "server_hello"
    assert reply["accepted_tools"] == ["read_file"]
    assert is_binary(reply["conversation_id"])
    assert [{pid, _}] = Registry.lookup(@registry, sid)
    assert pid == self()
    assert new_state.conversation_id == reply["conversation_id"]
  end

  test "an mcp_result is routed back regardless of how the peer produced it" do
    # The server cannot distinguish an editor's fs/read_text_file result from a
    # terminal File.read result — both arrive as {status:"ok", result:{content}}.
    waiter = self()
    state = %{initial_state(nil) | session_id: "acp-x", conversation_id: "c", pending: %{"call-1" => waiter}}

    frame =
      Jason.encode!(%{
        "type" => "mcp_result",
        "v" => 1,
        "call_id" => "call-1",
        "status" => "ok",
        "result" => %{"content" => "defmodule App"}
      })

    assert {:ok, new_state} = ChatSocket.handle_in({frame, [opcode: :text]}, state)
    assert_receive {:mcp_result, "call-1", "ok", %{"content" => "defmodule App"}}
    refute Map.has_key?(new_state.pending, "call-1")
  end
end
```

- [ ] **Step 2: Run it**

Run: `mix test test/magus_web/cli/chat_socket_acp_parity_test.exs`
Expected: PASS — **with no production code changes.** If it requires *any* change to `ChatSocket` to pass, that change is a bug in the front-end-agnostic claim; investigate before proceeding.

- [ ] **Step 3: Commit**

```bash
git commit -am "test(chat): ACP-peer parity — bridge is front-end agnostic"
```

**Manual cross-check:** with the dev server running, point `magus acp` (from the CLI plan) at it via Zed and confirm a `read_file` round-trip completes — the same server, exercised by an editor instead of a terminal.

---

## 4. Deferred delta: the `cancel` frame (only new server work ACP wants)

The chat skeleton has **no** cancel path; the ACP spec (§13) defers `session/cancel` accordingly. This is the **one** server change ACP would add later — specified here so it is ready, but **out of scope for the skeleton.** Build only when interruptible turns are wanted (e.g. alongside `exec_command`/long reads).

**Wire addition:** `cancel` (peer → server), `{type:"cancel", v:1, session_id}`. No response (best-effort).

**Files (when built):**
- Modify: `lib/magus_web/cli/chat_socket.ex` — add a `handle_in` clause for `"cancel"`.
- Test: `test/magus_web/cli/chat_socket_cancel_test.exs`.

**Design (verify exact symbols against the `magus` repo before implementing):**

- [ ] **Step 1 — Write the failing test:** a `cancel` frame for the connection's conversation triggers the agent's interrupt path and replies to (or abandons) any pending `mcp_call` waiters as cancelled.

```elixir
# test/magus_web/cli/chat_socket_cancel_test.exs (sketch — pin agent-interrupt API first)
defmodule MagusWeb.Cli.ChatSocketCancelTest do
  use Magus.DataCase, async: false
  alias MagusWeb.Cli.ChatSocket

  test "cancel frame requests interruption of the current turn" do
    # Arrange a state with a conversation + a pending mcp_call waiter; send cancel;
    # assert the pending waiter is notified/cleared and the agent interrupt is invoked.
    # The exact interrupt assertion depends on the Jido cancel API (see Step 3).
  end
end
```

- [ ] **Step 2 — Run it:** Expected FAIL (no `"cancel"` clause).

- [ ] **Step 3 — Implement.** Add to `ChatSocket.handle_in`:

```elixir
  defp handle_cancel(_msg, %{conversation_id: conv_id} = state) when is_binary(conv_id) do
    # VERIFY in the magus repo which is correct:
    #   (a) a Jido signal such as `ai.react.cancel` dispatched to the conversation agent, or
    #   (b) `Magus.Agents.<...>.cancel/halt` on the running turn, or
    #   (c) the existing mid-turn hibernation / `Magus.Agents.Recovery` interrupt.
    # Then: fail any pending mcp_call waiters as cancelled so blocked proxies unwind.
    for {_call_id, waiter} <- state.pending, do: send(waiter, {:cancelled})
    # ...invoke the verified agent-interrupt path here...
    {:ok, %{state | pending: %{}}}
  end

  defp handle_cancel(_msg, state), do: {:ok, state}
```

and route it in `handle_in`: `{:ok, %{"type" => "cancel"} = msg} -> handle_cancel(msg, state)`.

> The `Remote.ReadFile` proxy (Plan 1 Task 2) already returns terminal errors on `:DOWN`/timeout and **never raises**; extend its `receive` to also handle a `{:cancelled}` message → terminal `{:ok, %{error: "cancelled"}}`, so a cancel during an in-flight tool unwinds cleanly. Confirm against `lib/magus/agents/tools/remote/read_file.ex`.

- [ ] **Step 4 — Run it:** Expected PASS.
- [ ] **Step 5 — Commit:** `git commit -am "feat(chat): cancel frame interrupts the current turn"`.

**CLI counterpart (separate, in `magus-cli`):** wire ACP `session/cancel` → send a `cancel` frame; this is a follow-up to the ACP CLI plan, not part of its skeleton.

---

## 5. Key code references (in the `magus` repo)

From Plan 1's verification — reuse, do not re-derive:
- `lib/magus_web/cli/chat_socket.ex` — the bridge handler (cancel clause goes here).
- `lib/magus/agents/tools/remote/read_file.ex` — the proxy (extend its `receive` for cancel).
- `lib/magus/agents/conversation_agent.ex`, `lib/magus/agents/strategies/react/runner.ex` — the turn/agent internals to inspect for the **interrupt API** (§4 Step 3).
- `lib/magus/agents/recovery*` / mid-turn hibernation — the existing interruption machinery to reuse for cancel.

---

## 6. Summary

- **Skeleton:** execute **Plan 1** unchanged (server work = done there). Add the **§3 parity test** (asserts zero ACP-specific server code). That is the entire server-side requirement for the ACP walking skeleton.
- **Later:** the **§4 cancel delta** is the only net-new server change ACP introduces, and it is deferred until interruptible turns are needed.
