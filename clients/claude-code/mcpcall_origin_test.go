package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
)

// TestMCPCallSendsTheOriginHeaderFromTheEnvironment is ADR-054 T2's header
// half: the one client every shipped hook goes through turns the hook's
// exported AGENTSMEMORY_ORIGIN into X-Agentsmemory-Origin on the request, and
// sends NO such header when the variable is unset — a header with an empty
// value would be an origin of ” claimed explicitly, indistinguishable from the
// absent case and a byte on every call. Asserted on what the server RECEIVED,
// by presence in the map, for the reason TestNoTokenMeansNoAuthorizationHeader
// records: Header.Get cannot tell absent from present-and-empty.
func TestMCPCallSendsTheOriginHeaderFromTheEnvironment(t *testing.T) {
	var hdr http.Header
	var seen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr, seen = r.Header.Clone(), true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2024-11-05",` +
			`"capabilities":{},"serverInfo":{"name":"t","version":"0"}}}`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Setenv(mcpprotocol.OriginEnvVar, "")
	if c, err := dialMCP(ctx, srv.URL, "", 5*time.Second); err == nil {
		defer c.Close()
	}
	if !seen {
		t.Fatal("the server was never reached, so this test asserts nothing about the header")
	}
	if _, present := hdr[http.CanonicalHeaderKey(mcpprotocol.OriginHeader)]; present {
		t.Errorf("with %s unset the request still carried %s", mcpprotocol.OriginEnvVar, mcpprotocol.OriginHeader)
	}

	seen = false
	t.Setenv(mcpprotocol.OriginEnvVar, "hook:agentsmemory-recall-hook.sh")
	if c, err := dialMCP(ctx, srv.URL, "", 5*time.Second); err == nil {
		defer c.Close()
	}
	if !seen {
		t.Fatal("the server was never reached on the second dial")
	}
	if got := hdr.Get(mcpprotocol.OriginHeader); got != "hook:agentsmemory-recall-hook.sh" {
		t.Errorf("%s did not reach the server verbatim: %s=%q", mcpprotocol.OriginEnvVar, mcpprotocol.OriginHeader, got)
	}
}
