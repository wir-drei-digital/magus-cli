package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

func newSearchCmd() *cobra.Command {
	var brainFlag, mode string
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Args:  cobra.ExactArgs(1),
		Short: "Search brain content",
		Long: `Search across a brain's pages and file chunks.

If --brain is omitted the active brain (set via 'magus brain use <id>') is used.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			brainID := config.ResolveActiveBrain(cfg, brainFlag)
			if brainID == "" {
				return fmt.Errorf("no brain specified (use --brain <id> or `magus brain use <id>`)")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			hits, err := c.Search(cmd.Context(), brainID, api.SearchInput{
				Query: args[0], Mode: mode, Limit: limit,
			})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(hits)
			}
			for _, h := range hits {
				fmt.Printf("[%s %.2f] %s\n", h.Kind, h.Score, h.Snippet)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&brainFlag, "brain", "", "brain id or slug (defaults to active brain)")
	cmd.Flags().StringVar(&mode, "mode", "", "hybrid (default) | semantic | text")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results")
	return cmd
}
