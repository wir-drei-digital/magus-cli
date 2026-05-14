package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

func newSearchCmd() *cobra.Command {
	var brainID, mode string
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Args:  cobra.ExactArgs(1),
		Short: "Search brain content",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			hits, err := c.Search(brainID, api.SearchInput{
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
	cmd.Flags().StringVar(&brainID, "brain", "", "brain id or slug")
	cmd.Flags().StringVar(&mode, "mode", "", "hybrid (default) | semantic | text")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results")
	_ = cmd.MarkFlagRequired("brain")
	return cmd
}
