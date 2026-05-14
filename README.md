# magus

The command-line interface for the [Magus](https://magus.digital) knowledge-brain API.

## Install

```sh
curl -fsSL https://magus.digital/install.sh | sh
```

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
echo "# Notes\n\nFirst paragraph." | magus page write my-research "Today/Notes"
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

## Use with Claude Code (and other agents)

A skill file at [`internal/skill/magus.md`](internal/skill/magus.md) teaches
agents like Claude Code, Codex, and other markdown-frontmatter skill loaders
when and how to invoke this CLI. Install it once:

```sh
magus skill install
```

By default this writes to `~/.claude/skills/magus.md`. For other agents:

```sh
magus skill install --target codex       # ~/.codex/skills/magus.md
magus skill install --path /custom/place # arbitrary path
magus skill install --update             # overwrite existing
magus skill show                         # print the skill to stdout
magus skill uninstall                    # remove the installed file
```

The skill is embedded into the binary, so it updates atomically when you
upgrade the CLI.

## Commands

- `magus login [--token PAT]`: authorize
- `magus profiles`, `magus profile use <name>`: switch workspaces
- `magus brain list|create|show|archive`
- `magus page list|show|write|rename|move|delete`
- `magus search <query> --brain <id>`
- `magus block add|edit|delete`
- `magus link <source-block> <target> [--type relates_to]`
- `magus mcp`: stdio MCP server

Global flags: `--profile <name>`, `--json`, `--quiet`, `--no-color`.

## License

MIT.
