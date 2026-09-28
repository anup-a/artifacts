# byagent-mcp

Stdio [MCP](https://modelcontextprotocol.io/) server for the byagent Artifacts owner API.

P0 tools (no publish, no fake search):

| Tool | HTTP (`internal/api/router.go`) |
| --- | --- |
| `artifact_list` | `GET /api/artifacts?limit&cursor&end_user&project` |
| `artifact_get` | `GET /api/artifacts/{id}` |
| `artifact_comments` | `GET /api/artifacts/{id}/comments?status=&since=` |
| `artifact_reply` | `POST /api/artifacts/{id}/comments/{thread}/reply` |
| `artifact_resolve` | `POST /api/artifacts/{id}/comments/{thread}/resolve` |
| `collection_list` | `GET /api/collections` |
| `collection_get` | `GET /api/collections/{name}` |
| `brief_get` | `GET /api/artifacts?project=` (tag `brief`), then `GET /api/artifacts/{id}/files/brief.md` and open comments |

## Auth

Same as the `byagent` CLI:

1. `ARTIFACTS_TOKEN` + optional `ARTIFACTS_API`
2. Aliases: `BYAGENT_API_KEY` / `BYAGENT_API`
3. Else `~/.artifacts/config.json` `{ "token": "...", "api": "..." }`
4. Default API when unset: `https://app.byagent.dev`

Create a key at https://app.byagent.dev/app/keys.

Authorization header: `Authorization: Bearer <token>`.

## Build

From the repo root (Go 1.22+):

```bash
go build -o byagent-mcp ./cmd/byagent-mcp
```

## Run

```bash
export ARTIFACTS_TOKEN=...   # or use ~/.artifacts/config.json
./byagent-mcp                # speaks MCP on stdin/stdout
```

Install snippets for Claude Code / Cursor / Codex: see [`docs/mcp.md`](../../docs/mcp.md).

## Out of scope (P0)

- `artifact_publish` — deferred (multipart + preflight)
- Full-text `artifact_search` — **not implemented**; there is no search route. Use `project` / `end_user` filters on `artifact_list`.
