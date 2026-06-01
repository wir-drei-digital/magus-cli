# magus

The command-line interface for the [Magus](https://magus.digital) knowledge-brain API.

## Install

```sh
curl -fsSL https://magus.digital/install.sh | sh
```

Installs to `~/.magus/bin/magus`. The installer adds the directory to your `PATH` via your shell's rc file. Override with `MAGUS_INSTALL_DIR=/usr/local/bin sh install.sh` if you want a system-wide install.

Or build from source:

```sh
git clone https://github.com/wir-drei-digital/magus-cli
cd magus-cli
make install
```

## Quickstart

Authorize this machine:

```sh
magus login
```

Browser opens; click Approve in the workspace you want this token scoped to.

Create a brain and a page:

```sh
magus brain create "My Research"
magus brain use my-research
printf '# Notes\n\nFirst paragraph.\n' | magus page create "Today Notes"
```

Search:

```sh
magus search "First paragraph" --brain my-research
```

See the full quickstart at [docs/QUICKSTART.md](docs/QUICKSTART.md).

## Use with Claude Desktop / Cursor / Cline (MCP)

Add to your `claude_desktop_config.json` (or equivalent):

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

The MCP server reads the active profile's token from `~/.config/magus/config.toml`.

## Use with Claude Code

Magus ships as a Claude Code marketplace plugin. Install it once from inside Claude Code:

```
/plugin marketplace add wir-drei-digital/magus-cli
/plugin install magus@wir-drei-digital
```

Claude Code will discover the `magus` skill and load it on relevant prompts (when you mention your brain, notes, knowledge base, etc.). The skill source ships from [`plugins/magus/skills/magus/SKILL.md`](plugins/magus/skills/magus/SKILL.md).

## Use with Codex, Cursor, and other agents

For agents that read markdown frontmatter skill files from a known directory, install the embedded skill via the CLI:

```sh
magus skill install --target codex       # ~/.codex/skills/magus.md
magus skill install --path /custom/place # arbitrary path
magus skill show                         # print the skill to stdout
magus skill uninstall                    # remove the installed file
```

The skill is embedded into the binary, so it updates atomically when you upgrade the CLI.

## Commands

`<ref>` is a page id, a page slug (in the active brain), or `brain/page-slug`.

### Auth and profiles

- `magus login [--token PAT]`: authorize this machine (browser flow, or pass a PAT directly)
- `magus logout`: remove the active profile
- `magus whoami`: print the active profile
- `magus profiles`, `magus profile use <name>`: list and switch profiles (one per workspace)

### Brains

- `magus brain list|create|show|archive`
- `magus brain use <ref>`, `magus brain current`, `magus brain unset`: pin a default brain so other commands can drop `--brain`

### Pages

Each page is a single markdown document.

| Command | Description |
|---|---|
| `page list [--brain <ref>] [--tree]` | List pages, flat or as a `--tree` |
| `page show <ref>` | Print the markdown body (`--json` for metadata) |
| `page create <title> [--parent <ref>] [--file f.md]` | Create a page; body from stdin or `--file` |
| `page append <ref>`, `page prepend <ref>` | Add markdown to the end or start (stdin or `--file`) |
| `page replace <ref>` | Overwrite the entire body |
| `page edit <ref> --find "old" --with "new" [--all]` | Find and replace; must match once unless `--all` |
| `page clear <ref>` | Empty the body, keep the page |
| `page undo <ref>` | Revert the last body change |
| `page rename <ref> <title>` | Rename a page |
| `page move <ref> --parent <ref\|none>` | Reparent (`none` moves to root) |
| `page delete <ref>` | Soft-delete (recoverable from trash) |

Link pages by writing `[[Page Title]]` into a body (for example with `magus page append`). There is no separate link command.

### Search

- `magus search <query> [--brain <ref>] [--kind unified|semantic|text] [--cross-brain] [--limit N]`

`--kind` is `unified` (default: semantic + full-text + file chunks), `semantic`, or `text`.

### Agent integration

- `magus mcp`: bundled stdio MCP server (mirrors the page, brain, and search tools)
- `magus skill install|show|uninstall`: manage the embedded agent skill

### System

- `magus update [--check] [--force]`: self-update to the latest GitHub release
- `magus version`: print the version

Global flags: `--profile <name>`, `--json`, `--quiet`, `--api-url <url>`.

## License

MIT.
