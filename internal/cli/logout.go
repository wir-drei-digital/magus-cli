package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the active profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			name := profile
			if name == "" {
				name = cfg.DefaultProfile
			}
			if name == "" {
				return fmt.Errorf("no active profile")
			}

			delete(cfg.Profiles, name)
			if cfg.DefaultProfile == name {
				cfg.DefaultProfile = ""
				for k := range cfg.Profiles {
					cfg.DefaultProfile = k
					break
				}
			}

			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Printf("Removed profile %q.\n", name)
			return nil
		},
	}
}
