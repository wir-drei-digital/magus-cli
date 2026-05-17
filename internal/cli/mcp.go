package cli

import (
	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/mcp"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP stdio server backed by the active profile",
		Long: `Run a Model Context Protocol (MCP) server over stdio, exposing
the active profile's brain as a set of tools (brain_list, brain_create,
page_list, page_read, page_write, page_update, page_delete, brain_search).

The active brain (set via 'magus brain use <id>') is captured at startup
and used as a fallback for brain_search when its brain arg is omitted.
Changing the active brain while the server is running requires a restart.

Designed to be launched by MCP-aware clients like Claude Desktop or Cursor.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			activeBrain := config.ResolveActiveBrain(cfg, "")
			return mcp.Serve(cmd.Context(), c, Version, activeBrain)
		},
	}
}
