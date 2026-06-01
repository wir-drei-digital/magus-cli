---
name: magus
description: Use when the user wants to read, write, search, or organize their Magus knowledge brain (also called "my brain", "my notes", "my second brain"). The magus CLI is a Bash-callable binary that authenticates against the user's workspace and exposes brain and page operations with JSON output for scripting.
allowed-tools: Bash, Read, Grep
user-invocable: true
---

# magus: Knowledge Brain CLI

The user has a persistent knowledge brain. A brain holds markdown **pages** in a hierarchy (max 3 levels deep). Each page is a single CommonMark document. The CLI talks to the user's Magus workspace over HTTP using a Personal Access Token stored on this machine.

## When to use

Invoke `magus` when:

- The user asks you to save research, decisions, or observations for later
- The user asks you to find or recall content from past sessions
- The user references "my brain", "my notes", "my second brain", or "my knowledge base"
- You finished a non-trivial piece of work (a design, a debugging trail, a decision tree) and the user would benefit from a written record they can search later
- The user explicitly says "magus this" or "save that to magus"

## Check setup first

Before any operation, verify magus is configured:

```sh
magus whoami
```

If this prints `no active profile (run \`magus login\`)`, STOP and instruct the user to run `magus login` themselves. The login flow opens their browser for approval and you cannot complete it for them. After they run it once, the token is stored and you can use the CLI freely.

If `magus whoami` succeeds, proceed.

## Pick and pin a brain

```sh
magus brain list --json
```

Pick the brain by `slug` (preferred) or `id`. Then pin it for the session so later commands can drop `--brain`:

```sh
magus brain use project-x
magus brain current            # print the active brain (non-zero exit if unset)
magus brain unset              # clear it
```

Create a new brain with `magus brain create "Project X"`.

**Resolution rule** everywhere a brain is needed: explicit `--brain` wins, then the active brain, else an error.

## Pages are markdown

A page is one markdown body. Change it with explicit verbs:

```sh
magus page create "Title"      # new page, body from stdin or --file
magus page append <ref>        # add to the end
magus page prepend <ref>       # add to the start
magus page replace <ref>       # overwrite the whole body (destructive)
magus page edit <ref> --find "old" --with "new" [--all]   # surgical find/replace; --find must match once (use --all for every occurrence)
magus page clear <ref>         # empty the body (page kept)
magus page undo <ref>          # revert the last body change
magus page show <ref>          # print the markdown body
magus page list [--tree]       # browse the hierarchy
magus page rename <ref> "New Title"
magus page move <ref> --parent <ref|none>
magus page delete <ref>        # soft-delete (recoverable from trash)
```

`<ref>` is a page id, a page slug (active brain), or `brain/page-slug`.

## Save content (the most common operation)

```sh
cat <<'EOF' | magus page create "API Design"
# API Design Decisions

- Bearer token via PAT, one token per workspace
- Soft-delete pages, recover from trash
EOF
```

Add to an existing page instead of creating a new one:

```sh
echo "- Decided to cache embeddings" | magus page append "API Design"
```

Nest a page with `--parent` (titles are plain strings, not paths):

```sh
echo "..." | magus page create "Auth" --parent "API Design"
```

## Page body syntax

The body is CommonMark plus a few Magus extensions you can write directly:

~~~text
---
icon: 🧠
tags: [ml, research]
aliases: [Old Name]
---

[[Another Page]]                  link to another page
[[Another Page|label]]            link with custom display text

```source
url: https://example.com
title: Example Article
source_type: web                  web | paper | book | video
```

```callout
variant: info                     info | note | insight | warning | question
text: A highlighted note
```

[📎 caption](magus://file/<id>)    attach an uploaded file
![caption](magus://image/<id>)     embed an uploaded image
#tag #multi-word                   inline tags
~~~

**Linking is wikilinks.** There is no `link` command: to connect pages, write `[[Target Page]]` into a body with `page append` or `page edit`.

## Search before generating

```sh
magus search "rate limit strategy" --kind unified --limit 5 --json
```

Kinds: `unified` (default; semantic + full-text + file chunks), `semantic` (embeddings only), `text` (keyword). Add `--cross-brain` to span every brain you can access. JSON hits carry `kind`, `score` (or `rank` for whole-page hits), `snippet`, and reference ids.

## Read a page back

```sh
magus page show <ref>
```

Prints the markdown body. Add `--json` for metadata (id, slug, frontmatter, lock_version).

## Scripting patterns

- `--json` returns the raw API response on every read. Pipe to `jq`.
- The CLI exits non-zero on errors; standard shell error handling works.
- `MAGUS_API_TOKEN` and `MAGUS_API_URL` override the stored profile when set.
- Multi-workspace: `magus profiles` lists profiles, `magus --profile <name> ...` overrides per-invocation.

## When NOT to use

- **Source code:** the brain is for notes and decisions, not code dumps.
- **Ephemeral session state:** this conversation's TODO list belongs in the conversation.
- **Quoting a single sentence:** if the user just wants you to remember something for the rest of the session, a normal reply is fine.

## MCP alternative

This same binary serves an MCP stdio server (`magus mcp`). If the user has it configured in their MCP client and you see tools like `page_create`, `page_append`, `page_edit`, `brain_search`, prefer those over shelling out. The MCP path is structured and avoids parsing JSON.

## Full command reference

```
magus brain list|create|show|archive
magus brain use <ref>|current|unset
magus page list|show|create|append|prepend|replace|edit|clear|undo|rename|move|delete
magus search <query> [--kind unified|semantic|text] [--cross-brain] [--limit N]
magus profiles, magus profile use <name>
magus login [--token PAT], magus logout, magus whoami
magus mcp
magus version
magus update [--check] [--force]
```

If the CLI behaves oddly or reports a missing feature, suggest `magus update` before debugging further.

Global flags: `--profile <name>`, `--json`, `--quiet`. Run `magus <cmd> --help` for any subcommand's flags.
