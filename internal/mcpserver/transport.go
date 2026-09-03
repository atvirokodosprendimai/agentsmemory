package mcpserver

import (
	"net/http"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// conformStreamHTTP wraps the Streamable HTTP handler with the two transport
// rules mcp-go leaves to the host.
//
// Both were measured absent against the shipped container on 2026-09-03, and
// neither is behaviour mcp-go can decide: whether a GET should offer a stream
// depends on whether this deployment keeps sessions, and which protocol versions
// are acceptable depends on what the host is willing to serve.
func conformStreamHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code, msg := transportRefusal(r); code != 0 {
			// ⚠ Allow BELONGS TO THE 405 AND NOTHING ELSE, AND THE REORDER MADE
			// THAT MATTER. It used to be merely redundant on a 400; once the
			// version check moved ahead of the method check, a POST carrying an
			// unsupported version answered 400 WITH a method to retry with — a
			// retry that cannot succeed, because the version is what was refused.
			// Allow is defined as the response to a method that is not allowed.
			if code == http.StatusMethodNotAllowed {
				w.Header().Set("Allow", "POST, DELETE")
			}
			http.Error(w, msg, code)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// transportRefusal returns the status and message a request should be refused
// with, or (0, "") to let it through.
//
// It is separate from the middleware so a test can drive the decision without an
// HTTP server, and so the two rules below are one place rather than two branches
// in a handler.
func transportRefusal(r *http.Request) (int, string) {
	// ⚠ THE VERSION IS JUDGED BEFORE THE METHOD, AND THE ORDER WAS WRONG FIRST.
	// The spec's 400 for an unsupported version is unconditional, so a GET carrying
	// one must answer 400 rather than 405: the client learns it is speaking a
	// dialect this server does not, which is the more useful of the two refusals
	// and the one that does not depend on which method it happened to try.
	//
	// After initialization a client must state the protocol version it settled
	// on, and a server that cannot serve it must say so rather than answering in
	// a dialect the client did not agree to. Today nothing here behaves
	// differently per version, which is exactly why the check is cheap now and
	// expensive to add later: the day one thing does, every client that never
	// sent the header has already been served the wrong answer silently.
	//
	// ⚠ THE ACCEPTED SET IS DERIVED FROM mcp.ValidProtocolVersions, NOT LISTED
	// HERE. A hardcoded list goes stale in the one direction that breaks callers:
	// a client moving to a version this library already speaks would be refused by
	// a literal nobody remembered to edit. Deriving it means an mcp-go upgrade
	// widens the check on the same commit.
	if v := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version")); v != "" {
		if !slices.Contains(mcp.ValidProtocolVersions, v) {
			return http.StatusBadRequest, "unsupported MCP-Protocol-Version " + v + "; this server speaks " + strings.Join(mcp.ValidProtocolVersions, ", ")
		}
	}

	// A GET asks to open the server-to-client SSE stream. This server is mounted
	// WithStateLess, which keeps no session to push down, and nothing here calls
	// SendNotificationToClient — so the stream is dead by construction rather
	// than merely idle. mcp-go answers 200 and holds the connection anyway:
	// measured, a single GET held 12s and delivered zero bytes, and 25 concurrent
	// GETs were held open. The transport's own answer for a server that offers no
	// stream is 405, which also stops a client retrying forever.
	if r.Method == http.MethodGet {
		return http.StatusMethodNotAllowed, "this server keeps no session, so it offers no server-to-client stream: use POST"
	}

	return 0, ""
}
