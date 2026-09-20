package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// MCP tool/result shapes (subset of the 2024-11-05 protocol used by Claude/Cursor/Codex).

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type server struct {
	client *apiClient
}

func newServer(c *apiClient) *server {
	return &server{client: c}
}

func (s *server) run(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	for {
		raw, err := readMessage(r)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			if werr := writeRPCError(out, nil, rpcParseError, "parse error"); werr != nil {
				return werr
			}
			continue
		}
		if req.JSONRPC != "" && req.JSONRPC != "2.0" {
			if req.ID != nil {
				if werr := writeRPCError(out, req.ID, rpcInvalidRequest, "jsonrpc must be 2.0"); werr != nil {
					return werr
				}
			}
			continue
		}
		if err := s.handle(out, req); err != nil {
			return err
		}
	}
}

func (s *server) handle(out io.Writer, req jsonRPCRequest) error {
	isNotify := req.ID == nil
	switch req.Method {
	case "notifications/initialized", "initialized":
		return nil
	case "initialize":
		if isNotify {
			return nil
		}
		return writeResponse(out, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "byagent-mcp",
				"version": "0.1.0",
			},
		})
	case "ping":
		if isNotify {
			return nil
		}
		return writeResponse(out, req.ID, map[string]any{})
	case "tools/list":
		if isNotify {
			return nil
		}
		return writeResponse(out, req.ID, map[string]any{"tools": toolCatalog()})
	case "tools/call":
		if isNotify {
			return nil
		}
		result := s.callTool(req.Params)
		return writeResponse(out, req.ID, result)
	default:
		if isNotify {
			return nil
		}
		return writeRPCError(out, req.ID, rpcMethodNotFound, "method not found: "+req.Method)
	}
}

func toolCatalog() []toolDef {
	return []toolDef{
		{
			Name:        "artifact_list",
			Description: "List artifacts for the authenticated workspace. Maps to GET /api/artifacts?limit&cursor&end_user&project&q. Search is over titles only (case-insensitive substring) — there is no full-text search of page contents.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit":    map[string]any{"type": "integer", "description": "Page size 1..200 (API default 50)"},
					"cursor":   map[string]any{"type": "string", "description": "Opaque next_cursor from a previous list response"},
					"end_user": map[string]any{"type": "string", "description": "Filter by opaque end-user / customer id"},
					"project":  map[string]any{"type": "string", "description": "Filter by project slug/name"},
					"q":        map[string]any{"type": "string", "description": "Case-insensitive substring match on the artifact title"},
				},
			},
		},
		{
			Name:        "artifact_get",
			Description: "Get one artifact by id (includes share_code when present). Maps to GET /api/artifacts/{id}.",
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"id": map[string]any{"type": "string", "description": "Artifact id"}},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "artifact_comments",
			Description: "List comment threads on an artifact. Maps to GET /api/artifacts/{id}/comments?status=open|resolved|all&since=RFC3339.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "string", "description": "Artifact id"},
					"status": map[string]any{"type": "string", "enum": []string{"open", "resolved", "all"}, "description": "Thread status filter (API default open)"},
					"since":  map[string]any{"type": "string", "description": "RFC3339 timestamp; only threads updated after this time"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "artifact_reply",
			Description: "Reply as owner on a comment thread. Maps to POST /api/artifacts/{id}/comments/{thread}/reply with JSON {\"body\":\"...\"}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "string", "description": "Artifact id"},
					"thread": map[string]any{"type": "string", "description": "Thread id"},
					"body":   map[string]any{"type": "string", "description": "Reply text"},
				},
				"required": []string{"id", "thread", "body"},
			},
		},
		{
			Name:        "artifact_resolve",
			Description: "Resolve a comment thread. Maps to POST /api/artifacts/{id}/comments/{thread}/resolve.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "string", "description": "Artifact id"},
					"thread": map[string]any{"type": "string", "description": "Thread id"},
				},
				"required": []string{"id", "thread"},
			},
		},
	}
}

func (s *server) callTool(params json.RawMessage) callResult {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errResult("invalid tools/call params: " + err.Error())
	}
	args := map[string]any{}
	if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return errResult("invalid arguments: " + err.Error())
		}
	}
	ctx := context.Background()
	var (
		raw json.RawMessage
		err error
	)
	switch p.Name {
	case "artifact_list":
		raw, err = s.client.listArtifacts(ctx, listParams{
			Limit:   intArg(args, "limit"),
			Cursor:  strArg(args, "cursor"),
			EndUser: strArg(args, "end_user"),
			Project: strArg(args, "project"),
			Query:   strArg(args, "q"),
		})
	case "artifact_get":
		id := strArg(args, "id")
		if id == "" {
			return errResult("id is required")
		}
		raw, err = s.client.getArtifact(ctx, id)
	case "artifact_comments":
		id := strArg(args, "id")
		if id == "" {
			return errResult("id is required")
		}
		status := strArg(args, "status")
		if status != "" && status != "open" && status != "resolved" && status != "all" {
			return errResult("status must be open, resolved, or all")
		}
		raw, err = s.client.listComments(ctx, id, commentsParams{
			Status: status,
			Since:  strArg(args, "since"),
		})
	case "artifact_reply":
		id, thread, body := strArg(args, "id"), strArg(args, "thread"), strArg(args, "body")
		if id == "" || thread == "" {
			return errResult("id and thread are required")
		}
		if strings.TrimSpace(body) == "" {
			return errResult("body is required")
		}
		raw, err = s.client.reply(ctx, id, thread, body)
	case "artifact_resolve":
		id, thread := strArg(args, "id"), strArg(args, "thread")
		if id == "" || thread == "" {
			return errResult("id and thread are required")
		}
		raw, err = s.client.resolve(ctx, id, thread)
	default:
		return errResult("unknown tool: " + p.Name)
	}
	if err != nil {
		return errResult(err.Error())
	}
	return callResult{Content: []textContent{{Type: "text", Text: string(raw)}}}
}

func errResult(msg string) callResult {
	return callResult{
		Content: []textContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

func strArg(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// JSON numbers sometimes arrive without quotes from loose clients.
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func intArg(args map[string]any, key string) int {
	v, ok := args[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	case string:
		var n int
		fmt.Sscanf(t, "%d", &n)
		return n
	default:
		return 0
	}
}
