package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

func newBlockCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "block", Short: "Manage blocks"}
	cmd.AddCommand(blockAddCmd(), blockEditCmd(), blockDeleteCmd())
	return cmd
}

func blockAddCmd() *cobra.Command {
	var text, blockType, language string
	var level int
	cmd := &cobra.Command{
		Use:   "add <page-id>",
		Args:  cobra.ExactArgs(1),
		Short: "Add a block to a page",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			b, err := c.AddBlock(args[0], api.AddBlockInput{
				Type: blockType, Text: text, Level: level, Language: language,
			})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(b)
			}
			fmt.Printf("Added %s block %s\n", b.Type, b.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "block text")
	cmd.Flags().StringVar(&blockType, "type", "paragraph", "block type")
	cmd.Flags().StringVar(&language, "language", "", "code language")
	cmd.Flags().IntVar(&level, "level", 0, "heading level (1-6)")
	return cmd
}

func blockEditCmd() *cobra.Command {
	var oldText, newText string
	var all bool
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Args:  cobra.ExactArgs(1),
		Short: "Edit a block (currently: --replace ... --with ...)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if oldText == "" {
				return fmt.Errorf("--replace is required")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			b, err := c.ReplaceBlockText(args[0], oldText, newText, all)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(b)
			}
			fmt.Println("Block updated")
			return nil
		},
	}
	cmd.Flags().StringVar(&oldText, "replace", "", "text to find")
	cmd.Flags().StringVar(&newText, "with", "", "replacement text")
	cmd.Flags().BoolVar(&all, "all", false, "replace all occurrences")
	return cmd
}

func blockDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Args:  cobra.ExactArgs(1),
		Short: "Delete a block",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			if err := c.DeleteBlock(args[0]); err != nil {
				return err
			}
			output.Println(quietMode, "Deleted.")
			return nil
		},
	}
}
