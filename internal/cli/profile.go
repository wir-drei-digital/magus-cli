package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

func newProfilesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profiles",
		Short: "List configured profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if len(cfg.Profiles) == 0 {
				fmt.Println("No profiles configured. Run `magus login` to create one.")
				return nil
			}
			for name, p := range cfg.Profiles {
				marker := " "
				if name == cfg.DefaultProfile {
					marker = "*"
				}
				fmt.Printf("%s %-15s %s\n", marker, name, nonEmpty(p.Workspace, "Personal"))
			}
			return nil
		},
	}
}

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage profiles",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "use [name]",
		Short: "Switch the default profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name := args[0]
			if _, ok := cfg.Profiles[name]; !ok {
				return fmt.Errorf("profile %q not found", name)
			}
			cfg.DefaultProfile = name
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Printf("Default profile set to %q.\n", name)
			return nil
		},
	})
	return cmd
}
