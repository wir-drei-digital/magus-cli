# Quickstart

## Authorize

Run `magus login`. A browser opens at `<api-url>/cli/authorize`. Click Approve and the CLI captures the token automatically.

For CI or headless use, pass a PAT directly:

```sh
magus login --token mgs_pat_...
```

You can save multiple profiles (one per workspace):

```sh
magus login --name personal
magus login --name acme
magus profile use acme
```

## Pin an active brain (optional)

If you mostly work in one brain, set it as the active brain so commands stop needing `--brain`:

```sh
magus brain use my-brain     # store as active for the current profile
magus brain current          # print the active brain (non-zero exit if unset)
magus brain unset            # clear it
```

Resolution everywhere: explicit `--brain` flag wins; otherwise the active brain is used; otherwise the command errors. With an active brain set, `magus page list`, `magus search ...`, and `magus page create "Today Notes"` all work without `--brain`.

## Write a page

Create a page (body from stdin or `--file`):

```sh
printf '# Heading\n\nBody\n' | magus page create "Today Notes"
magus page create "Today Notes" --file ~/notes.md
```

Add to an existing page instead of creating:

```sh
echo "- another note" | magus page append "Today Notes"
```

Overwrite, surgically edit, or revert:

```sh
echo "fresh body" | magus page replace "Today Notes"
magus page edit "Today Notes" --find "typo" --with "fixed"
magus page undo "Today Notes"
```

The --find text must match exactly once; pass --all to replace every occurrence.

Nest with `--parent <ref>` (a page id, slug, or `brain/slug`).

## Search

```sh
magus search "neural networks" --brain research --kind unified --limit 10
```

`--kind` is `unified` (default; semantic + text + file chunks), `semantic` (embeddings only), or `text` (full-text). Add `--cross-brain` to span every accessible brain.

## Read back as markdown

```sh
magus page show <ref>
```

`<ref>` is a page id, a page slug (active brain), or `brain/page-slug`.

## MCP integration

Bundle the CLI's MCP server into any MCP-aware client. Example `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "magus-brain": {
      "command": "magus",
      "args": ["mcp"]
    }
  }
}
```

The MCP tools:

- `brain_list`, `brain_create`
- `page_list`, `page_read`
- `page_create`, `page_append`, `page_prepend`, `page_replace`, `page_edit`
- `page_clear`, `page_undo`, `page_rename`, `page_move`, `page_delete`
- `brain_search`
