package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/update"
)

var (
	updateCheckOnly bool
	updateForce     bool
)

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update magus to the latest GitHub release",
		Long: `Fetches the latest magus release from GitHub, verifies its SHA-256
checksum, and atomically replaces the running binary in place.

Examples:
  magus update              # install the latest release if newer
  magus update --check      # only report availability; don't install
  magus update --force      # reinstall even if already on latest`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "windows" {
				return fmt.Errorf("self-update is not supported on Windows yet. Re-run install.sh or download a new binary from GitHub Releases.")
			}

			ua := "magus-update/" + Version
			c := update.NewClient(ua)

			res, err := c.Run(Version, update.Options{
				CheckOnly: updateCheckOnly,
				Force:     updateForce,
			})
			if err != nil {
				return err
			}

			switch {
			case res.UpToDate && !updateForce:
				fmt.Printf("magus is already up to date (v%s)\n", res.LatestVersion)
			case updateCheckOnly:
				fmt.Printf("Update available: %s -> %s (run 'magus update' to install)\n",
					displayVersion(res.CurrentVersion), res.LatestVersion)
			case res.Updated:
				fmt.Printf("Updated magus %s -> %s\n",
					displayVersion(res.CurrentVersion), res.LatestVersion)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&updateCheckOnly, "check", false, "only check; do not install")
	cmd.Flags().BoolVar(&updateForce, "force", false, "reinstall even if already on latest")
	return cmd
}

// displayVersion renders the build-from-source sentinel as something
// friendlier in the update message.
func displayVersion(v string) string {
	if v == "dev" {
		return "dev"
	}
	return v
}
