package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/brain"
)

func tools(c *api.Client, activeBrain string) []registeredTool {
	return []registeredTool{
		{
			def: mcpgo.NewTool("brain_list",
				mcpgo.WithDescription("List all brains in the active workspace.")),
			handler: func(ctx context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				return brainListCore(ctx, c)
			},
		},
		{
			def: mcpgo.NewTool("brain_create",
				mcpgo.WithDescription("Create a new brain in the active workspace."),
				mcpgo.WithString("title", mcpgo.Required(), mcpgo.Description("Brain title")),
				mcpgo.WithString("description", mcpgo.Description("Optional description")),
				mcpgo.WithString("icon"),
				mcpgo.WithString("color")),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				title, err := req.RequireString("title")
				if err != nil {
					return nil, err
				}
				return brainCreateCore(ctx, c, map[string]string{
					"title":       title,
					"description": stringArg(req, "description"),
					"icon":        stringArg(req, "icon"),
					"color":       stringArg(req, "color"),
				})
			},
		},
		{
			def: mcpgo.NewTool("page_list",
				mcpgo.WithDescription("List pages in a brain as a tree or flat array."),
				mcpgo.WithString("brain", mcpgo.Required()),
				mcpgo.WithString("as", mcpgo.Description("tree (default) or flat"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				brainRef, err := req.RequireString("brain")
				if err != nil {
					return nil, err
				}
				return pageListCore(ctx, c, brainRef, stringArg(req, "as") == "flat")
			},
		},
		{
			def: mcpgo.NewTool("page_read",
				mcpgo.WithDescription("Read a page's markdown body."),
				mcpgo.WithString("page", mcpgo.Required(), mcpgo.Description("Page id"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				return pageReadCore(ctx, c, pageID)
			},
		},
		{
			def: mcpgo.NewTool("page_create",
				mcpgo.WithDescription("Create a new page with an optional markdown body. Body may use frontmatter, [[wikilinks]], ```source/```callout fenced blocks, magus://file links, and #tags."),
				mcpgo.WithString("brain", mcpgo.Required()),
				mcpgo.WithString("title", mcpgo.Required()),
				mcpgo.WithString("body", mcpgo.Description("Markdown body")),
				mcpgo.WithString("parent_page_id", mcpgo.Description("Parent page id for nesting"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				brainRef, err := req.RequireString("brain")
				if err != nil {
					return nil, err
				}
				title, err := req.RequireString("title")
				if err != nil {
					return nil, err
				}
				return pageCreateCore(ctx, c, brainRef, api.CreatePageInput{
					Title:        title,
					Body:         stringArg(req, "body"),
					ParentPageID: stringArg(req, "parent_page_id"),
				})
			},
		},
		{
			def: mcpgo.NewTool("page_append",
				mcpgo.WithDescription("Append markdown to the end of a page body."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("body", mcpgo.Required())),
			handler: bodyEditHandler(c, "append"),
		},
		{
			def: mcpgo.NewTool("page_prepend",
				mcpgo.WithDescription("Prepend markdown to the start of a page body."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("body", mcpgo.Required())),
			handler: bodyEditHandler(c, "prepend"),
		},
		{
			def: mcpgo.NewTool("page_replace",
				mcpgo.WithDescription("Overwrite a page's entire body. Destructive."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("body", mcpgo.Required())),
			handler: bodyEditHandler(c, "replace"),
		},
		{
			def: mcpgo.NewTool("page_edit",
				mcpgo.WithDescription("Find-and-replace within a page body. The find text must match exactly once unless all=true."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("find", mcpgo.Required()),
				mcpgo.WithString("with", mcpgo.Description("Replacement text")),
				mcpgo.WithBoolean("all", mcpgo.Description("Replace every occurrence"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				find, err := req.RequireString("find")
				if err != nil {
					return nil, err
				}
				return pageEditCore(ctx, c, pageID, find, stringArg(req, "with"), boolArg(req, "all"))
			},
		},
		{
			def: mcpgo.NewTool("page_clear",
				mcpgo.WithDescription("Empty a page's body; the page itself is kept."),
				mcpgo.WithString("page", mcpgo.Required())),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				return resultFromPage(c.ClearPage(ctx, pageID))
			},
		},
		{
			def: mcpgo.NewTool("page_undo",
				mcpgo.WithDescription("Undo the last body change on a page."),
				mcpgo.WithString("page", mcpgo.Required())),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				return resultFromPage(c.UndoPage(ctx, pageID))
			},
		},
		{
			def: mcpgo.NewTool("page_rename",
				mcpgo.WithDescription("Rename a page."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("title", mcpgo.Required())),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				title, err := req.RequireString("title")
				if err != nil {
					return nil, err
				}
				return resultFromPage(c.UpdatePage(ctx, pageID, api.UpdatePageInput{Title: &title}))
			},
		},
		{
			def: mcpgo.NewTool("page_move",
				mcpgo.WithDescription("Move a page under another parent. Pass an empty parent_page_id to move to root."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("parent_page_id", mcpgo.Description("New parent page id, or empty for root"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				parent := stringArg(req, "parent_page_id")
				return resultFromPage(c.UpdatePage(ctx, pageID, api.UpdatePageInput{ParentPageID: &parent}))
			},
		},
		{
			def: mcpgo.NewTool("page_delete",
				mcpgo.WithDescription("Soft-delete a page (recoverable from trash)."),
				mcpgo.WithString("page", mcpgo.Required())),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				return pageDeleteCore(ctx, c, pageID)
			},
		},
		{
			def: mcpgo.NewTool("brain_search",
				mcpgo.WithDescription("Search across brain content. Returns ranked hits from page bodies and attached file chunks."),
				mcpgo.WithString("query", mcpgo.Required()),
				mcpgo.WithString("brain"),
				mcpgo.WithString("kind", mcpgo.Description("unified (default) | semantic | text")),
				mcpgo.WithNumber("limit")),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				query, err := req.RequireString("query")
				if err != nil {
					return nil, err
				}
				limit := 0
				if n, ok := numberArg(req, "limit"); ok {
					limit = int(n)
				}
				return brainSearchCore(ctx, c, activeBrain, stringArg(req, "brain"), query, stringArg(req, "kind"), limit)
			},
		},
	}
}

// ---- core functions ---------------------------------------------------

func brainListCore(ctx context.Context, c *api.Client) (*mcpgo.CallToolResult, error) {
	brains, err := c.ListBrains(ctx, api.ListBrainsOpts{})
	if err != nil {
		return nil, err
	}
	return jsonResult(brains)
}

func brainCreateCore(ctx context.Context, c *api.Client, args map[string]string) (*mcpgo.CallToolResult, error) {
	brain, err := c.CreateBrain(ctx, api.CreateBrainInput{
		Title:       args["title"],
		Description: args["description"],
		Icon:        args["icon"],
		Color:       args["color"],
	})
	if err != nil {
		return nil, err
	}
	return jsonResult(brain)
}

func pageListCore(ctx context.Context, c *api.Client, brainID string, asFlat bool) (*mcpgo.CallToolResult, error) {
	pages, err := c.ListPages(ctx, brainID, asFlat)
	if err != nil {
		return nil, err
	}
	return jsonResult(pages)
}

func pageReadCore(ctx context.Context, c *api.Client, pageID string) (*mcpgo.CallToolResult, error) {
	return resultFromPage(c.GetPage(ctx, pageID, ""))
}

func pageCreateCore(ctx context.Context, c *api.Client, brainID string, input api.CreatePageInput) (*mcpgo.CallToolResult, error) {
	return resultFromPage(c.CreatePage(ctx, brainID, input))
}

func pageEditCore(ctx context.Context, c *api.Client, pageID, find, with string, all bool) (*mcpgo.CallToolResult, error) {
	page, err := c.GetPage(ctx, pageID, "")
	if err != nil {
		return nil, err
	}
	next, err := brain.ApplyFindReplace(page.Body, find, with, all)
	if err != nil {
		return nil, err
	}
	return resultFromPage(c.UpdatePageBody(ctx, pageID, next, "replace"))
}

func pageDeleteCore(ctx context.Context, c *api.Client, pageID string) (*mcpgo.CallToolResult, error) {
	res, err := c.DeletePage(ctx, pageID)
	if err != nil {
		return nil, err
	}
	return jsonResult(res)
}

func brainSearchCore(ctx context.Context, c *api.Client, activeBrain, brainArg, query, kind string, limit int) (*mcpgo.CallToolResult, error) {
	brainRef := brainArg
	if brainRef == "" {
		brainRef = activeBrain
	}
	if brainRef == "" {
		return nil, fmt.Errorf("no brain specified (pass brain arg or run `magus brain use <id>`)")
	}
	hits, err := c.Search(ctx, brainRef, api.SearchInput{Query: query, Kind: kind, Limit: limit})
	if err != nil {
		return nil, err
	}
	return jsonResult(hits)
}

// ---- helpers ----------------------------------------------------------

// bodyEditHandler builds an append/prepend/replace MCP handler.
func bodyEditHandler(c *api.Client, mode string) func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		pageID, err := req.RequireString("page")
		if err != nil {
			return nil, err
		}
		body, err := req.RequireString("body")
		if err != nil {
			return nil, err
		}
		return resultFromPage(c.UpdatePageBody(ctx, pageID, body, mode))
	}
}

func resultFromPage(p *api.Page, err error) (*mcpgo.CallToolResult, error) {
	if err != nil {
		return nil, err
	}
	return jsonResult(p)
}

func stringArg(req mcpgo.CallToolRequest, key string) string {
	return req.GetString(key, "")
}

func boolArg(req mcpgo.CallToolRequest, key string) bool {
	return req.GetBool(key, false)
}

func numberArg(req mcpgo.CallToolRequest, key string) (float64, bool) {
	v, err := req.RequireFloat(key)
	if err != nil {
		return 0, false
	}
	return v, true
}

func jsonResult(payload any) (*mcpgo.CallToolResult, error) {
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcpgo.NewToolResultText(string(b)), nil
}
