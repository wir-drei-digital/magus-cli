# Brain Page API Rework (magus-cli)

Date: 2026-06-01
Status: Approved design, pre-implementation
Scope: First pass of the larger magus-cli rework, focused on the brain page + search HTTP API.

## Background

The Magus server migrated brain pages from a "blocks" data model to
**markdown-as-storage**: a page is now a single CommonMark `body` string with
custom syntax (YAML frontmatter, `[[wikilinks]]`, fenced `source`/`callout`
blocks, `magus://` file links, `#tags`). The block and connection resources are
gone from the public API.

The CLI was built against the older transitional API and is now broken in
several places:

- `api/pages.go` reads `markdown` and sends `content`; the server now returns
  and accepts `body`. It also carries dead `blocks` / `blocks_added` fields and
  is missing `lock_version`, `frontmatter`, `icon`.
- `api/blocks.go` + `cli/block.go` call `/api/v2/pages/:id/blocks` and
  `/api/v2/blocks/:id`, which no longer exist.
- `api/connections.go` + `cli/link.go` call `/api/v2/connections`, which no
  longer exists. Links are now implicit `[[wikilinks]]` written into a body.
- `page write` / `page clear` fake updates by POSTing to the *create* endpoint
  with a `page_id` + `mode`. The server now separates create from body-edit.
- `api/search.go` sends `mode` but the server wants `kind`; hit fields are
  misaligned (`page_title` vs `title`, missing `rank` / `source_id` / `file_id`).
- `internal/mcp/tools.go` mirrors all of the above.

The primary consumer of this CLI is an AI agent (driving via the `magus mcp`
server or via shell). The design optimizes for **agent clarity**: explicit
single-purpose verbs, no hidden create-vs-update branching, named destructive
operations, and server-native append/prepend to avoid read-modify-write races.

## Scope

**In:**

- Re-align the `api/` client layer for pages and search against the current
  server contract.
- Replace the `page` command surface with explicit verbs (below).
- Add a client-side surgical `page edit --find/--with`.
- Delete the blocks and connections concepts (CLI commands + API client files).
- Update the bundled MCP server (`magus mcp`) tools to match.
- Rewrite `SKILL.md` (both copies) and `README.md` / `docs/QUICKSTART.md` to
  document markdown-as-storage and the body syntax.
- Update the affected tests.

**Out (future, each its own spec):**

- `magus chat` TUI + WebSocket reverse-tunnel MCP (a larger feature on its own).
- `magus files` upload/management commands.
- Cross-graph "superbrain" search beyond what the server's `cross_brain` flag
  already provides.
- Any auth/login changes (`magus login` already works).

## Server API contract (reference)

All routes are under `/api/v2`, Bearer-token auth, `{"data": ...}` success
envelope, `{"error": {"code", "message", "details"}}` error envelope. Already
handled by `api/client.go`; no transport changes needed.

| Operation | Method + path | Request body | Notes |
|---|---|---|---|
| List pages | `GET /brains/:brain/pages` | (none) | tree by default; `?as=flat` for a flat list |
| Show page (id) | `GET /pages/:id` | (none) | returns full page incl. `body` |
| Show page (slug) | `GET /brains/:brain/pages/:slug` | (none) | full page |
| Create page | `POST /brains/:brain/pages` | `{title, body?, parent_page_id?}` | 409 `already_exists` on case-insensitive title collision |
| Edit body | `PATCH /pages/:id` | `{body, mode}` | `mode`: `replace` \| `append` \| `prepend`; append/prepend joined with `\n\n`; `base_version` handled server-side |
| Rename | `PATCH /pages/:id` | `{title}` | |
| Move | `PATCH /pages/:id` | `{parent_page_id}` | `""`/null moves to root |
| Clear body | `POST /pages/:id/clear` | (none) | sets body to `""` |
| Undo | `POST /pages/:id/undo` | (none) | restores previous `update_body` version |
| Delete | `DELETE /pages/:id` | (none) | soft-delete (trash); returns `{id, deleted_at}` |
| Search | `POST /brains/:brain/search` | `{query, kind?, limit?, cross_brain?}` | `kind`: `unified` (default) \| `semantic` \| `text` |

Full page response shape:

```json
{ "id", "title", "slug", "body", "lock_version", "frontmatter",
  "brain_id", "parent_page_id", "depth", "icon", "inserted_at", "updated_at" }
```

Summary (list) shape: `{ id, slug, title, brain_id, parent_page_id, depth, updated_at }`,
plus a `children` array in tree mode.

Search hit shapes (discriminated by `kind`):

```
page         { kind, rank,  brain_id, page_id, title, snippet }
page_chunk   { kind, score, brain_id, page_id, snippet }
source_chunk { kind, score, brain_id, source_id, snippet }
file_chunk   { kind, score, brain_id, page_id, file_id, snippet }
```

Conflict detail payloads (in `error.details`):

- `already_exists`: `{ existing_page_id, existing_page_title, body_preview, last_modified_at }`
- `version_conflict`: `{ existing_page_id, current_version, base_version, ... }`

## Command surface

`<ref>` is uniformly `id` | `page-slug` (active brain) | `brain/page-slug`,
resolved by the existing `resolvePage` (`cli/page_resolver.go`, unchanged).

### Pages (`magus page`)

| Command | Server call |
|---|---|
| `page list [--brain] [--tree]` | `GET /brains/:b/pages[?as=flat]` |
| `page show <ref> [--json]` | `GET /pages/:id`; prints `body`, `--json` prints full object |
| `page create <title> [--brain] [--parent <ref>]` | `POST /brains/:b/pages` (body from stdin/`--file`) |
| `page append <ref>` | `PATCH /pages/:id` `{body, mode: append}` (body from stdin/`--file`) |
| `page prepend <ref>` | `PATCH /pages/:id` `{body, mode: prepend}` |
| `page replace <ref>` | `PATCH /pages/:id` `{body, mode: replace}` (the one explicitly destructive verb) |
| `page edit <ref> --find X --with Y [--all]` | client read-modify-write, then `PATCH replace` |
| `page clear <ref>` | `POST /pages/:id/clear` |
| `page undo <ref>` | `POST /pages/:id/undo` |
| `page rename <ref> <title>` | `PATCH /pages/:id` `{title}` |
| `page move <ref> --parent <ref\|none>` | `PATCH /pages/:id` `{parent_page_id}` |
| `page delete <ref>` | `DELETE /pages/:id` |

Body input convention (create/append/prepend/replace): read from `--file` if
set, else from stdin if piped, else empty (a title-only page on create; a no-op
guarded with an error on the edit verbs).

### Search (`magus search <query>`)

Flags: `--kind unified|semantic|text` (default unified, omitted from request when
empty), `--limit`, `--cross-brain`, `--brain` (falls back to active brain).
Human output is kind-aware (rank/score + identifier + snippet); `--json` prints
the raw hit array.

### Brain (`magus brain`)

Unchanged. `list` / `create` / `get` / `update` / `archive` already match the
server.

### Removed

- `magus block` (and `api/blocks.go`)
- `magus link` (and `api/connections.go`)

## Surgical edit semantics (`page edit`)

Agent-safe, modeled on a unique-match editor:

1. `GET` the page, take its `body`.
2. Count occurrences of `--find`.
   - 0 matches: error `"text not found in page body"` (non-zero exit).
   - more than 1 match and no `--all`: error asking for a more specific string
     or `--all` (prevents silent partial edits).
3. Replace (first match, or all with `--all`) and `PATCH /pages/:id`
   `{body, mode: replace}`.

`page undo` is the safety net. Two round-trips (GET + PATCH) is acceptable.

## API client changes (`internal/api/`)

- **`pages.go`**
  - `Page`: add `Body`, `LockVersion`, `Frontmatter map[string]any`, `Icon`;
    keep `Children` (tree), `DeletedAt` (delete result); remove `Markdown`,
    `Blocks`, `BlocksAdded`.
  - Replace `WritePage`/`WritePageInput` with:
    - `CreatePage(brainID, {Title, Body, ParentPageID})`
    - `UpdatePageBody(pageID, body, mode)`
    - `ClearPage(pageID)`, `UndoPage(pageID)`
  - Keep `UpdatePage` for title/parent. Drop the `?format=` arg from `GetPage`.
- **`search.go`**: rename request `mode` to `kind`; add `cross_brain bool`; drop
  `page`. `SearchHit`: add `Rank`, `SourceID`, `FileID`, `Title` (json `title`);
  drop `ID`, `PageNumber`.
- **Delete** `blocks.go`, `connections.go`.
- `client.go`, `brains.go`, `errors.go`: unchanged. `errors.go` already carries
  `Details`; the `page create` command handler reads `existing_page_id` from it
  so an agent can pivot (e.g. to `page append <existing_id>`).

## MCP server (`internal/mcp/tools.go`, `server.go`)

Mirror the page verbs so the CLI and MCP vocabularies match. Registered tools:

```
brain_list, brain_create,
page_list, page_read,
page_create, page_append, page_prepend, page_replace, page_edit,
page_clear, page_undo, page_rename, page_move, page_delete,
brain_search
```

- `page_read` returns `body` (drop the `format=markdown` path).
- `page_create` params `{brain, title, body?, parent_page_id?}`.
- `page_append`/`prepend`/`replace` params `{page, body}`.
- `page_edit` params `{page, find, with, all?}`.
- `brain_search` param `mode` to `kind` (`unified|semantic|text`); description
  no longer says "blocks".
- Each tool description documents the relevant body syntax so the agent knows
  what it may write.

The MCP brain surface stays `brain_list` + `brain_create` (brain admin is rare
for an agent); not expanded in this pass.

## SKILL.md, README, QUICKSTART

`SKILL.md` exists in two byte-identical copies: the canonical plugin path
`plugins/magus/skills/magus/SKILL.md` and the embedded `internal/skill/SKILL.md`.
Edit the canonical copy, then `make sync-skill`; `TestSkillContentMatchesPluginCopy`
enforces parity.

Rewrite all three docs to drop blocks/connections and document the new model.
The most important addition is the **body syntax reference** the agent should
emit:

```text
---
icon: 🧠
tags: [ml, research]
aliases: [Old Name]
---

[[Page Name]]                         cross-reference / link to another page
[[Page Name|display text]]            link with custom display text

```source                             registers a source on the page
url: https://example.com
title: Example Article
source_type: web                      web | paper | book | video
```

```callout
variant: info                         info | note | insight | warning | question
text: A short highlighted note
```

[📎 caption](magus://file/<uuid>)     attach an uploaded file
![caption](magus://image/<uuid>)      embed an uploaded image
#tag #multi-word-tag                  inline tags

Standard CommonMark/GFM otherwise: headings, lists, tables, task lists, code.
```

Linking guidance to call out explicitly: there is no `link` command; to link
pages, write `[[Target Page]]` into a body via `page append`/`edit`.

## `root.go`

Remove `newBlockCmd()` and `newLinkCmd()` registration from the `data` group.
Update the root `Long` description (drop "blocks ... and connections").

## Error / conflict handling

- `page create` on 409 `already_exists`: surface code + message, and include
  `existing_page_id` / `existing_page_title` from `details` so the agent can
  switch to `page append <existing_id>`.
- Body edits: the server reads `lock_version` fresh per request, so single-step
  PATCH calls do not conflict; just surface any server error verbatim.
- Errors print to stderr with a non-zero exit; `--json` callers rely on exit
  code + the structured `error` envelope.

## Testing

- **`api/`**: no block/connection tests exist, so removal is clean. Keep the
  generic `client_test.go`. Add table-driven tests (httptest mock servers)
  asserting the request method/path/body for `CreatePage`, `UpdatePageBody`
  (each mode), `ClearPage`, `UndoPage`, and search with `kind`/`cross_brain`.
- **`mcp/tools_test.go`**: update `TestPageReadCore` (no `format=markdown`, reads
  `body`), replace `TestPageWriteCore` with create/append/edit cases, update
  `TestBrainSearchCore*` to assert `kind`, and update `TestToolsRegistration` to
  the new tool list. Add a `page_edit` test covering the GET-then-PATCH
  read-modify-write and the unique-match guard.
- **`cli/page_resolver_test.go`**: unchanged (resolution is unchanged).
- Add a unit test for the surgical-edit match logic (0 / 1 / many occurrences,
  `--all`).

## Delivery

- Work on a feature branch in the `magus-cli` repo; conventional commits.
- Gate: `gofmt`, `go vet ./...`, `go build ./...`, `go test ./...`, and
  `make sync-skill` parity before finishing.

## Resolved decisions

- Explicit verbs over a smart `write` (clearest for an agent; no create-vs-update
  guessing; named destructive `replace`).
- Include the surgical `page edit --find/--with` (matches how an agent edits).
- Hard-remove blocks and links (no deprecation stubs); linking is `[[wikilinks]]`.
- Drop slash-path title ancestor auto-creation; nesting is explicit `--parent`.
- Document custom block syntax rather than adding emit helper commands (YAGNI).
- Rename search `--mode` to `--kind` with no back-compat alias (values changed,
  pre-1.0).
- CLI fully explicit; MCP mirrors the same verbs for a consistent vocabulary.
```
