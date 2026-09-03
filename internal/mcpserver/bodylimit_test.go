package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// TestTheBodyLimitCannotShadowTheContentLimit is the property the number is
// chosen for, and the reason it is derived rather than written.
//
// Content is bounded at palace.MaxContentLength RUNES by sanitize. If the body
// limit sat below the worst-case encoding of that, a caller sending the largest
// content the server accepts would get a transport error instead of the sentence
// explaining the real rule — and would have no way to tell "too big for this
// endpoint" from "too big for a memory".
//
// A rune is up to 4 bytes in UTF-8, and a JSON string escapes further, so the
// check is deliberately generous about what the encoding might cost.
func TestTheBodyLimitCannotShadowTheContentLimit(t *testing.T) {
	worstCaseEncoded := palace.MaxContentLength * 4 // UTF-8 upper bound per rune
	if maxBodyBytes <= worstCaseEncoded {
		t.Fatalf("maxBodyBytes=%d is not above the worst-case encoding of MaxContentLength (%d runes -> %d bytes); the transport would refuse a payload sanitize would have accepted",
			maxBodyBytes, palace.MaxContentLength, worstCaseEncoded)
	}

	// And it must still be a limit: a body limit large enough to be no limit is
	// the shape that passes this file's first assertion and buys nothing.
	if maxBodyBytes > 64*worstCaseEncoded {
		t.Errorf("maxBodyBytes=%d is more than 64x the worst case; that is not a bound, it is a formality", maxBodyBytes)
	}
}

// TestAnOversizedBodyIsRefusedByTheEnvelope drives the real handler, because a
// constant is not a limit until something applies it.
func TestAnOversizedBodyIsRefusedByTheEnvelope(t *testing.T) {
	h := StreamHTTP(New(Deps{Version: "test"}))

	// Comfortably over the limit, and shaped like the call that motivated this:
	// one enormous string argument to a read tool.
	huge := strings.Repeat("A", maxBodyBytes+(1<<20))
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"am_search","arguments":{"query":%q}}}`, huge)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Errorf("a %d-byte body was accepted; /mcp is still unbounded", len(body))
	}
}

// TestAPayloadTheContentLimitWouldAcceptSurvivesTheTransport is the other
// direction, and the one a limit chosen by feel gets wrong.
//
// It sends a body at the size a legitimate maximum-length memory occupies and
// requires the transport to pass it through — the request may fail for any other
// reason, but not because the envelope truncated it.
func TestAPayloadTheContentLimitWouldAcceptSurvivesTheTransport(t *testing.T) {
	h := StreamHTTP(New(Deps{Version: "test"}))

	// MaxContentLength runes of ASCII: the largest content sanitize accepts, in
	// its most compact encoding, which is what a real caller sends.
	content := strings.Repeat("a", palace.MaxContentLength)
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"am_add_drawer","arguments":{"wing":"wing_acme","room":"decisions","content":%q}}}`, content)

	if len(body) > maxBodyBytes {
		t.Fatalf("the fixture itself (%d bytes) exceeds maxBodyBytes (%d); the limit is below a legitimate maximum-length memory", len(body), maxBodyBytes)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// It will not succeed — this server has no palace wired — but it must get far
	// enough to be answered as JSON-RPC rather than refused as a too-large body.
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Errorf("a maximum-length memory was refused as too large; the body limit shadows the content limit")
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Errorf("the response is not JSON-RPC (%q); the body was cut before the server could parse it: %v",
			rec.Body.String()[:min(120, rec.Body.Len())], err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
