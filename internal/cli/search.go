package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

func newSearchCmd() *cobra.Command {
	var brainFlag, kind string
	var limit int
	var crossBrain bool
	cmd := &cobra.Command{
		Use:   "search <query>",
		Args:  cobra.ExactArgs(1),
		Short: "Search brain content",
		Long: `Search across a brain's pages and attached file chunks.

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
				Query: args[0], Kind: kind, Limit: limit, CrossBrain: crossBrain,
			})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(hits)
			}
			for _, h := range hits {
				score := h.Score
				if score == 0 {
					score = h.Rank
				}
				label := h.Title
				if label == "" {
					label = h.PageID
				}
				fmt.Printf("[%s %.2f] %s  %s\n", h.Kind, score, label, h.Snippet)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&brainFlag, "brain", "", "brain id or slug (defaults to active brain)")
	cmd.Flags().StringVar(&kind, "kind", "", "unified (default) | semantic | text")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results")
	cmd.Flags().BoolVar(&crossBrain, "cross-brain", false, "search across all accessible brains")
	return cmd
}
