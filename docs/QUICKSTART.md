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

Resolution everywhere: explicit `--brain` flag (or positional arg) wins; otherwise the active brain is used; otherwise the command errors. With an active brain set, `magus page list`, `magus search ...`, and `magus page write "Notes/Today"` (single positional arg = title) all work without `--brain`.

## Write a page from markdown

From stdin:

```sh
echo "# Heading\n\nBody" | magus page write my-brain "Notes/Today"
```

From a file:

```sh
magus page write my-brain "Notes/Today" --file ~/notes.md
```

Title supports slash-paths (max 3 levels). Existing pages get blocks appended by default; pass `--mode replace` or `--mode create_only` for different semantics.

## Search

```sh
magus search "neural networks" --brain research --mode hybrid --limit 10
```

`--mode` can be `hybrid` (default, combines semantic + text + file chunks), `semantic` (embeddings only), or `text` (full-text search only).

## Read back as markdown

```sh
magus page show <page-id> --format markdown
```

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

- `brain_list`
- `brain_create`
- `page_list`
- `page_read`
- `page_write`
- `page_update`
- `page_delete`
- `brain_search`
