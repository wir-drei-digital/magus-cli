package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

// shortID returns the first 8 characters of a UUID for compact display.
// If s is shorter than 8 characters, returns s unchanged.
func shortID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

func newBrainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brain",
		Short: "Manage brains",
	}
	cmd.AddCommand(
		brainListCmd(),
		brainCreateCmd(),
		brainShowCmd(),
		brainArchiveCmd(),
		brainUseCmd(),
		brainCurrentCmd(),
		brainUnsetCmd(),
	)
	return cmd
}

func loadClient() (*api.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	token := config.ResolveToken(cfg, profile)
	if token == "" {
		return nil, fmt.Errorf("no token configured (run `magus login`)")
	}
	url := config.ResolveAPIURL(cfg, profile, DefaultAPIURL)
	return api.New(url, token, "magus-cli/"+Version), nil
}

func brainListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List brains in the active workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			brains, err := c.ListBrains(cmd.Context(), api.ListBrainsOpts{})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(brains)
			}
			rows := make([][]string, len(brains))
			for i, b := range brains {
				rows[i] = []string{shortID(b.ID), b.Slug, b.Title, b.UpdatedAt}
			}
			output.PrintTable([]string{"id", "slug", "title", "updated"}, rows)
			return nil
		},
	}
}

func brainCreateCmd() *cobra.Command {
	var description, icon, color string
	cmd := &cobra.Command{
		Use:   "create <title>",
		Short: "Create a brain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			brain, err := c.CreateBrain(cmd.Context(), api.CreateBrainInput{
				Title: args[0], Description: description, Icon: icon, Color: color,
			})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(brain)
			}
			fmt.Printf("Created brain %s (%s)\n", brain.Title, brain.Slug)
			return nil
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "brain description")
	cmd.Flags().StringVar(&icon, "icon", "", "icon")
	cmd.Flags().StringVar(&color, "color", "", "color (hex)")
	return cmd
}

func brainShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id-or-slug>",
		Short: "Show a brain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			brain, err := c.GetBrain(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(brain)
			}
			fmt.Printf("ID:          %s\nTitle:       %s\nSlug:        %s\nDescription: %s\nUpdated:     %s\n",
				brain.ID, brain.Title, brain.Slug, brain.Description, brain.UpdatedAt)
			return nil
		},
	}
}

func brainArchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive <id-or-slug>",
		Short: "Archive a brain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			if err := c.ArchiveBrain(cmd.Context(), args[0]); err != nil {
				return err
			}
			output.Println(quietMode, "Archived.")
			return nil
		},
	}
}

// activeProfileName resolves the name of the profile to mutate for active-brain
// commands. It honours the global --profile override, otherwise defaults to
// cfg.DefaultProfile. Returns "" if no profile is configured.
func activeProfileName(cfg *config.Config) string {
	if profile != "" {
		return profile
	}
	return cfg.DefaultProfile
}

func brainUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <id-or-slug>",
		Short: "Set the active brain for this profile",
		Long: `Stores <id-or-slug> as the active brain for the current profile so that
subsequent commands (page list, search, page write) don't need --brain.

Resolution order everywhere:
  1. explicit --brain flag (or positional arg)
  2. the profile's active brain
  3. error`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name := activeProfileName(cfg)
			if name == "" {
				return fmt.Errorf("no profile configured (run `magus login`)")
			}
			p, ok := cfg.Profiles[name]
			if !ok {
				return fmt.Errorf("profile %q not found", name)
			}
			p.ActiveBrain = args[0]
			cfg.Profiles[name] = p
			if err := cfg.Save(); err != nil {
				return err
			}
			output.Println(quietMode, fmt.Sprintf("Active brain set to %q for profile %q.", args[0], name))
			return nil
		},
	}
}

func brainCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Print the active brain for the current profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			p, ok := cfg.Active(profile)
			if !ok || p.ActiveBrain == "" {
				// Empty stdout, error to stderr + non-zero exit (via main.go).
				return fmt.Errorf("no active brain")
			}
			fmt.Println(p.ActiveBrain)
			return nil
		},
	}
}

func brainUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset",
		Short: "Clear the active brain for the current profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name := activeProfileName(cfg)
			if name == "" {
				return fmt.Errorf("no profile configured (run `magus login`)")
			}
			p, ok := cfg.Profiles[name]
			if !ok {
				return fmt.Errorf("profile %q not found", name)
			}
			p.ActiveBrain = ""
			cfg.Profiles[name] = p
			if err := cfg.Save(); err != nil {
				return err
			}
			output.Println(quietMode, fmt.Sprintf("Cleared active brain for profile %q.", name))
			return nil
		},
	}
}
