package cli

import (
	"github.com/spf13/cobra"
)

const DefaultAPIURL = "https://magus.digital"

var (
	apiURL    string
	profile   string
	jsonMode  bool
	quietMode bool
	noColor   bool
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "magus",
		Short: "Magus CLI — manage your knowledge brain from the terminal",
		Long: `magus is the command-line interface for the Magus brain API.

It authenticates with a Personal Access Token (PAT) scoped to a single
workspace, and exposes commands for managing brains, pages, blocks,
search, and connections. Includes a bundled stdio MCP server (` + "`magus mcp`" + `)
for use with Claude Desktop, Cursor, and other MCP-aware clients.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.PersistentFlags().StringVar(&apiURL, "api-url", "", "override API base URL (default from profile)")
	cmd.PersistentFlags().StringVar(&profile, "profile", "", "override active profile")
	cmd.PersistentFlags().BoolVar(&jsonMode, "json", false, "machine-readable JSON output")
	cmd.PersistentFlags().BoolVar(&quietMode, "quiet", false, "suppress non-error output")
	cmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color output")

	cmd.AddCommand(
		newVersionCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newWhoamiCmd(),
		newProfilesCmd(),
		newProfileCmd(),
		newBrainCmd(),
		newPageCmd(),
		newSearchCmd(),
	)
	return cmd
}

func Execute() error {
	return newRootCmd().Execute()
}
