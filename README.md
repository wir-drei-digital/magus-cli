# magus

The command-line interface for the [Magus](https://magus.digital) knowledge-brain API.

## Install

```sh
curl -fsSL https://magus.digital/install.sh | sh
```

Installs to `~/.magus/bin/magus` (no sudo). The installer adds the directory to your `PATH` via your shell's rc file. Override with `MAGUS_INSTALL_DIR=/usr/local/bin sh install.sh` if you want a system-wide install.

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

- `magus login [--token PAT]`: authorize
- `magus profiles`, `magus profile use <name>`: switch workspaces
- `magus brain list|create|show|archive`
- `magus brain use|current|unset`: pin a default brain so other commands can drop `--brain`
- `magus page list|show|create|append|prepend|replace|edit|clear|undo|rename|move|delete`
- `magus search <query> [--kind unified|semantic|text] [--cross-brain]`
- `magus mcp`: stdio MCP server
- `magus skill install|show|uninstall`: manage the embedded agent skill
- `magus update [--check] [--force]`: self-update to the latest GitHub release

Global flags: `--profile <name>`, `--json`, `--quiet`.

## License

MIT.
