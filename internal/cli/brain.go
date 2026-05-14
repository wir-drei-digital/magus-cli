package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

func newBrainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brain",
		Short: "Manage brains",
	}
	cmd.AddCommand(brainListCmd(), brainCreateCmd(), brainShowCmd(), brainArchiveCmd())
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
	return api.New(url, token), nil
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
			brains, err := c.ListBrains(api.ListBrainsOpts{})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(brains)
			}
			rows := make([][]string, len(brains))
			for i, b := range brains {
				rows[i] = []string{b.Slug, b.Title, b.UpdatedAt}
			}
			output.PrintTable([]string{"slug", "title", "updated"}, rows)
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
			brain, err := c.CreateBrain(api.CreateBrainInput{
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
			brain, err := c.GetBrain(args[0])
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(brain)
			}
			fmt.Printf("Title:       %s\nSlug:        %s\nDescription: %s\nUpdated:     %s\n",
				brain.Title, brain.Slug, brain.Description, brain.UpdatedAt)
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
			if err := c.ArchiveBrain(args[0]); err != nil {
				return err
			}
			output.Println(quietMode, "Archived.")
			return nil
		},
	}
}
