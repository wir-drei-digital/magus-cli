package cli

import (
	"context"

	"github.com/spf13/cobra"
)

const DefaultAPIURL = "https://magus.digital"

var (
	apiURL    string
	profile   string
	jsonMode  bool
	quietMode bool
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "magus",
		Short: "Magus CLI — manage your knowledge brain from the terminal",
		Long: `magus is the command-line interface for the Magus brain API.

It authenticates with a Personal Access Token (PAT) scoped to a single
workspace, and exposes commands for managing brains, pages, and search.
Pages are stored as markdown. Includes a bundled stdio MCP server
(` + "`magus mcp`" + `) for use with Claude Desktop, Cursor, and other MCP-aware
clients.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.PersistentFlags().StringVar(&apiURL, "api-url", "", "override API base URL (default from profile)")
	cmd.PersistentFlags().StringVar(&profile, "profile", "", "override active profile")
	cmd.PersistentFlags().BoolVar(&jsonMode, "json", false, "machine-readable JSON output")
	cmd.PersistentFlags().BoolVar(&quietMode, "quiet", false, "suppress non-error output")

	cmd.AddGroup(
		&cobra.Group{ID: "data", Title: "Knowledge operations:"},
		&cobra.Group{ID: "auth", Title: "Authentication:"},
		&cobra.Group{ID: "agent", Title: "Agent integration:"},
		&cobra.Group{ID: "system", Title: "System:"},
	)

	addInGroup := func(group string, sub *cobra.Command) {
		sub.GroupID = group
		cmd.AddCommand(sub)
	}

	addInGroup("auth", newLoginCmd())
	addInGroup("auth", newLogoutCmd())
	addInGroup("auth", newWhoamiCmd())
	addInGroup("auth", newProfilesCmd())
	addInGroup("auth", newProfileCmd())

	addInGroup("data", newBrainCmd())
	addInGroup("data", newPageCmd())
	addInGroup("data", newSearchCmd())

	addInGroup("agent", newChatCmd())
	addInGroup("agent", newMCPCmd())
	addInGroup("agent", newACPCmd())
	addInGroup("agent", newSkillCmd())

	addInGroup("system", newVersionCmd())
	addInGroup("system", newUpdateCmd())

	return cmd
}

func Execute(ctx context.Context) error {
	return newRootCmd().ExecuteContext(ctx)
}
