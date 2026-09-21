# byagent MCP (stdio)

Install the P0 MCP server so agents can list/get artifacts and work comments without inventing API calls.

Build once from this repo:

```bash
go build -o "$HOME/bin/byagent-mcp" ./cmd/byagent-mcp
```

(or `go run ./cmd/byagent-mcp` with a wrapper script). Put the binary on your `PATH`.

Auth (required): set `ARTIFACTS_TOKEN` (or `BYAGENT_API_KEY`) in the MCP server env, or rely on `~/.artifacts/config.json` from `byagent login`. Optional `ARTIFACTS_API` / `BYAGENT_API` (default `https://app.byagent.dev`).

Tools: `artifact_list`, `artifact_get`, `artifact_comments`, `artifact_reply`, `artifact_resolve`.  
No publish in P0. **No fake search**: `artifact_list` takes `q`, which is the
case-insensitive title match the dashboard's search box runs — not a semantic
or full-text search of page contents. Combine it with `project` / `end_user`,
or page with `cursor`.

## Claude Code

Add to `~/.claude.json` (or project `.mcp.json`):

```json
{
  "mcpServers": {
    "byagent": {
      "command": "byagent-mcp",
      "env": {
        "ARTIFACTS_TOKEN": "${ARTIFACTS_TOKEN}"
      }
    }
  }
}
```

Or pass the key explicitly:

```json
{
  "mcpServers": {
    "byagent": {
      "command": "byagent-mcp",
      "env": {
        "ARTIFACTS_TOKEN": "sk_your_key_here",
        "ARTIFACTS_API": "https://app.byagent.dev"
      }
    }
  }
}
```

Restart Claude Code after editing.

## Cursor

Cursor Settings → MCP → Add server, or edit `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "byagent": {
      "command": "byagent-mcp",
      "env": {
        "ARTIFACTS_TOKEN": "sk_your_key_here",
        "ARTIFACTS_API": "https://app.byagent.dev"
      }
    }
  }
}
```

If the binary is not on `PATH`, use an absolute `command` path.

## Codex CLI

In `~/.codex/config.toml` (or the project config Codex reads):

```toml
[mcp_servers.byagent]
command = "byagent-mcp"

[mcp_servers.byagent.env]
ARTIFACTS_TOKEN = "sk_your_key_here"
ARTIFACTS_API = "https://app.byagent.dev"
```

## Smoke check

With the server on stdio you should see `tools/list` advertise the five tools above. A bad token yields a tool result with `isError: true` (not a JSON-RPC transport error).
