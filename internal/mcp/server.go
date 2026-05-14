package mcp

import (
	"context"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wir-drei-digital/magus-cli/internal/api"
)

// Serve runs the MCP stdio server, registering all brain tools.
func Serve(_ context.Context, client *api.Client) error {
	s := server.NewMCPServer(
		"magus-brain",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	for _, tool := range tools(client) {
		s.AddTool(tool.def, tool.handler)
	}

	return server.ServeStdio(s)
}

type registeredTool struct {
	def     mcpgo.Tool
	handler server.ToolHandlerFunc
}
