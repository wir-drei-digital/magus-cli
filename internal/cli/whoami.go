package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print the active profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			p, ok := cfg.Active(profile)
			if !ok {
				return fmt.Errorf("no active profile (run `magus login`)")
			}
			fmt.Printf("API URL:   %s\n", p.APIURL)
			fmt.Printf("Workspace: %s\n", nonEmpty(p.Workspace, "Personal"))
			fmt.Printf("Scope:     %s\n", nonEmpty(p.Scope, "(unknown)"))
			fmt.Printf("Email:     %s\n", nonEmpty(p.UserEmail, "(unknown)"))
			return nil
		},
	}
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
