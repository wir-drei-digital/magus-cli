package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/brain"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

func newPageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "page", Short: "Manage pages"}
	cmd.AddCommand(
		pageListCmd(),
		pageShowCmd(),
		pageCreateCmd(),
		pageAppendCmd(),
		pagePrependCmd(),
		pageReplaceCmd(),
		pageEditCmd(),
		pageClearCmd(),
		pageUndoCmd(),
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
	cmd := &cobra.Command{
		Use:   "show <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Show a page (prints its markdown body)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			page, err := c.GetPage(cmd.Context(), pageID)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			fmt.Println(page.Body)
			return nil
		},
	}
	return cmd
}

func pageCreateCmd() *cobra.Command {
	var brainFlag, parent, file string
	cmd := &cobra.Command{
		Use:   "create <title>",
		Args:  cobra.ExactArgs(1),
		Short: "Create a new page (body from stdin or --file)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			brainID := config.ResolveActiveBrain(cfg, brainFlag)
			if brainID == "" {
				return fmt.Errorf("no brain specified (use --brain <id> or `magus brain use <id>`)")
			}
			body, err := readContent(file)
			if err != nil {
				return err
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			input := api.CreatePageInput{Title: args[0], Body: body}
			if parent != "" {
				parentID, err := resolvePage(cmd.Context(), c, parent)
				if err != nil {
					return fmt.Errorf("resolve --parent: %w", err)
				}
				input.ParentPageID = parentID
			}
			page, err := c.CreatePage(cmd.Context(), brainID, input)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			output.Println(quietMode, fmt.Sprintf("Created %q (%s)", page.Title, page.Slug))
			return nil
		},
	}
	cmd.Flags().StringVar(&brainFlag, "brain", "", "brain id or slug (defaults to active brain)")
	cmd.Flags().StringVar(&parent, "parent", "", "parent page ref (id|slug|brain/slug)")
	cmd.Flags().StringVar(&file, "file", "", "markdown file path; defaults to stdin")
	return cmd
}

// bodyEditCmd builds append/prepend/replace: each reads markdown from
// stdin/--file and PATCHes the page body with the given mode.
func bodyEditCmd(use, short, mode string) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   use,
		Args:  cobra.ExactArgs(1),
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readContent(file)
			if err != nil {
				return err
			}
			if body == "" {
				return fmt.Errorf("no content provided (pipe markdown via stdin or pass --file)")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			page, err := c.UpdatePageBody(cmd.Context(), pageID, body, mode)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			output.Println(quietMode, fmt.Sprintf("Updated %q (%s)", page.Title, mode))
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "markdown file path; defaults to stdin")
	return cmd
}

func pageAppendCmd() *cobra.Command {
	return bodyEditCmd("append <ref>", "Append markdown to a page", "append")
}

func pagePrependCmd() *cobra.Command {
	return bodyEditCmd("prepend <ref>", "Prepend markdown to a page", "prepend")
}

func pageReplaceCmd() *cobra.Command {
	return bodyEditCmd("replace <ref>", "Overwrite a page's entire body", "replace")
}

func pageEditCmd() *cobra.Command {
	var find, with string
	var all bool
	cmd := &cobra.Command{
		Use:   "edit <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Find-and-replace within a page body",
		RunE: func(cmd *cobra.Command, args []string) error {
			if find == "" {
				return fmt.Errorf("--find is required")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			page, err := c.GetPage(cmd.Context(), pageID)
			if err != nil {
				return err
			}
			next, err := brain.ApplyFindReplace(page.Body, find, with, all)
			if err != nil {
				return err
			}
			updated, err := c.UpdatePageBody(cmd.Context(), pageID, next, "replace")
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(updated)
			}
			output.Println(quietMode, fmt.Sprintf("Edited %q", updated.Title))
			return nil
		},
	}
	cmd.Flags().StringVar(&find, "find", "", "text to find (must match exactly once unless --all)")
	cmd.Flags().StringVar(&with, "with", "", "replacement text")
	cmd.Flags().BoolVar(&all, "all", false, "replace all occurrences")
	return cmd
}

func pageClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Empty a page's body (the page itself is kept)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			page, err := c.ClearPage(cmd.Context(), pageID)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			output.Println(quietMode, fmt.Sprintf("Cleared %q", page.Title))
			return nil
		},
	}
}

func pageUndoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "undo <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Undo the last body change on a page",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			page, err := c.UndoPage(cmd.Context(), pageID)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			output.Println(quietMode, fmt.Sprintf("Reverted last change on %q", page.Title))
			return nil
		},
	}
}

func pageRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <ref> <title>",
		Args:  cobra.ExactArgs(2),
		Short: "Rename a page",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			title := args[1]
			p, err := c.UpdatePage(cmd.Context(), pageID, api.UpdatePageInput{Title: &title})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(p)
			}
			output.Println(quietMode, fmt.Sprintf("Renamed to %q", p.Title))
			return nil
		},
	}
}

func pageMoveCmd() *cobra.Command {
	var parent string
	cmd := &cobra.Command{
		Use:   "move <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Move a page under another parent (or 'none' for root)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if parent == "" {
				return fmt.Errorf("--parent is required (use 'none' to move to root)")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			var parentPtr *string
			if parent == "none" {
				empty := ""
				parentPtr = &empty
			} else {
				parentID, err := resolvePage(cmd.Context(), c, parent)
				if err != nil {
					return fmt.Errorf("resolve --parent: %w", err)
				}
				parentPtr = &parentID
			}
			p, err := c.UpdatePage(cmd.Context(), pageID, api.UpdatePageInput{ParentPageID: parentPtr})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(p)
			}
			output.Println(quietMode, fmt.Sprintf("Moved %q", p.Title))
			return nil
		},
	}
	cmd.Flags().StringVar(&parent, "parent", "", "new parent page ref, or 'none'")
	return cmd
}

func pageDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Soft-delete a page (recoverable from trash)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			res, err := c.DeletePage(cmd.Context(), pageID)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(res)
			}
			output.Println(quietMode, fmt.Sprintf("Trashed (deleted_at=%s)", res.DeletedAt))
			return nil
		},
	}
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
