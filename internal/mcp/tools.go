package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/wir-drei-digital/magus-cli/internal/api"
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
				args := map[string]string{
					"title":       title,
					"description": stringArg(req, "description"),
					"icon":        stringArg(req, "icon"),
					"color":       stringArg(req, "color"),
				}
				return brainCreateCore(ctx, c, args)
			},
		},
		{
			def: mcpgo.NewTool("page_list",
				mcpgo.WithDescription("List pages in a brain as a tree or flat array."),
				mcpgo.WithString("brain", mcpgo.Required()),
				mcpgo.WithString("as", mcpgo.Description("tree (default) or flat"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				brain, err := req.RequireString("brain")
				if err != nil {
					return nil, err
				}
				asFlat := stringArg(req, "as") == "flat"
				return pageListCore(ctx, c, brain, asFlat)
			},
		},
		{
			def: mcpgo.NewTool("page_read",
				mcpgo.WithDescription("Read a page as markdown including all blocks."),
				mcpgo.WithString("page", mcpgo.Required())),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				page, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				return pageReadCore(ctx, c, page)
			},
		},
		{
			def: mcpgo.NewTool("page_write",
				mcpgo.WithDescription("Create or append to a page. Title supports slash-paths to auto-create ancestors."),
				mcpgo.WithString("brain", mcpgo.Required()),
				mcpgo.WithString("title", mcpgo.Required()),
				mcpgo.WithString("content", mcpgo.Description("Markdown content")),
				mcpgo.WithString("parent_page_id"),
				mcpgo.WithString("mode", mcpgo.Description("append (default) | create_only | replace"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				brain, err := req.RequireString("brain")
				if err != nil {
					return nil, err
				}
				title, err := req.RequireString("title")
				if err != nil {
					return nil, err
				}
				args := map[string]string{
					"title":          title,
					"content":        stringArg(req, "content"),
					"parent_page_id": stringArg(req, "parent_page_id"),
					"mode":           stringArg(req, "mode"),
				}
				return pageWriteCore(ctx, c, brain, args)
			},
		},
		{
			def: mcpgo.NewTool("page_update",
				mcpgo.WithDescription("Rename or move a page."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("title"),
				mcpgo.WithString("parent_page_id")),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				args := map[string]string{
					"title":          stringArg(req, "title"),
					"parent_page_id": stringArg(req, "parent_page_id"),
				}
				return pageUpdateCore(ctx, c, pageID, args)
			},
		},
		{
			def: mcpgo.NewTool("page_delete",
				mcpgo.WithDescription("Soft-delete a page (recoverable from trash for 30 days)."),
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
				mcpgo.WithDescription("Search across brain content. Returns ranked hits from page blocks and attached file chunks."),
				mcpgo.WithString("query", mcpgo.Required()),
				mcpgo.WithString("brain"),
				mcpgo.WithString("mode", mcpgo.Description("hybrid (default) | semantic | text")),
				mcpgo.WithNumber("limit")),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				query, err := req.RequireString("query")
				if err != nil {
					return nil, err
				}
				brain := stringArg(req, "brain")
				limit := 0
				if n, ok := numberArg(req, "limit"); ok {
					limit = int(n)
				}
				return brainSearchCore(ctx, c, activeBrain, brain, query, stringArg(req, "mode"), limit)
			},
		},
	}
}

// ---- core functions ---------------------------------------------------
// Each MCP tool's "do the work" path is factored out so it can be unit
// tested without needing to synthesize a real mcpgo.CallToolRequest.

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
	p, err := c.GetPage(ctx, pageID, "markdown")
	if err != nil {
		return nil, err
	}
	return jsonResult(p)
}

func pageWriteCore(ctx context.Context, c *api.Client, brainID string, args map[string]string) (*mcpgo.CallToolResult, error) {
	input := api.WritePageInput{
		Title:        args["title"],
		Content:      args["content"],
		ParentPageID: args["parent_page_id"],
		Mode:         args["mode"],
	}
	p, err := c.WritePage(ctx, brainID, input)
	if err != nil {
		return nil, err
	}
	return jsonResult(p)
}

func pageUpdateCore(ctx context.Context, c *api.Client, pageID string, args map[string]string) (*mcpgo.CallToolResult, error) {
	input := api.UpdatePageInput{}
	if t := args["title"]; t != "" {
		input.Title = &t
	}
	if p := args["parent_page_id"]; p != "" {
		input.ParentPageID = &p
	}
	p, err := c.UpdatePage(ctx, pageID, input)
	if err != nil {
		return nil, err
	}
	return jsonResult(p)
}

func pageDeleteCore(ctx context.Context, c *api.Client, pageID string) (*mcpgo.CallToolResult, error) {
	res, err := c.DeletePage(ctx, pageID)
	if err != nil {
		return nil, err
	}
	return jsonResult(res)
}

func brainSearchCore(ctx context.Context, c *api.Client, activeBrain, brainArg, query, mode string, limit int) (*mcpgo.CallToolResult, error) {
	brain := brainArg
	if brain == "" {
		brain = activeBrain
	}
	if brain == "" {
		return nil, fmt.Errorf("no brain specified (pass brain arg or run `magus brain use <id>`)")
	}
	input := api.SearchInput{
		Query: query,
		Mode:  mode,
		Limit: limit,
	}
	hits, err := c.Search(ctx, brain, input)
	if err != nil {
		return nil, err
	}
	return jsonResult(hits)
}

// stringArg returns a string argument or "" if absent/wrong type.
func stringArg(req mcpgo.CallToolRequest, key string) string {
	return req.GetString(key, "")
}

// numberArg returns a float64 argument and ok=true if it was provided.
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
