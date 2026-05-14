package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// Set via -ldflags at build time.
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the magus version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("magus %s (%s, %s)\n", Version, Commit, BuildDate)
		},
	}
}
