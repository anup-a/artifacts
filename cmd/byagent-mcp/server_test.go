package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAPI answers the three reads brief_get makes. The brief is the artifact
// tagged brief in the collection; a same-named page without the tag and a
// brief of another collection must both be passed over.
func fakeAPI(t *testing.T, withSource bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/artifacts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") != "puzzled" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("list query %q auth %q", r.URL.RawQuery, r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{"artifacts": []any{
			map[string]any{"id": "ar_page", "project": "puzzled", "tags": []string{"notes"}},
			map[string]any{"id": "ar_other", "project": "Puzzled", "tags": []string{"brief"}},
			map[string]any{"id": "ar_brief", "url": "https://x/a/ar_brief/", "project": "puzzled", "tags": []string{"brief"}, "current_version": 3, "open_comments": 1},
		}})
	})
	mux.HandleFunc("GET /api/artifacts/ar_brief/files/brief.md", func(w http.ResponseWriter, r *http.Request) {
		if !withSource {
			http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, 404)
			return
		}
		w.Write([]byte("# GA4 fix\n"))
	})
	mux.HandleFunc("GET /api/artifacts/ar_brief/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"threads": []any{map[string]any{"id": "th_1"}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func callBrief(t *testing.T, api string, args string) (callResult, map[string]any) {
	t.Helper()
	s := newServer(newClient(config{API: api, Token: "tok"}))
	res := s.callTool(json.RawMessage(`{"name":"brief_get","arguments":` + args + `}`))
	var out map[string]any
	if !res.IsError {
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			t.Fatal(err)
		}
	}
	return res, out
}

func TestBriefGetReadsSourceAndOpenComments(t *testing.T) {
	_, out := callBrief(t, fakeAPI(t, true).URL, `{"collection":"puzzled"}`)
	art := out["artifact"].(map[string]any)
	if art["id"] != "ar_brief" || art["version"].(float64) != 3 || out["content"] != "# GA4 fix\n" {
		t.Fatalf("%v", out)
	}
	if th := out["threads"].([]any); len(th) != 1 {
		t.Fatalf("threads %v", th)
	}
}

func TestBriefGetWithoutSourceOrBrief(t *testing.T) {
	_, out := callBrief(t, fakeAPI(t, false).URL, `{"collection":"puzzled"}`)
	if out["artifact"] == nil || out["content"] != "" {
		t.Fatalf("an older brief without its source still resolves: %v", out)
	}
	res, _ := callBrief(t, fakeAPI(t, true).URL, `{"collection":" "}`)
	if !res.IsError {
		t.Fatal("blank collection must be refused")
	}
}

// Clients speak newline-delimited JSON (the MCP stdio transport); LSP-style
// Content-Length framing still works. Each reply matches its request.
func TestRunAnswersBothFramings(t *testing.T) {
	s := newServer(newClient(config{API: "http://unused", Token: "tok"}))
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`

	var out bytes.Buffer
	if err := s.run(strings.NewReader(init+"\n"+`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n\n"+list+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], `"brief_get"`) {
		t.Fatalf("line replies: %q", out.String())
	}

	out.Reset()
	framed := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(list), list)
	if err := s.run(strings.NewReader(framed), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "Content-Length: ") || !strings.Contains(out.String(), `"brief_get"`) {
		t.Fatalf("framed reply: %q", out.String())
	}
}
