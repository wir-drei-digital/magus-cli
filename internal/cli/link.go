package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

func newLinkCmd() *cobra.Command {
	var typ, targetKind string
	cmd := &cobra.Command{
		Use:   "link <source-block-id> <target-id>",
		Args:  cobra.ExactArgs(2),
		Short: "Create a connection between two blocks (or block to page)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			input := api.CreateConnectionBlockLevel{
				SourceBlockID: args[0],
				Type:          typ,
			}
			if targetKind == "page" {
				input.TargetPageID = args[1]
			} else {
				input.TargetBlockID = args[1]
			}
			conn, err := c.CreateBlockConnection(input)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(conn)
			}
			fmt.Printf("Linked %s (id=%s)\n", typ, conn.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&typ, "type", "relates_to", "connection type")
	cmd.Flags().StringVar(&targetKind, "target-kind", "block", "block | page")
	return cmd
}
