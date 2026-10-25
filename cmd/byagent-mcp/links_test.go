package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// link_add files the URL with the MCP client's name as the agent unless the
// caller names one; link_list passes its filters through.
func TestLinkTools(t *testing.T) {
	var got []map[string]string
	var listQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/links", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &b)
		got = append(got, b)
		w.WriteHeader(201)
		w.Write([]byte(`{"id":"ln_1","created":true}`))
	})
	mux.HandleFunc("GET /api/links", func(w http.ResponseWriter, r *http.Request) {
		listQuery = r.URL.RawQuery
		w.Write([]byte(`{"links":[]}`))
	})
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)

	s := newServer(newClient(config{API: api.URL, Token: "tok"}))
	var out strings.Builder
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"Claude Desktop"}}}` + "\n"
	if err := s.run(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if s.clientName != "claude-desktop" {
		t.Fatalf("client name %q", s.clientName)
	}

	if res := s.callTool(json.RawMessage(`{"name":"link_add","arguments":{"url":" https://a.vercel.app ","title":"Launch"}}`)); res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	if res := s.callTool(json.RawMessage(`{"name":"link_add","arguments":{"url":"https://b.netlify.app","agent":"grok"}}`)); res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	if len(got) != 2 || got[0]["url"] != "https://a.vercel.app" || got[0]["title"] != "Launch" || got[0]["agent"] != "claude-desktop" || got[1]["agent"] != "grok" {
		t.Fatalf("bodies: %v", got)
	}
	if res := s.callTool(json.RawMessage(`{"name":"link_add","arguments":{}}`)); !res.IsError {
		t.Fatal("link_add without url succeeded")
	}
	if res := s.callTool(json.RawMessage(`{"name":"link_list","arguments":{"q":"launch","project":"creatica"}}`)); res.IsError || listQuery != "project=creatica&q=launch" {
		t.Fatalf("list: %v %q", res, listQuery)
	}
}

func TestAgentSlug(t *testing.T) {
	for in, want := range map[string]string{"Claude Desktop": "claude-desktop", "codex-mcp-client": "codex-mcp-client", "  ChatGPT  ": "chatgpt", "": "", "cursor/1.2": "cursor-1.2"} {
		if got := agentSlug(in); got != want {
			t.Errorf("agentSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
