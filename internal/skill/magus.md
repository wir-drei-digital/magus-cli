---
name: magus
description: Use when the user wants to read, write, search, or organize their Magus knowledge brain (also called "my brain", "my notes", "my second brain"). The magus CLI is a Bash-callable binary that authenticates against the user's workspace and exposes brain/page/block operations with JSON output for scripting.
---

# magus — Knowledge Brain CLI

The user has a persistent knowledge brain. Pages contain markdown content organized into hierarchical "brains" (max 3 levels deep). The CLI talks to the user's Magus workspace over HTTP using a Personal Access Token stored on this machine.

## When to use

Invoke `magus` when:

- The user asks you to save research, decisions, or observations for later
- The user asks you to find or recall content from past sessions
- The user references "my brain", "my notes", "my second brain", or "my knowledge base"
- You finished a non-trivial piece of work (a design, a debugging trail, a decision tree) and the user would benefit from having a written record they can search later
- The user explicitly says "magus this" or "save that to magus"

## Check setup first

Before any operation, verify magus is configured:

```sh
magus whoami
```

If this prints `no active profile (run \`magus login\`)`, STOP and instruct the user to run `magus login` themselves. The login flow opens their browser for approval and you cannot complete it for them. After they run it once, the token is stored at `~/.config/magus/config.toml` and you can use the CLI freely.

If `magus whoami` succeeds, proceed.

## Picking a brain

```sh
magus brain list --json
```

Returns JSON. Pick the brain by `slug` (preferred for stable references) or `id`. The user usually has a default brain in mind; if there are multiple and the choice isn't obvious, ask.

To create a new brain:

```sh
magus brain create "Project X"
```

## Register the active brain at session start

**This is the single most important habit.** Before you run more than one magus command in a session, register the brain you'll be working with:

```sh
magus brain use project-x
```

This pins it to the active profile so every subsequent command picks it up automatically. After that:

- `magus page list` — no `--brain` needed
- `magus search "query"` — no `--brain` needed
- `magus page write "Notes/Today"` — one positional arg (title); brain comes from active
- `magus page write "Title" --file notes.md` — same

Inspect or clear:

```sh
magus brain current            # print the active brain (non-zero exit if unset)
magus brain unset              # clear it
```

**Resolution rule** everywhere a brain is needed:

1. Explicit `--brain` flag (or first positional arg for `page write`) wins
2. The profile's active brain (from `magus brain use`)
3. Error: `no brain specified ...`

**When to call `magus brain use`:**

- The user names a brain explicitly: `magus brain use <their-slug>` immediately
- The user only has one brain: `magus brain use` to the only one in `magus brain list --json`
- The user references "my brain" without disambiguation and there are multiple: ask which one, then `magus brain use`
- The user's prior session left an active brain (`magus brain current` returns something): trust it unless they ask to switch

**Worked example.** User says "save these architecture notes":

```sh
magus brain current >/dev/null 2>&1 || magus brain use research   # set if unset
cat <<'EOF' | magus page write "Architecture/2026-05-17"
# Architecture decisions
- ...
EOF
magus search "architecture" --limit 3 --json   # confirm it's findable
```

Notice no `--brain` after the initial `brain use`. That's the whole point.

## Save content to a page (the most common operation)

The user just gave you research notes, a design decision, or observations. Save them. With an active brain registered (see section above), drop `--brain`:

```sh
cat <<'EOF' | magus page write "Projects/Magus/API Design"
# API Design Decisions

- Bearer token via PAT (32 random base62 chars, mgs_pat_ prefix)
- One token = one workspace
- Soft-delete pages, recover within 30 days
EOF
```

Without an active brain, pass it explicitly as the first positional arg:

```sh
cat notes.md | magus page write project-x "Projects/Magus/API Design"
```

Key behaviors:

- **Slash-path titles auto-create ancestors.** `"Projects/Magus/API Design"` creates the `Projects` and `Magus` pages if they don't exist (max 3 levels).
- **Default mode is `append`.** Writing to an existing page appends blocks. To overwrite, pass `--mode replace`. To error if the page exists, `--mode create_only`.
- **Content is markdown.** Server parses it into structured blocks (paragraph, heading, code, list, quote, etc.) for round-trip safety.
- **Reads from `--file <path>` or stdin.** Pipe markdown in or pass a file.

## Search before generating

When the user asks something where they may have prior notes, search the brain first (active brain implicit; pass `--brain` only to override):

```sh
magus search "rate limit strategy" --mode hybrid --limit 5 --json
```

Modes:

- `hybrid` (default): semantic + full-text + file-chunk hits, ranked
- `semantic`: embeddings only, good for concept matching
- `text`: keyword match, good for exact strings

The JSON response has `kind` (`block` or `file_chunk`), `snippet`, `score`, and reference ids you can fetch.

## Read a page back as markdown

```sh
magus page show <page-id> --format markdown
```

Use this when you need to quote or compare prior content before answering. The CLI also accepts page slugs in some places but `id` is most reliable.

## Browse hierarchy

```sh
magus page list --tree
```

`--tree` renders nested children, omit for a flat list. With no active brain set, pass `--brain <id-or-slug>`.

## Surgical edits

Inside a known block:

```sh
magus block edit <block-id> --replace "old phrase" --with "new phrase"
```

Add `--all` to replace every occurrence.

## Scripting patterns

- `--json` returns the raw API response on every read. Pipe to `jq` for filtering.
- The CLI exits non-zero on errors; standard shell error handling works.
- `MAGUS_API_TOKEN` and `MAGUS_API_URL` env vars override the stored profile when set.
- For multi-workspace setups: `magus profiles` lists configured profiles, `magus --profile <name> ...` overrides per-invocation.

## When NOT to use

- **Source code:** the brain is for notes and decisions, not code dumps. Code lives in the repo.
- **Ephemeral session state:** this conversation's TODO list belongs in the conversation, not the brain.
- **Large binary files (PDFs, images, video):** file uploads are not in this CLI version. Stick to markdown.
- **Quoting a single sentence:** if the user just wants you to remember something for the rest of the session, an `assistant` text reply is fine.

## MCP alternative

This same binary also serves an MCP stdio server:

```sh
magus mcp
```

If the user has configured the MCP server in their Claude Desktop / Cursor / Cline config and you see tools like `magus_brain_search`, `magus_page_write` available, prefer those over shelling out to `magus`. The MCP path is structured and avoids parsing JSON output.

## Full command reference

```
magus brain list|create|show|archive
magus brain use <id-or-slug>|current|unset
magus page list [--brain <id>]|show|write [brain] <title>|rename|move|delete
magus search <query> [--brain <id-or-slug>]
magus block add|edit|delete
magus link <source-block> <target> [--type relates_to]
magus profiles, magus profile use <name>
magus login [--token PAT], magus logout, magus whoami
magus mcp
magus version
magus update [--check] [--force]
```

If the CLI reports it can't reach a feature or behaves oddly, suggest `magus update` to pull the latest release before debugging further.

Global flags: `--profile <name>`, `--json`, `--quiet`. Run `magus <cmd> --help` for any subcommand's flags.
