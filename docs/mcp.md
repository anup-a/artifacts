# byagent MCP

There are two servers. Both expose the same read and comment tools; only the
remote one can publish.

## Remote (Streamable HTTP): `https://mcp.byagent.dev/mcp`

For clients that take an MCP URL rather than a command: Grok Bot connectors,
claude.ai, ChatGPT. Source: `mcp/` (Node, reuses the CLI's API client and
Markdown renderer). Runs as the `mcp` service on the box (`deploy/box`) and is
shipped with the app by `deploy/box/ship.sh`.

- Stateless: every `POST /mcp` is answered with plain JSON; no sessions, no SSE. `GET /mcp` is 405.
- Auth: the caller's own byagent key on every request, `Authorization: Bearer <key>` (or `X-Byagent-Token`). `initialize` and `tools/list` work without one; `tools/call` does not. The server stores no keys.
- Tools: `artifact_publish` (Markdown or HTML, new page or new version with `id`) plus the stdio tools below.
- Health: `GET /health`. Tests: `node --test mcp/test/*.test.mjs` (after `npm --prefix cli ci`).
- Routing: the Cloudflare tunnel sends `mcp.byagent.dev` to `http://mcp:8090`; that rule sits above the `*.byagent.dev` wildcard.

## Local (stdio): `byagent-mcp`

Install the P0 MCP server so agents can list/get artifacts and work comments without inventing API calls.

Build once from this repo:

```bash
go build -o "$HOME/bin/byagent-mcp" ./cmd/byagent-mcp
```

(or `go run ./cmd/byagent-mcp` with a wrapper script). Put the binary on your `PATH`.

Auth (required): set `ARTIFACTS_TOKEN` (or `BYAGENT_API_KEY`) in the MCP server env, or rely on `~/.artifacts/config.json` from `byagent login`. Optional `ARTIFACTS_API` / `BYAGENT_API` (default `https://app.byagent.dev`).

Tools: `artifact_list`, `artifact_get`, `artifact_comments`, `artifact_reply`, `artifact_resolve`, `collection_list`, `collection_get`, `brief_get`.  
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

With the server on stdio you should see `tools/list` advertise the seven tools above. A bad token yields a tool result with `isError: true` (not a JSON-RPC transport error).
