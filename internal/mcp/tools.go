package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/wir-drei-digital/magus-cli/internal/api"
)

func tools(c *api.Client) []registeredTool {
	return []registeredTool{
		{
			def: mcpgo.NewTool("brain_list",
				mcpgo.WithDescription("List all brains in the active workspace.")),
			handler: func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				brains, err := c.ListBrains(api.ListBrainsOpts{})
				if err != nil {
					return nil, err
				}
				return jsonResult(brains)
			},
		},
		{
			def: mcpgo.NewTool("brain_create",
				mcpgo.WithDescription("Create a new brain in the active workspace."),
				mcpgo.WithString("title", mcpgo.Required(), mcpgo.Description("Brain title")),
				mcpgo.WithString("description", mcpgo.Description("Optional description")),
				mcpgo.WithString("icon"),
				mcpgo.WithString("color")),
			handler: func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				title, err := req.RequireString("title")
				if err != nil {
					return nil, err
				}
				brain, err := c.CreateBrain(api.CreateBrainInput{
					Title:       title,
					Description: stringArg(req, "description"),
					Icon:        stringArg(req, "icon"),
					Color:       stringArg(req, "color"),
				})
				if err != nil {
					return nil, err
				}
				return jsonResult(brain)
			},
		},
		{
			def: mcpgo.NewTool("page_list",
				mcpgo.WithDescription("List pages in a brain as a tree or flat array."),
				mcpgo.WithString("brain", mcpgo.Required()),
				mcpgo.WithString("as", mcpgo.Description("tree (default) or flat"))),
			handler: func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				brain, err := req.RequireString("brain")
				if err != nil {
					return nil, err
				}
				asFlat := stringArg(req, "as") == "flat"
				pages, err := c.ListPages(brain, asFlat)
				if err != nil {
					return nil, err
				}
				return jsonResult(pages)
			},
		},
		{
			def: mcpgo.NewTool("page_read",
				mcpgo.WithDescription("Read a page as markdown including all blocks."),
				mcpgo.WithString("page", mcpgo.Required())),
			handler: func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				page, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				p, err := c.GetPage(page, "markdown")
				if err != nil {
					return nil, err
				}
				return jsonResult(p)
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
			handler: func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				brain, err := req.RequireString("brain")
				if err != nil {
					return nil, err
				}
				title, err := req.RequireString("title")
				if err != nil {
					return nil, err
				}
				input := api.WritePageInput{
					Title:        title,
					Content:      stringArg(req, "content"),
					ParentPageID: stringArg(req, "parent_page_id"),
					Mode:         stringArg(req, "mode"),
				}
				p, err := c.WritePage(brain, input)
				if err != nil {
					return nil, err
				}
				return jsonResult(p)
			},
		},
		{
			def: mcpgo.NewTool("page_update",
				mcpgo.WithDescription("Rename or move a page."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("title"),
				mcpgo.WithString("parent_page_id")),
			handler: func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				input := api.UpdatePageInput{}
				if t := stringArg(req, "title"); t != "" {
					input.Title = &t
				}
				if p := stringArg(req, "parent_page_id"); p != "" {
					input.ParentPageID = &p
				}
				p, err := c.UpdatePage(pageID, input)
				if err != nil {
					return nil, err
				}
				return jsonResult(p)
			},
		},
		{
			def: mcpgo.NewTool("page_delete",
				mcpgo.WithDescription("Soft-delete a page (recoverable from trash for 30 days)."),
				mcpgo.WithString("page", mcpgo.Required())),
			handler: func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				res, err := c.DeletePage(pageID)
				if err != nil {
					return nil, err
				}
				return jsonResult(res)
			},
		},
		{
			def: mcpgo.NewTool("brain_search",
				mcpgo.WithDescription("Search across brain content. Returns ranked hits from page blocks and attached file chunks."),
				mcpgo.WithString("query", mcpgo.Required()),
				mcpgo.WithString("brain"),
				mcpgo.WithString("mode", mcpgo.Description("hybrid (default) | semantic | text")),
				mcpgo.WithNumber("limit")),
			handler: func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				query, err := req.RequireString("query")
				if err != nil {
					return nil, err
				}
				brain := stringArg(req, "brain")
				if brain == "" {
					return nil, fmt.Errorf("brain is required for search (default-brain resolution not yet implemented)")
				}
				input := api.SearchInput{
					Query: query,
					Mode:  stringArg(req, "mode"),
				}
				if n, ok := numberArg(req, "limit"); ok {
					input.Limit = int(n)
				}
				hits, err := c.Search(brain, input)
				if err != nil {
					return nil, err
				}
				return jsonResult(hits)
			},
		},
	}
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
