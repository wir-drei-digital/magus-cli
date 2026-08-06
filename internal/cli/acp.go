package cli

import (
	"fmt"
	"os"

	sdk "github.com/coder/acp-go-sdk"
	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/magus-cli/internal/acp"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

func newACPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "acp",
		Short: "Run as an ACP agent for editors (Zed, etc.)",
		Long: `Run as an Agent Client Protocol (ACP) agent over stdio.

An ACP-aware editor (Zed and others) launches 'magus acp' as a subprocess and
drives the magus cloud agent through it. When the agent reads a local file, the
editor services the read behind its own permission prompt.

stdin/stdout carry the JSON-RPC protocol; do not pipe other data through them.
Authentication uses the active profile's token (run 'magus login' first).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			token := config.ResolveToken(cfg, profile)
			apiURL := config.ResolveAPIURL(cfg, profile, DefaultAPIURL)

			agent := acp.New(token, apiURL, "magus-cli/"+Version)
			// stdout/stdin are the protocol channel; diagnostics go to stderr.
			conn := sdk.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
			agent.SetEditor(conn)

			fmt.Fprintln(os.Stderr, "magus acp: connected to editor over stdio")
			<-conn.Done()
			return nil
		},
	}
}
