package mcp

import (
	"context"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/wir-drei-digital/magus-cli/internal/api"
)

// Serve runs the MCP stdio server, registering all brain tools.
// activeBrain is used as a fallback for tools that take an optional brain
// argument (currently brain_search). Pass "" if no active brain is configured.
func Serve(_ context.Context, client *api.Client, version, activeBrain string) error {
	if version == "" {
		version = "dev"
	}
	s := server.NewMCPServer(
		"magus-brain",
		version,
		server.WithToolCapabilities(true),
	)

	for _, tool := range tools(client, activeBrain) {
		s.AddTool(tool.def, tool.handler)
	}

	return server.ServeStdio(s)
}

type registeredTool struct {
	def     mcpgo.Tool
	handler server.ToolHandlerFunc
}
