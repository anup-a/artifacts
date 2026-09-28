// Command byagent-mcp is a stdio MCP server for the byagent Artifacts API.
//
// It exposes list/get/comments/reply/resolve/collection/brief tools over
// JSON-RPC on stdio: newline-delimited, as MCP clients send it, or
// Content-Length framed (LSP-style). Auth matches the byagent CLI:
// ARTIFACTS_TOKEN + ARTIFACTS_API (or BYAGENT_* aliases), else
// ~/.artifacts/config.json. Default API is https://app.byagent.dev.
package main

import (
	"fmt"
	"os"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "byagent-mcp: %v\n", err)
		os.Exit(2)
	}
	client := newClient(cfg)
	srv := newServer(client)
	if err := srv.run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "byagent-mcp: %v\n", err)
		os.Exit(1)
	}
}
