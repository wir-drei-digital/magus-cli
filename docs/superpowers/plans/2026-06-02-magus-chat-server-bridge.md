# magus chat — Server Bridge Implementation Plan (Plan 1 of 2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `magus` (Phoenix) half of the chat walking skeleton: a token-authenticated WebSocket endpoint that drives a `ConversationAgent`, registers a `read_file` proxy tool per turn (caller-scoped, multiplayer-correct), round-trips `mcp_call`/`mcp_result` to the connected client, and streams agent output back.

**Architecture:** A raw WebSocket (`WebSock`/Bandit) handler authenticates the PAT at the HTTP upgrade (reusing `ApiTokenAuthPlug`), creates/loads a single-owner `Conversation`, and registers itself in a `Registry` keyed by the client's `session_id`. Each user turn is driven via `Chat.send_user_message/2` carrying `caller_session_id` + `local_tools` in `message.metadata`; a small additive change to `Preflight` (via a pure `Remote.Injection` helper) **appends** the resolved local-tool modules to the agent's normal toolset and merges the caller identity into the per-turn tool context. The `Remote.ReadFile` proxy's `run/2` resolves the handler pid from the Registry and does a `send`/`receive` round-trip with a self-enforced timeout, returning terminal (non-retryable) results. The handler subscribes to `agents:{conversation_id}` and maps PubSub signals to `chat_stream` frames.

**Tech Stack:** Elixir, Phoenix 1.8 (Bandit adapter), `websock_adapter`, Ash, Jido / `jido_ai` ReAct strategy, Phoenix.PubSub, ExUnit + Mox.

**Repo:** All work is in `/Users/daniel/Development/magus`. (This plan doc lives in `magus-cli/docs/superpowers/plans/` as the planning hub, per the spec.)

**Spec:** `magus-cli/docs/superpowers/specs/2026-06-02-magus-chat-skeleton-design.md`.

---

## Wire protocol (frames this plan handles)

Server-relevant frames (JSON text, `type` + `v:1`):
- **in** `hello` — `{session_id, capabilities:{local_tools:[names]}, conversation:{new}|{resume:id}}`
- **out** `server_hello` — `{conversation_id, accepted_tools:[names], server_version}`
- **in** `chat` — `{session_id, text}`
- **out** `chat_stream` — `{event, data}` (`text.delta|text.done|tool.start|tool.complete|turn.done|error`)
- **out** `mcp_call` — `{call_id, tool_name, params}`
- **in** `mcp_result` — `{call_id, status:"ok"|"error"|"denied", result|error}`

## File structure

| File | Responsibility |
|---|---|
| `mix.exs` (modify) | add `{:websock_adapter, "~> 0.5"}` |
| `lib/magus/application.ex` (modify) | start `{Registry, keys: :unique, name: Magus.Cli.ConnectionRegistry}` |
| `lib/magus/agents/tools/remote/read_file.ex` (create) | proxy `Jido.Action`; `send`/`receive` round-trip; self-timeout; terminal results |
| `lib/magus/agents/tools/remote/catalog.ex` (create) | fixed name→module catalog; `resolve/1` drops unknown (zero-trust) |
| `lib/magus/agents/tools/remote/injection.ex` (create) | pure `augment/2`: append local tools + merge `caller_session_id` |
| `lib/magus/agents/plugins/support/preflight.ex` (modify) | one pipe: `\|> Injection.augment(data)` |
| `lib/magus/agents/dispatcher.ex` (modify) | pass `caller_session_id`/`local_tools` from `message.metadata` into signal data |
| `lib/magus_web/cli/chat_socket.ex` (create) | `WebSock` handler: hello / chat / mcp_result / broadcast→chat_stream / mcp_call push |
| `lib/magus_web/cli/chat_socket_controller.ex` (create) | upgrade action (`WebSockAdapter.upgrade`) |
| `lib/magus_web/router.ex` (modify) | `:cli_socket` pipeline + `/cli/chat` route |

Tests mirror under `test/`.

---

## Task 1: Add `websock_adapter` dep + the connection Registry

**Files:**
- Modify: `mix.exs`
- Modify: `lib/magus/application.ex`
- Test: `test/magus/cli/connection_registry_test.exs`

- [ ] **Step 1: Write the failing test**

```elixir
# test/magus/cli/connection_registry_test.exs
defmodule Magus.Cli.ConnectionRegistryTest do
  use ExUnit.Case, async: true

  @registry Magus.Cli.ConnectionRegistry

  test "a process can register under a session id and be looked up" do
    sid = "sess-#{System.unique_integer([:positive])}"
    {:ok, _} = Registry.register(@registry, sid, nil)
    assert [{pid, _}] = Registry.lookup(@registry, sid)
    assert pid == self()
  end

  test "unknown session id resolves to empty (fail-closed)" do
    assert Registry.lookup(@registry, "nope-#{System.unique_integer()}") == []
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mix test test/magus/cli/connection_registry_test.exs`
Expected: FAIL — `ArgumentError: unknown registry: Magus.Cli.ConnectionRegistry`.

- [ ] **Step 3: Add the dependency**

In `mix.exs`, add to `deps/0` (alongside `{:bandit, "~> 1.5"}`):

```elixir
{:websock_adapter, "~> 0.5"},
```

- [ ] **Step 4: Start the Registry in the supervision tree**

In `lib/magus/application.ex`, add to the `children` list (near the other infra children, e.g. after the PubSub entry):

```elixir
{Registry, keys: :unique, name: Magus.Cli.ConnectionRegistry},
```

- [ ] **Step 5: Fetch deps and run the test**

Run: `mix deps.get && mix test test/magus/cli/connection_registry_test.exs`
Expected: PASS (2 tests).

- [ ] **Step 6: Commit**

```bash
git add mix.exs mix.lock lib/magus/application.ex test/magus/cli/connection_registry_test.exs
git commit -m "feat(chat): add websock_adapter dep and CLI connection registry"
```

---

## Task 2: `Remote.ReadFile` proxy action

**Files:**
- Create: `lib/magus/agents/tools/remote/read_file.ex`
- Test: `test/magus/agents/tools/remote/read_file_test.exs`

> Contract facts (verified): the ReAct runner invokes `run(params, context)` inside a `Task` with **`timeout: :infinity`** (no enforced wall-clock) and retries **only** `{:error, %{type: :timeout|:exception|:execution_error}}`. So this proxy self-enforces its timeout, **never raises**, and returns every failure as a terminal `{:ok, %{error: ...}}`. Params arrive string-keyed from the LLM; context uses atom keys (`Helpers.get_param/2` and `validate_context/2` handle both).

- [ ] **Step 1: Write the failing test**

```elixir
# test/magus/agents/tools/remote/read_file_test.exs
defmodule Magus.Agents.Tools.Remote.ReadFileTest do
  use ExUnit.Case, async: false  # mutates Application env in the timeout case
  alias Magus.Agents.Tools.Remote.ReadFile

  @registry Magus.Cli.ConnectionRegistry

  # Spawns a process that registers under `sid` and runs `reply_fun.(from, call_id)`
  # when it receives the mcp_call. Returns once registration is confirmed.
  defp stub_handler(sid, reply_fun) do
    test = self()

    spawn(fn ->
      {:ok, _} = Registry.register(@registry, sid, nil)
      send(test, :registered)

      receive do
        {:mcp_call, call_id, tool, params, from} ->
          send(test, {:got_call, tool, params})
          reply_fun.(from, call_id)
      end
    end)

    assert_receive :registered, 1_000
  end

  test "returns content on an ok result" do
    sid = "s-#{System.unique_integer([:positive])}"
    stub_handler(sid, fn from, call_id ->
      send(from, {:mcp_result, call_id, "ok", %{"content" => "hello\nworld"}})
    end)

    assert {:ok, %{content: "hello\nworld", path: "a.txt"}} =
             ReadFile.run(%{"path" => "a.txt"}, %{caller_session_id: sid})

    assert_receive {:got_call, "read_file", %{path: "a.txt"}}
  end

  test "no live connection is a terminal ok-wrapped error (not retryable)" do
    assert {:ok, %{error: msg}} =
             ReadFile.run(%{"path" => "a.txt"}, %{caller_session_id: "missing"})

    assert msg =~ "No active local connection"
  end

  test "denied maps to a terminal error" do
    sid = "s-#{System.unique_integer([:positive])}"
    stub_handler(sid, fn from, call_id -> send(from, {:mcp_result, call_id, "denied", %{}}) end)

    assert {:ok, %{error: msg}} = ReadFile.run(%{"path" => "secret"}, %{caller_session_id: sid})
    assert msg =~ "denied"
  end

  test "missing caller_session_id is terminal" do
    assert {:ok, %{error: _}} = ReadFile.run(%{"path" => "a.txt"}, %{})
  end

  test "times out when the handler never replies" do
    Application.put_env(:magus, :remote_tool_timeout_ms, 50)
    on_exit(fn -> Application.delete_env(:magus, :remote_tool_timeout_ms) end)

    sid = "s-#{System.unique_integer([:positive])}"
    stub_handler(sid, fn _from, _call_id -> Process.sleep(1_000) end)

    assert {:ok, %{error: msg}} = ReadFile.run(%{"path" => "a.txt"}, %{caller_session_id: sid})
    assert msg =~ "Timed out"
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mix test test/magus/agents/tools/remote/read_file_test.exs`
Expected: FAIL — `module Magus.Agents.Tools.Remote.ReadFile is not available`.

- [ ] **Step 3: Write the implementation**

```elixir
# lib/magus/agents/tools/remote/read_file.ex
defmodule Magus.Agents.Tools.Remote.ReadFile do
  @moduledoc """
  Reverse-tunnel proxy tool: reads a file on the *caller's* local machine.

  `run/2` resolves the caller's WebSocket handler from the connection registry
  (by `caller_session_id`, never by conversation), then does a synchronous
  `send`/`receive` round-trip with a self-enforced timeout. The runner enforces
  no wall-clock timeout, so we bound it here. All failures are returned as
  terminal `{:ok, %{error: ...}}` (never raised, never `type: :timeout/:exception`)
  so the ReAct loop does not retry a denied/absent/timed-out call.
  """

  use Jido.Action,
    name: "read_file",
    description: """
    Read the contents of a file on the user's local machine. The user may be
    prompted to approve access and can deny it. Provide an absolute path or a
    path relative to the user's working directory.
    """,
    schema: [
      path: [type: :string, required: true, doc: "File path to read"]
    ]

  import Magus.Agents.Tools.Helpers, only: [validate_context: 2, get_param: 2]

  @registry Magus.Cli.ConnectionRegistry

  def display_name, do: "Reading local file..."
  def summarize_output(%{content: c}) when is_binary(c), do: "#{c |> String.split("\n") |> length()} lines"
  def summarize_output(%{error: _}), do: "Error"
  def summarize_output(_), do: "Completed"

  @impl true
  def run(params, context) do
    case validate_context(context, [:caller_session_id]) do
      {:ok, ctx} -> round_trip(ctx.caller_session_id, get_param(params, :path))
      {:error, message} -> {:ok, %{error: message}}
    end
  end

  defp round_trip(session_id, path) do
    case Registry.lookup(@registry, session_id) do
      [{handler, _} | _] ->
        do_call(handler, path)

      [] ->
        {:ok,
         %{
           error: "No active local connection for this session.",
           hint: "The user's local agent is not connected; cannot read files now."
         }}
    end
  end

  defp do_call(handler, path) do
    call_id = Ecto.UUID.generate()
    ref = Process.monitor(handler)
    send(handler, {:mcp_call, call_id, "read_file", %{path: path}, self()})

    receive do
      {:mcp_result, ^call_id, "ok", result} ->
        Process.demonitor(ref, [:flush])
        {:ok, %{path: path, content: pick(result, "content"), truncated: pick(result, "truncated") || false}}

      {:mcp_result, ^call_id, "denied", _result} ->
        Process.demonitor(ref, [:flush])
        {:ok, %{error: "User denied access to #{path}.", hint: "Ask the user to approve, or choose another file."}}

      {:mcp_result, ^call_id, "error", result} ->
        Process.demonitor(ref, [:flush])
        {:ok, %{error: "Could not read #{path}: #{pick(result, "message") || "read failed"}"}}

      {:DOWN, ^ref, :process, ^handler, _reason} ->
        {:ok, %{error: "Local connection dropped before #{path} could be read."}}
    after
      timeout_ms() ->
        Process.demonitor(ref, [:flush])
        {:ok, %{error: "Timed out reading #{path} from the local machine."}}
    end
  end

  defp pick(map, key), do: Map.get(map, key) || Map.get(map, String.to_atom(key))
  defp timeout_ms, do: Application.get_env(:magus, :remote_tool_timeout_ms, 30_000)
end
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mix test test/magus/agents/tools/remote/read_file_test.exs`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add lib/magus/agents/tools/remote/read_file.ex test/magus/agents/tools/remote/read_file_test.exs
git commit -m "feat(chat): add read_file reverse-tunnel proxy tool"
```

---

## Task 3: `Remote.Catalog` — fixed name→module catalog

**Files:**
- Create: `lib/magus/agents/tools/remote/catalog.ex`
- Test: `test/magus/agents/tools/remote/catalog_test.exs`

- [ ] **Step 1: Write the failing test**

```elixir
# test/magus/agents/tools/remote/catalog_test.exs
defmodule Magus.Agents.Tools.Remote.CatalogTest do
  use ExUnit.Case, async: true
  alias Magus.Agents.Tools.Remote.{Catalog, ReadFile}

  test "names lists the known tools" do
    assert Catalog.names() == ["read_file"]
  end

  test "known?/1 distinguishes catalog members" do
    assert Catalog.known?("read_file")
    refute Catalog.known?("exec_command")
  end

  test "resolve maps known names to modules and drops unknown ones" do
    assert Catalog.resolve(["read_file", "rm -rf /"]) == [ReadFile]
    assert Catalog.resolve([]) == []
    assert Catalog.resolve("not-a-list") == []
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mix test test/magus/agents/tools/remote/catalog_test.exs`
Expected: FAIL — `module Magus.Agents.Tools.Remote.Catalog is not available`.

- [ ] **Step 3: Write the implementation**

```elixir
# lib/magus/agents/tools/remote/catalog.ex
defmodule Magus.Agents.Tools.Remote.Catalog do
  @moduledoc """
  The fixed, known set of local (reverse-tunnel) tools the cloud may propose.

  The capability set is finite and reviewed on both ends. Names that are not in
  the catalog are dropped — the server never invents a capability from the wire
  (zero-trust). New tool *kinds* require a deploy + a CLI release.
  """

  alias Magus.Agents.Tools.Remote.ReadFile

  @known %{"read_file" => ReadFile}

  @spec known() :: %{optional(String.t()) => module()}
  def known, do: @known

  @spec names() :: [String.t()]
  def names, do: Map.keys(@known)

  @spec known?(String.t()) :: boolean()
  def known?(name), do: Map.has_key?(@known, name)

  @spec resolve([String.t()]) :: [module()]
  def resolve(names) when is_list(names) do
    names |> Enum.map(&Map.get(@known, &1)) |> Enum.reject(&is_nil/1) |> Enum.uniq()
  end

  def resolve(_), do: []
end
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mix test test/magus/agents/tools/remote/catalog_test.exs`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add lib/magus/agents/tools/remote/catalog.ex test/magus/agents/tools/remote/catalog_test.exs
git commit -m "feat(chat): add fixed remote-tool catalog"
```

---

## Task 4: `Remote.Injection` (+ wire into Preflight) — augment, don't replace

**Files:**
- Create: `lib/magus/agents/tools/remote/injection.ex`
- Modify: `lib/magus/agents/plugins/support/preflight.ex:114`
- Test: `test/magus/agents/tools/remote/injection_test.exs`

> Why a pure helper: the agent's normal toolset is assembled in `Preflight.build_react_signal` as `request_context.tools`. Local tools must **augment** that list (full agent power + local tools), not replace it (a wholesale `data[:tools]` override would strip the agent's normal tools). Extracting the logic into a pure `augment/2` keeps it unit-testable and reduces the Preflight change to one line.

- [ ] **Step 1: Write the failing test**

```elixir
# test/magus/agents/tools/remote/injection_test.exs
defmodule Magus.Agents.Tools.Remote.InjectionTest do
  use ExUnit.Case, async: true
  alias Magus.Agents.Tools.Remote.{Injection, ReadFile}

  test "appends resolved local tools to the existing toolset and merges caller id" do
    signal = %{tools: [SomeAgentTool], tool_context: %{user_id: "u1", conversation_id: "c1"}}
    out = Injection.augment(signal, %{local_tools: ["read_file"], caller_session_id: "s1"})

    assert SomeAgentTool in out.tools
    assert ReadFile in out.tools
    assert out.tool_context == %{user_id: "u1", conversation_id: "c1", caller_session_id: "s1"}
  end

  test "is a no-op without local tools or caller session" do
    signal = %{tools: [SomeAgentTool]}
    assert Injection.augment(signal, %{}) == signal
  end

  test "sets tools/context when none existed yet" do
    out = Injection.augment(%{}, %{local_tools: ["read_file"], caller_session_id: "s1"})
    assert out.tools == [ReadFile]
    assert out.tool_context == %{caller_session_id: "s1"}
  end

  test "drops unknown tool names but still records the caller session" do
    out = Injection.augment(%{tools: []}, %{local_tools: ["exec_command"], caller_session_id: "s1"})
    assert out.tools == []
    assert out.tool_context == %{caller_session_id: "s1"}
  end

  test "reads string-keyed data too" do
    out = Injection.augment(%{}, %{"local_tools" => ["read_file"], "caller_session_id" => "s1"})
    assert out.tools == [ReadFile]
    assert out.tool_context == %{caller_session_id: "s1"}
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mix test test/magus/agents/tools/remote/injection_test.exs`
Expected: FAIL — `module Magus.Agents.Tools.Remote.Injection is not available`.

- [ ] **Step 3: Write the implementation**

```elixir
# lib/magus/agents/tools/remote/injection.ex
defmodule Magus.Agents.Tools.Remote.Injection do
  @moduledoc """
  Augments an `ai.react.query` signal map with per-turn local tools and the
  caller's connection identity — WITHOUT replacing the agent's normal toolset.

  Appends resolved local-tool modules to `:tools` (uniq) and merges
  `caller_session_id` into `:tool_context`. Both `run_tools` and
  `run_tool_context` are per-turn in the strategy and cleared on completion, so
  this never leaks into other turns or into a hibernated/thawed agent.
  """

  alias Magus.Agents.Tools.Remote.Catalog

  @spec augment(map(), map()) :: map()
  def augment(signal, data) when is_map(signal) and is_map(data) do
    signal
    |> append_local_tools(get(data, :local_tools) || [])
    |> merge_caller_session(get(data, :caller_session_id))
  end

  defp append_local_tools(signal, names) do
    case Catalog.resolve(names) do
      [] -> signal
      mods -> Map.update(signal, :tools, mods, fn existing -> Enum.uniq((existing || []) ++ mods) end)
    end
  end

  defp merge_caller_session(signal, nil), do: signal

  defp merge_caller_session(signal, sid) do
    Map.update(signal, :tool_context, %{caller_session_id: sid}, fn ctx ->
      Map.put(ctx || %{}, :caller_session_id, sid)
    end)
  end

  defp get(data, key), do: Map.get(data, key) || Map.get(data, to_string(key))
end
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mix test test/magus/agents/tools/remote/injection_test.exs`
Expected: PASS (5 tests).

- [ ] **Step 5: Wire it into Preflight (one line)**

In `lib/magus/agents/plugins/support/preflight.ex`, in `build_react_signal/*`, the `react_signal` pipeline currently ends (around line 113-114):

```elixir
            |> maybe_put_runtime_field(:model_name, data)
            |> then(&Jido.Signal.new!("ai.react.query", &1))
```

Insert the augment step immediately before the `then(...)`:

```elixir
            |> maybe_put_runtime_field(:model_name, data)
            |> Magus.Agents.Tools.Remote.Injection.augment(data)
            |> then(&Jido.Signal.new!("ai.react.query", &1))
```

- [ ] **Step 6: Verify compile + format + full Preflight tests still pass**

Run: `mix compile --warnings-as-errors && mix format --check-formatted && mix test test/magus/agents/plugins/support/`
Expected: PASS (compiles clean; existing Preflight tests unaffected — `augment(signal, %{})` is a no-op for normal turns).

- [ ] **Step 7: Commit**

```bash
git add lib/magus/agents/tools/remote/injection.ex test/magus/agents/tools/remote/injection_test.exs lib/magus/agents/plugins/support/preflight.ex
git commit -m "feat(chat): augment agent toolset with caller-scoped local tools"
```

---

## Task 5: `Dispatcher.build_signal_data/3` — carry caller id + local tools from metadata

**Files:**
- Modify: `lib/magus/agents/dispatcher.ex` (`build_signal_data/3`, ~lines 56-77)
- Test: `test/magus/agents/dispatcher_local_tools_test.exs`

> The driver (Task 8) puts `caller_session_id` + `local_tools` into `message.metadata`. `build_signal_data/3` builds the `message.user` signal data; Preflight (Task 4) reads `local_tools`/`caller_session_id` off that data. This task threads the two keys through. `build_signal_data/3` is `@doc false` but public and pure over its inputs.

- [ ] **Step 1: Write the failing test**

```elixir
# test/magus/agents/dispatcher_local_tools_test.exs
defmodule Magus.Agents.DispatcherLocalToolsTest do
  use ExUnit.Case, async: true

  alias Magus.Agents.Dispatcher
  alias Magus.Chat.{Conversation, Message}

  defp message(metadata) do
    %Message{
      id: Ecto.UUID.generate(),
      text: "hi",
      mode: :chat,
      selected_model_id: nil,
      attachments: [],
      metadata: metadata
    }
  end

  defp conversation, do: %Conversation{chat_mode: :chat}
  defp routed, do: %{routing_reason: nil, model_keys: %{}}

  test "threads caller_session_id and local_tools from metadata into signal data" do
    data =
      Dispatcher.build_signal_data(
        message(%{"caller_session_id" => "s1", "local_tools" => ["read_file"]}),
        conversation(),
        routed()
      )

    assert data[:caller_session_id] == "s1"
    assert data[:local_tools] == ["read_file"]
  end

  test "omits the local-tool keys when metadata has none" do
    data = Dispatcher.build_signal_data(message(%{}), conversation(), routed())
    refute Map.has_key?(data, :caller_session_id)
    refute Map.has_key?(data, :local_tools)
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mix test test/magus/agents/dispatcher_local_tools_test.exs`
Expected: FAIL — assertions on `:caller_session_id`/`:local_tools` fail (keys absent).

- [ ] **Step 3: Implement the pass-through**

In `lib/magus/agents/dispatcher.ex`, `build_signal_data/3` ends by returning a map (with `metadata = message.metadata || %{}` already in scope). Add a final pipe through a new private helper. Change the end of the function from:

```elixir
    %{
      message_id: to_string(message.id),
      text: message.text,
      # ... existing fields ...
      brain_page_id: metadata["brain_page_id"] || metadata[:brain_page_id]
    }
  end
```

to:

```elixir
    %{
      message_id: to_string(message.id),
      text: message.text,
      # ... existing fields ...
      brain_page_id: metadata["brain_page_id"] || metadata[:brain_page_id]
    }
    |> put_local_tools(metadata)
  end

  defp put_local_tools(data, metadata) do
    case metadata["caller_session_id"] || metadata[:caller_session_id] do
      nil ->
        data

      session_id ->
        Map.merge(data, %{
          caller_session_id: session_id,
          local_tools: metadata["local_tools"] || metadata[:local_tools] || []
        })
    end
  end
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mix test test/magus/agents/dispatcher_local_tools_test.exs`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add lib/magus/agents/dispatcher.ex test/magus/agents/dispatcher_local_tools_test.exs
git commit -m "feat(chat): thread caller session + local tools through dispatcher"
```

---

## Task 6: `ChatSocket` — init + `hello` (register, conversation, server_hello)

**Files:**
- Create: `lib/magus_web/cli/chat_socket.ex`
- Test: `test/magus_web/cli/chat_socket_hello_test.exs`

> The handler is a `WebSock` behaviour module. Its callbacks (`init/1`, `handle_in/2`, `handle_info/2`, `terminate/2`) are plain functions we unit-test by calling them directly with a constructed state. `Registry` auto-unregisters on process death, so `terminate/2` need not unregister.

- [ ] **Step 1: Write the failing test**

```elixir
# test/magus_web/cli/chat_socket_hello_test.exs
defmodule MagusWeb.Cli.ChatSocketHelloTest do
  use Magus.DataCase, async: true
  import Magus.Generators

  alias MagusWeb.Cli.ChatSocket

  @registry Magus.Cli.ConnectionRegistry

  defp initial_state(user), do: %{user: user, session_id: nil, conversation_id: nil, accepted_tools: []}

  defp hello_frame(session_id, tools, conversation \\ %{"new" => true}) do
    Jason.encode!(%{
      "type" => "hello",
      "v" => 1,
      "session_id" => session_id,
      "capabilities" => %{"local_tools" => tools},
      "conversation" => conversation
    })
  end

  test "hello creates a conversation, registers the session, and replies server_hello" do
    user = generate(user())
    sid = "s-#{System.unique_integer([:positive])}"

    {:ok, state} = ChatSocket.init(initial_state(user))

    assert {:push, {:text, json}, new_state} =
             ChatSocket.handle_in({hello_frame(sid, ["read_file", "exec_command"]), [opcode: :text]}, state)

    reply = Jason.decode!(json)
    assert reply["type"] == "server_hello"
    assert is_binary(reply["conversation_id"])
    # unknown tools are dropped (zero-trust): only read_file is accepted
    assert reply["accepted_tools"] == ["read_file"]

    assert new_state.conversation_id == reply["conversation_id"]
    assert [{pid, _}] = Registry.lookup(@registry, sid)
    assert pid == self()
  end

  test "resume of a conversation the user does not own is rejected" do
    owner = generate(user())
    other = generate(user())
    {:ok, conv} = Magus.Chat.create_conversation(%{chat_mode: :chat}, actor: owner)

    {:ok, state} = ChatSocket.init(initial_state(other))
    sid = "s-#{System.unique_integer([:positive])}"

    assert {:push, {:text, json}, _state} =
             ChatSocket.handle_in(
               {hello_frame(sid, ["read_file"], %{"resume" => conv.id}), [opcode: :text]},
               state
             )

    assert Jason.decode!(json)["type"] == "error"
    assert Registry.lookup(@registry, sid) == []
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mix test test/magus_web/cli/chat_socket_hello_test.exs`
Expected: FAIL — `module MagusWeb.Cli.ChatSocket is not available`.

- [ ] **Step 3: Write the implementation**

```elixir
# lib/magus_web/cli/chat_socket.ex
defmodule MagusWeb.Cli.ChatSocket do
  @moduledoc """
  Raw WebSocket handler for `magus chat`. Authenticated at the HTTP upgrade
  (see ChatSocketController). One connection = one process = one identity; local
  tools and routing follow this connection, never the conversation.
  """

  @behaviour WebSock

  alias Magus.Agents.Tools.Remote.Catalog

  @registry Magus.Cli.ConnectionRegistry

  @impl true
  def init(state) do
    # state seeded by the controller: %{user: %User{}, token: %ApiToken{}}
    {:ok, Map.merge(%{session_id: nil, conversation_id: nil, accepted_tools: [], pending: %{}}, state)}
  end

  @impl true
  def handle_in({text, [opcode: :text]}, state) do
    case Jason.decode(text) do
      {:ok, %{"type" => "hello"} = msg} -> handle_hello(msg, state)
      {:ok, %{"type" => "chat"} = msg} -> handle_chat(msg, state)
      {:ok, %{"type" => "mcp_result"} = msg} -> handle_mcp_result(msg, state)
      {:ok, _other} -> {:ok, state}
      {:error, _} -> {:push, error_frame("bad_frame", "Could not decode frame"), state}
    end
  end

  def handle_in(_other, state), do: {:ok, state}

  # --- hello -------------------------------------------------------------

  defp handle_hello(msg, state) do
    session_id = msg["session_id"]
    advertised = get_in(msg, ["capabilities", "local_tools"]) || []
    accepted = Enum.filter(advertised, &Catalog.known?/1)

    case resolve_conversation(msg["conversation"], state.user) do
      {:ok, conversation} ->
        {:ok, _} = Registry.register(@registry, session_id, nil)
        Phoenix.PubSub.subscribe(Magus.PubSub, "agents:#{conversation.id}")

        state = %{state | session_id: session_id, conversation_id: conversation.id, accepted_tools: accepted}

        frame =
          Jason.encode!(%{
            "type" => "server_hello",
            "v" => 1,
            "conversation_id" => conversation.id,
            "accepted_tools" => accepted,
            "server_version" => to_string(Application.spec(:magus, :vsn))
          })

        {:push, {:text, frame}, state}

      {:error, _reason} ->
        {:push, error_frame("forbidden", "Conversation not found or not yours"), state}
    end
  end

  defp resolve_conversation(%{"resume" => id}, user) when is_binary(id) do
    Magus.Chat.get_conversation(id, actor: user)
  end

  defp resolve_conversation(_new, user) do
    Magus.Chat.create_conversation(%{chat_mode: :chat}, actor: user)
  end

  # --- chat / mcp_result : implemented in later tasks --------------------

  defp handle_chat(_msg, state), do: {:ok, state}
  defp handle_mcp_result(_msg, state), do: {:ok, state}

  @impl true
  def handle_info(_msg, state), do: {:ok, state}

  @impl true
  def terminate(_reason, _state), do: :ok

  # --- helpers -----------------------------------------------------------

  defp error_frame(code, message) do
    {:text, Jason.encode!(%{"type" => "error", "v" => 1, "code" => code, "message" => message})}
  end
end
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mix test test/magus_web/cli/chat_socket_hello_test.exs`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add lib/magus_web/cli/chat_socket.ex test/magus_web/cli/chat_socket_hello_test.exs
git commit -m "feat(chat): ChatSocket hello — register, conversation, server_hello"
```

---

## Task 7: `ChatSocket` — mcp_call push + mcp_result routing

**Files:**
- Modify: `lib/magus_web/cli/chat_socket.ex`
- Test: `test/magus_web/cli/chat_socket_mcp_test.exs`

> The proxy (Task 2) does `send(handler, {:mcp_call, call_id, tool, params, from_pid})`. The handler pushes the `mcp_call` frame and records `call_id => from_pid`. On the `mcp_result` frame it sends `{:mcp_result, call_id, status, result}` back to that pid. This closes the round-trip the proxy's `receive` is waiting on.

- [ ] **Step 1: Write the failing test**

```elixir
# test/magus_web/cli/chat_socket_mcp_test.exs
defmodule MagusWeb.Cli.ChatSocketMcpTest do
  use ExUnit.Case, async: true
  alias MagusWeb.Cli.ChatSocket

  defp state, do: %{user: nil, session_id: "s", conversation_id: "c", accepted_tools: ["read_file"], pending: %{}}

  test "an mcp_call message is pushed as a frame and the waiter is tracked" do
    waiter = self()

    assert {:push, {:text, json}, new_state} =
             ChatSocket.handle_info({:mcp_call, "call-1", "read_file", %{path: "a.txt"}, waiter}, state())

    frame = Jason.decode!(json)
    assert frame["type"] == "mcp_call"
    assert frame["call_id"] == "call-1"
    assert frame["tool_name"] == "read_file"
    assert frame["params"] == %{"path" => "a.txt"}
    assert new_state.pending["call-1"] == waiter
  end

  test "an mcp_result frame is routed back to the waiting process and untracked" do
    waiter = self()
    state = %{state() | pending: %{"call-1" => waiter}}

    frame =
      Jason.encode!(%{
        "type" => "mcp_result",
        "v" => 1,
        "call_id" => "call-1",
        "status" => "ok",
        "result" => %{"content" => "hello"}
      })

    assert {:ok, new_state} = ChatSocket.handle_in({frame, [opcode: :text]}, state)
    assert_receive {:mcp_result, "call-1", "ok", %{"content" => "hello"}}
    refute Map.has_key?(new_state.pending, "call-1")
  end

  test "an mcp_result for an unknown call_id is ignored safely" do
    frame = Jason.encode!(%{"type" => "mcp_result", "call_id" => "ghost", "status" => "ok", "result" => %{}})
    assert {:ok, _state} = ChatSocket.handle_in({frame, [opcode: :text]}, state())
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mix test test/magus_web/cli/chat_socket_mcp_test.exs`
Expected: FAIL — `handle_info` returns `{:ok, state}` (no push); `pending` not updated.

- [ ] **Step 3: Implement the routing**

In `lib/magus_web/cli/chat_socket.ex`, replace the placeholder `handle_mcp_result/2` and the catch-all `handle_info/2`:

```elixir
  defp handle_mcp_result(%{"call_id" => call_id} = msg, state) do
    case Map.pop(state.pending, call_id) do
      {nil, _pending} ->
        {:ok, state}

      {waiter, pending} ->
        send(waiter, {:mcp_result, call_id, msg["status"], msg["result"] || %{}})
        {:ok, %{state | pending: pending}}
    end
  end

  defp handle_mcp_result(_msg, state), do: {:ok, state}
```

and

```elixir
  @impl true
  def handle_info({:mcp_call, call_id, tool_name, params, from_pid}, state) do
    frame =
      Jason.encode!(%{
        "type" => "mcp_call",
        "v" => 1,
        "call_id" => call_id,
        "tool_name" => tool_name,
        "params" => params
      })

    {:push, {:text, frame}, %{state | pending: Map.put(state.pending, call_id, from_pid)}}
  end

  def handle_info(_msg, state), do: {:ok, state}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mix test test/magus_web/cli/chat_socket_mcp_test.exs`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add lib/magus_web/cli/chat_socket.ex test/magus_web/cli/chat_socket_mcp_test.exs
git commit -m "feat(chat): ChatSocket mcp_call push + mcp_result routing"
```

---

## Task 8: `ChatSocket` — chat handling + PubSub → chat_stream mapping

**Files:**
- Modify: `lib/magus_web/cli/chat_socket.ex`
- Test: `test/magus_web/cli/chat_socket_stream_test.exs`
- Test: `test/magus_web/cli/chat_socket_chat_test.exs`

> `chat` drives a turn via `Chat.send_user_message/2` with `caller_session_id` + `accepted_tools` in metadata. The handler subscribes to `agents:{conversation_id}` (Task 6) and receives `%Phoenix.Socket.Broadcast{event: "agent_signal", payload: %{type: ...}}`; we map those to `chat_stream` frames, mirroring `MagusWeb.Api.Controllers.SseStreamer.handle_payload/4`. The async agent run uses the LLM (mocked in the chat test).

- [ ] **Step 1: Write the failing stream-mapping test (deterministic, no LLM)**

```elixir
# test/magus_web/cli/chat_socket_stream_test.exs
defmodule MagusWeb.Cli.ChatSocketStreamTest do
  use ExUnit.Case, async: true
  alias MagusWeb.Cli.ChatSocket

  defp state, do: %{user: nil, session_id: "s", conversation_id: "c", accepted_tools: [], pending: %{}}

  defp broadcast(payload), do: %Phoenix.Socket.Broadcast{topic: "agents:c", event: "agent_signal", payload: payload}

  defp push(payload) do
    assert {:push, {:text, json}, _} = ChatSocket.handle_info(broadcast(payload), state())
    Jason.decode!(json)
  end

  test "maps text.chunk to text.delta" do
    f = push(%{type: "text.chunk", message_id: "m1", delta: "Hel", text: "Hel"})
    assert f["type"] == "chat_stream"
    assert f["event"] == "text.delta"
    assert f["data"]["delta"] == "Hel"
  end

  test "maps text.complete to text.done" do
    f = push(%{type: "text.complete", message_id: "m1", text: "Hello", usage: %{}})
    assert f["event"] == "text.done"
    assert f["data"]["text"] == "Hello"
  end

  test "maps tool.start and tool.complete" do
    assert push(%{type: "tool.start", event_id: "e1", tool_name: "read_file", display_name: "Reading...", inputs: %{}})["event"] ==
             "tool.start"

    assert push(%{type: "tool.complete", event_id: "e1", tool_name: "read_file", status: :success, output_summary: "3 lines", duration_ms: 0, error: nil})["event"] ==
             "tool.complete"
  end

  test "maps response.complete to turn.done" do
    assert push(%{type: "response.complete", triggering_message_id: "req-1"})["event"] == "turn.done"
  end

  test "maps error using the error_message field" do
    f = push(%{type: "error", message_id: "m1", error_type: :request_failed, error_message: "boom"})
    assert f["event"] == "error"
    assert f["data"]["message"] == "boom"
  end

  test "ignores unmapped signal types" do
    assert {:ok, _} = ChatSocket.handle_info(broadcast(%{type: "thinking.chunk", delta: "..."}), state())
  end
end
```

- [ ] **Step 2: Run it to verify it fails**

Run: `mix test test/magus_web/cli/chat_socket_stream_test.exs`
Expected: FAIL — broadcasts hit the catch-all `handle_info` (`{:ok, state}`, no push).

- [ ] **Step 3: Implement chat driving + the stream mapper**

In `lib/magus_web/cli/chat_socket.ex`, replace the placeholder `handle_chat/2`:

```elixir
  defp handle_chat(%{"text" => text}, %{conversation_id: conv_id, user: user} = state)
       when is_binary(conv_id) do
    Magus.Chat.send_user_message(
      %{
        conversation_id: conv_id,
        text: text,
        metadata: %{"caller_session_id" => state.session_id, "local_tools" => state.accepted_tools}
      },
      actor: user
    )

    {:ok, state}
  end

  defp handle_chat(_msg, state), do: {:ok, state}
```

and add a `handle_info/2` clause for PubSub broadcasts (place it BEFORE the existing `{:mcp_call, ...}` clause is fine; both patterns are disjoint):

```elixir
  def handle_info(%Phoenix.Socket.Broadcast{event: "agent_signal", payload: payload}, state) do
    case map_signal(payload) do
      nil -> {:ok, state}
      {event, data} -> {:push, {:text, chat_stream_frame(event, data)}, state}
    end
  end
```

and the mapper + frame helper (near the other private helpers):

```elixir
  defp map_signal(%{type: "text.chunk"} = p), do: {"text.delta", %{"delta" => p[:delta], "message_id" => p[:message_id]}}
  defp map_signal(%{type: "text.complete"} = p), do: {"text.done", %{"text" => p[:text], "message_id" => p[:message_id]}}
  defp map_signal(%{type: "tool.start"} = p), do: {"tool.start", %{"event_id" => p[:event_id], "tool_name" => p[:tool_name], "inputs" => p[:inputs]}}
  defp map_signal(%{type: "tool.complete"} = p), do: {"tool.complete", %{"event_id" => p[:event_id], "tool_name" => p[:tool_name], "status" => to_string(p[:status]), "summary" => p[:output_summary]}}
  defp map_signal(%{type: "response.complete"} = p), do: {"turn.done", %{"message_id" => p[:triggering_message_id]}}
  defp map_signal(%{type: "error"} = p), do: {"error", %{"message" => p[:error_message]}}
  defp map_signal(_), do: nil

  defp chat_stream_frame(event, data) do
    Jason.encode!(%{"type" => "chat_stream", "v" => 1, "event" => event, "data" => data})
  end
```

- [ ] **Step 4: Run the stream test to verify it passes**

Run: `mix test test/magus_web/cli/chat_socket_stream_test.exs`
Expected: PASS (6 tests).

- [ ] **Step 5: Write the chat-driving test (LLM mocked)**

```elixir
# test/magus_web/cli/chat_socket_chat_test.exs
defmodule MagusWeb.Cli.ChatSocketChatTest do
  use Magus.DataCase, async: false  # async agent task + global Mox
  import Magus.Generators
  import Mox

  alias MagusWeb.Cli.ChatSocket

  setup :set_mox_global

  test "chat persists a user message carrying caller_session_id + local_tools metadata" do
    user = generate(user())
    {:ok, conv} = Magus.Chat.create_conversation(%{chat_mode: :chat}, actor: user)

    state = %{user: user, session_id: "s-1", conversation_id: conv.id, accepted_tools: ["read_file"], pending: %{}}

    frame = Jason.encode!(%{"type" => "chat", "v" => 1, "session_id" => "s-1", "text" => "hi there"})
    assert {:ok, _state} = ChatSocket.handle_in({frame, [opcode: :text]}, state)

    # The user message is persisted with our metadata (assert via the Chat read API).
    messages = Magus.Chat.list_messages!(conv.id, actor: user)
    user_msg = Enum.find(messages, &(&1.role == :user and &1.text == "hi there"))
    assert user_msg
    assert user_msg.metadata["caller_session_id"] == "s-1"
    assert user_msg.metadata["local_tools"] == ["read_file"]
  end
end
```

> NOTE: confirm the exact message-read interface (`Magus.Chat.list_messages!/2` or equivalent) against `lib/magus/chat/chat.ex`; adjust the read call if the code interface differs. The async agent dispatch may run without an LLM expectation — if it logs a Mox error, add a benign stub: `expect(Magus.Test.Mocks.LLMMock, :chat, fn _, _, _ -> Magus.Test.Mocks.mock_stream_response("ok") end)` (see `test/support/mocks.ex` for the exact builder name/arity).

- [ ] **Step 6: Run the chat test to verify it passes**

Run: `mix test test/magus_web/cli/chat_socket_chat_test.exs`
Expected: PASS (1 test). If the read interface name differs, fix per the NOTE and re-run.

- [ ] **Step 7: Commit**

```bash
git add lib/magus_web/cli/chat_socket.ex test/magus_web/cli/chat_socket_stream_test.exs test/magus_web/cli/chat_socket_chat_test.exs
git commit -m "feat(chat): ChatSocket chat driving + PubSub->chat_stream mapping"
```

---

## Task 9: Upgrade controller + router route (auth at the upgrade)

**Files:**
- Create: `lib/magus_web/cli/chat_socket_controller.ex`
- Modify: `lib/magus_web/router.ex`
- Test: `test/magus_web/cli/chat_socket_controller_test.exs`

> Auth reuses `ApiTokenAuthPlug` on a dedicated `:cli_socket` pipeline; the controller reads `conn.assigns.current_user`/`current_token` and calls `WebSockAdapter.upgrade/4`. A missing/invalid token is rejected by the plug (401) before the action runs. Any valid token may chat (v1 decision; `read_file` is read-only and CLI-gated).

- [ ] **Step 1: Write the failing test**

```elixir
# test/magus_web/cli/chat_socket_controller_test.exs
defmodule MagusWeb.Cli.ChatSocketControllerTest do
  use MagusWeb.ConnCase, async: true
  import Magus.Generators

  test "rejects a missing token with 401", %{conn: conn} do
    conn = get(conn, "/cli/chat")
    assert json_response(conn, 401)["error"]["code"] == "missing_token"
  end

  test "rejects an invalid token with 401", %{conn: conn} do
    conn =
      conn
      |> put_req_header("authorization", "Bearer not-a-real-token")
      |> get("/cli/chat")

    assert json_response(conn, 401)["error"]["code"] == "invalid_token"
  end

  test "a valid token passes auth and the connection is upgraded", %{conn: conn} do
    user = generate(user())
    {_token, plaintext} = api_token(actor: user, scope: :write)

    conn =
      conn
      |> put_req_header("authorization", "Bearer #{plaintext}")
      |> get("/cli/chat")

    # WebSockAdapter.upgrade/4 marks the conn upgraded rather than sending a body.
    assert conn.halted or conn.state == :upgraded
    refute conn.status == 401
  end
end
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mix test test/magus_web/cli/chat_socket_controller_test.exs`
Expected: FAIL — no route for `/cli/chat` (`Phoenix.Router.NoRouteError`).

- [ ] **Step 3: Write the controller**

```elixir
# lib/magus_web/cli/chat_socket_controller.ex
defmodule MagusWeb.Cli.ChatSocketController do
  use MagusWeb, :controller

  # ApiTokenAuthPlug (on the :cli_socket pipeline) has already authenticated and
  # assigned :current_user / :current_token, or halted with 401.
  def upgrade(conn, _params) do
    state = %{user: conn.assigns.current_user, token: conn.assigns.current_token}

    conn
    |> WebSockAdapter.upgrade(MagusWeb.Cli.ChatSocket, state, timeout: 60_000)
    |> halt()
  end
end
```

- [ ] **Step 4: Add the route + pipeline**

In `lib/magus_web/router.ex`, add a pipeline (near the `:api_v2` pipeline) and a scope:

```elixir
  pipeline :cli_socket do
    plug MagusWeb.Api.Plugs.ApiTokenAuthPlug
  end

  scope "/cli", MagusWeb.Cli do
    pipe_through [:cli_socket]
    get "/chat", ChatSocketController, :upgrade
  end
```

(Keep this separate from the existing browser-session `scope "/cli"`, which uses `[:browser, :require_auth_browser]`.)

- [ ] **Step 5: Run test to verify it passes**

Run: `mix test test/magus_web/cli/chat_socket_controller_test.exs`
Expected: PASS (3 tests). If the upgraded-conn assertion differs by Bandit/WebSockAdapter version, inspect `conn` and assert on the actual upgrade marker.

- [ ] **Step 6: Commit**

```bash
git add lib/magus_web/cli/chat_socket_controller.ex lib/magus_web/router.ex test/magus_web/cli/chat_socket_controller_test.exs
git commit -m "feat(chat): token-authed /cli/chat websocket upgrade route"
```

---

## Task 10: Hibernation regression test + full verification

**Files:**
- Test: `test/magus/agents/tools/remote/no_persist_test.exs`

> Confirms the multiplayer/hibernation guarantee: because local tools are injected per-turn (`run_tools`/`run_tool_context`, cleared on completion), a `ConversationAgent`'s persisted/base config never contains them. We assert the agent definition's base toolset has no remote tools.

- [ ] **Step 1: Write the test**

```elixir
# test/magus/agents/tools/remote/no_persist_test.exs
defmodule Magus.Agents.Tools.Remote.NoPersistTest do
  use ExUnit.Case, async: true

  test "ConversationAgent base config carries no remote/local tools" do
    # Base tools are empty in the agent definition; local tools only ever arrive
    # per-turn via run_tools, so a thawed agent (restored from base config)
    # cannot expose read_file without a live connection re-injecting it.
    strategy_opts = Magus.Agents.ConversationAgent.__jido_strategy_opts__()
    base_tools = Keyword.get(strategy_opts, :tools, [])
    refute Magus.Agents.Tools.Remote.ReadFile in base_tools
  end
end
```

> NOTE: confirm the accessor for the agent's compiled strategy opts. If `__jido_strategy_opts__/0` is not exported by `use Jido.Agent`, assert against the source instead: read `lib/magus/agents/conversation_agent.ex` and assert the `tools: []` in the strategy config — or grep-assert no `Remote.` module appears in the agent's `plugins`/strategy block. The guarantee is structural (base toolset is empty), so a source-level assertion is acceptable.

- [ ] **Step 2: Run it**

Run: `mix test test/magus/agents/tools/remote/no_persist_test.exs`
Expected: PASS (adjust the accessor per the NOTE if needed).

- [ ] **Step 3: Full server-side verification**

Run: `mix compile --warnings-as-errors && mix format --check-formatted && mix test test/magus/agents/tools/remote/ test/magus_web/cli/ test/magus/cli/ test/magus/agents/dispatcher_local_tools_test.exs`
Expected: All green.

- [ ] **Step 4: Commit**

```bash
git add test/magus/agents/tools/remote/no_persist_test.exs
git commit -m "test(chat): assert local tools never persist into agent base config"
```

---

## Manual end-to-end smoke (after Plan 2 / with a WS client)

Until the CLI (Plan 2) exists, smoke the bridge with a tiny script (e.g. `websocat` or an `iex` WebSock client):

1. Start the server: `mix phx.server`.
2. Connect to `ws://localhost:4000/cli/chat` with header `Authorization: Bearer <PAT>` (mint one via the settings UI or `magus login`).
3. Send `hello` advertising `["read_file"]` → expect `server_hello` with `accepted_tools:["read_file"]` and a `conversation_id`.
4. Send `chat` `{"text":"read mix.exs and tell me the app name"}`.
5. Expect a `mcp_call` for `read_file` → reply with `mcp_result {status:"ok", result:{content:"..."}}`.
6. Expect `chat_stream` text frames ending in `turn.done`.

This is automated end-to-end in Plan 2 (CLI) against the real CLI executor + approval flow.

---

## Self-review notes (coverage vs. spec §6)

- WS endpoint + auth-at-upgrade → Tasks 1, 9. Connection registry (caller-scoped) → Tasks 1, 6. `server_hello` + accepted-tools intersection → Task 6. Proxy `ReadFile` (self-timeout, fail-closed, non-retryable terminal results, `:DOWN` handling) → Task 2. Catalog → Task 3. Per-turn augmenting injection (multiplayer-correct, no persistence) → Tasks 4, 5, 10. `mcp_call`/`mcp_result` round-trip → Task 7. PubSub → `chat_stream` mapping → Task 8. Resume ownership → Task 6. Token-scope decision (any valid token chats) → Task 9.
- **Deferred to Plan 2 (CLI):** the local enforcement pipeline (known-tool gate, path confinement, approval/allowlist, anti-spoofing, size cap, two-sided audit), the transport client, the TUI. The server is a policy-agnostic relay by design — no local policy lives here.
- **Audit (server side):** add `ActivityLog` of emitted `mcp_call`s — small follow-up; not blocking the round-trip. Tracked for Plan 2 integration or a fast-follow.
