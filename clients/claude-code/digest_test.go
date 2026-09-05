package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTheDigestIsSelectedByTheFlag drives the real CLI against a fake MCP
// server: with --digest the output is the plain-text digest, without it the
// JSON page. This is the rung the renderer's own test cannot see — a
// renderer nothing selects prints nothing.
func TestTheDigestIsSelectedByTheFlag(t *testing.T) {
	page := `{"count":1,"hits":[{"identity":"THE ONE MEMORY","wing":"wing_alpha","room":"decisions","regions":[{"start":40,"text":"the body of the memory"}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			w.Write([]byte(`{"jsonrpc":"2.0","id":` + idOf(req.ID) + `,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"t","version":"0"}}}`))
		case "tools/list":
			w.Write([]byte(`{"jsonrpc":"2.0","id":` + idOf(req.ID) + `,"result":{"tools":[{"name":"am_search","description":"recall","inputSchema":{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"number"},"wing":{"type":"string"}},"required":["query"]},"annotations":{"readOnlyHint":true}}]}}`))
		case "tools/call":
			esc, _ := json.Marshal(page)
			w.Write([]byte(`{"jsonrpc":"2.0","id":` + idOf(req.ID) + `,"result":{"content":[{"type":"text","text":` + string(esc) + `}]}}`))
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	run := func(args ...string) string {
		var buf bytes.Buffer
		root := rootCommand()
		root.Writer = &buf
		full := append([]string{"aiagentmemory", "mcp", "--mcp-url", srv.URL, "--token", "t"}, args...)
		if err := root.Run(context.Background(), full); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, buf.String())
		}
		return buf.String()
	}
	plain := run("search", "the one")
	if !strings.Contains(plain, `"identity"`) {
		t.Fatalf("without --digest the JSON page must print as before:\n%s", plain)
	}
	digest := run("search", "the one", "--digest", "1600")
	if strings.Contains(digest, `"identity"`) || !strings.Contains(digest, "THE ONE MEMORY") || !strings.Contains(digest, "wing_alpha/decisions") {
		t.Fatalf("with --digest the output must be the rendered digest, not JSON:\n%s", digest)
	}
}

func readAll(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

func idOf(v any) string {
	b, _ := json.Marshal(v)
	if v == nil {
		return "0"
	}
	return string(b)
}
