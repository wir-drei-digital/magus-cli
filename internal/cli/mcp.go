package cli

import (
	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/mcp"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP stdio server backed by the active profile",
		Long: `Run a Model Context Protocol (MCP) server over stdio, exposing
the active profile's brain as a set of tools (brain_list, brain_create,
page_list, page_read, page_write, page_update, page_delete, brain_search).

Designed to be launched by MCP-aware clients like Claude Desktop or Cursor.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			return mcp.Serve(cmd.Context(), c, Version)
		},
	}
}
