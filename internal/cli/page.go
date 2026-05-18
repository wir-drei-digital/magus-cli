package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

func newPageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "page", Short: "Manage pages"}
	cmd.AddCommand(
		pageListCmd(),
		pageShowCmd(),
		pageWriteCmd(),
		pageRenameCmd(),
		pageMoveCmd(),
		pageDeleteCmd(),
	)
	return cmd
}

func pageListCmd() *cobra.Command {
	var brainFlag string
	var tree bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List pages",
		Long: `List pages in a brain.

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
			pages, err := c.ListPages(cmd.Context(), brainID, !tree)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(pages)
			}
			rows := make([][]string, 0, len(pages))
			collect(&rows, pages, "")
			output.PrintTable([]string{"title", "slug", "depth"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&brainFlag, "brain", "", "brain id or slug (defaults to active brain)")
	cmd.Flags().BoolVar(&tree, "tree", false, "render tree-shaped output")
	return cmd
}

func collect(rows *[][]string, pages []api.Page, indent string) {
	for _, p := range pages {
		*rows = append(*rows, []string{indent + p.Title, p.Slug, fmt.Sprintf("%d", p.Depth)})
		if len(p.Children) > 0 {
			collect(rows, p.Children, indent+"  ")
		}
	}
}

func pageShowCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "show <id>",
		Args:  cobra.ExactArgs(1),
		Short: "Show a page",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			page, err := c.GetPage(cmd.Context(), args[0], format)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			if format == "markdown" {
				fmt.Println(page.Markdown)
				return nil
			}
			fmt.Printf("Title: %s\nSlug: %s\nDepth: %d\nUpdated: %s\n",
				page.Title, page.Slug, page.Depth, page.UpdatedAt)
			for _, b := range page.Blocks {
				fmt.Printf("  [%s] %v\n", b.Type, b.Content)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "blocks", "blocks | markdown | json")
	return cmd
}

func pageWriteCmd() *cobra.Command {
	var file, mode, parent string
	cmd := &cobra.Command{
		Use:   "write [brain] <title>",
		Args:  cobra.RangeArgs(1, 2),
		Short: "Create or append to a page (reads markdown from stdin or --file)",
		Long: `Title supports slash-paths like "Projects/Magus/API" to auto-create
ancestor pages. Markdown content is read from --file or from stdin if
--file is omitted.

If only one positional arg is given it is treated as the title and the
brain is taken from the active brain (set via 'magus brain use <id>').
With two positional args the first is the brain id-or-slug.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var brainID, title string
			if len(args) == 2 {
				brainID = args[0]
				title = args[1]
			} else {
				title = args[0]
			}
			if brainID == "" {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				brainID = config.ResolveActiveBrain(cfg, "")
			}
			if brainID == "" {
				return fmt.Errorf("no brain specified (pass <brain> or run `magus brain use <id>`)")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			content, err := readContent(file)
			if err != nil {
				return err
			}
			page, err := c.WritePage(cmd.Context(), brainID, api.WritePageInput{
				Title:        title,
				Content:      content,
				ParentPageID: parent,
				Mode:         mode,
			})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			fmt.Printf("Wrote page %q (%d blocks)\n", page.Title, page.BlocksAdded)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "markdown file path; defaults to stdin")
	cmd.Flags().StringVar(&mode, "mode", "", "append (default) | create_only | replace")
	cmd.Flags().StringVar(&parent, "parent", "", "parent page id")
	return cmd
}

func readContent(file string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return "", nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func pageRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <id> <title>",
		Args:  cobra.ExactArgs(2),
		Short: "Rename a page",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			title := args[1]
			p, err := c.UpdatePage(cmd.Context(), args[0], api.UpdatePageInput{Title: &title})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(p)
			}
			fmt.Printf("Renamed to %q\n", p.Title)
			return nil
		},
	}
}

func pageMoveCmd() *cobra.Command {
	var parent string
	cmd := &cobra.Command{
		Use:   "move <id>",
		Args:  cobra.ExactArgs(1),
		Short: "Move a page under another parent (or 'none' to move to root)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			var parentPtr *string
			if parent == "none" {
				empty := ""
				parentPtr = &empty
			} else if parent != "" {
				parentPtr = &parent
			} else {
				return fmt.Errorf("--parent is required (use 'none' to move to root)")
			}
			p, err := c.UpdatePage(cmd.Context(), args[0], api.UpdatePageInput{ParentPageID: parentPtr})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(p)
			}
			fmt.Printf("Moved %q\n", p.Title)
			return nil
		},
	}
	cmd.Flags().StringVar(&parent, "parent", "", "new parent page id, or 'none'")
	return cmd
}

func pageDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Args:  cobra.ExactArgs(1),
		Short: "Soft-delete a page",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			res, err := c.DeletePage(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(res)
			}
			fmt.Printf("Trashed (deleted_at=%s)\n", res.DeletedAt)
			return nil
		},
	}
}
