package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// initializeResult drives ONE real initialize through the same transport
// production uses and returns the parsed result object.
//
// Through the transport, not off the constant: `instructions` is a field the
// server has to be CONSTRUCTED to send, and a test that asserts on the string
// literal passes just as happily when nobody passed it to NewMCPServer. That is
// this repository's signature defect — the component exercised instead of the
// selection — and it is exactly the shape that left the field empty for every
// client that has ever connected.
func initializeResult(t *testing.T) map[string]any {
	t.Helper()
	return initializeResultWith(t, Deps{})
}

// initializeResultWith is initializeResult over a caller-supplied Deps, so a test
// can pin a field that the handshake is supposed to REPORT rather than one it
// merely holds — see TestServerInfoCarriesTheBuildVersion.
func initializeResultWith(t *testing.T, deps Deps) map[string]any {
	t.Helper()
	// A real server with nil deps: registration only builds tools and closures,
	// and initialize touches none of them.
	srv := httptest.NewServer(StreamHTTP(New(deps)))
	t.Cleanup(srv.Close)

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	// The streamable transport may answer as JSON or as one SSE data: line.
	payload := string(raw)
	if i := strings.Index(payload, "data: "); i >= 0 {
		payload = payload[i+len("data: "):]
		if j := strings.IndexByte(payload, '\n'); j >= 0 {
			payload = payload[:j]
		}
	}
	var envelope struct {
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		t.Fatalf("initialize response did not parse: %v\n%s", err, raw)
	}
	if envelope.Error != nil {
		t.Fatalf("initialize returned an error: %v", envelope.Error)
	}
	if envelope.Result == nil {
		t.Fatalf("initialize returned no result:\n%s", raw)
	}
	return envelope.Result
}

// instructionsFromHandshake is the field under test, read from a live handshake.
func instructionsFromHandshake(t *testing.T) string {
	t.Helper()
	result := initializeResult(t)
	text, _ := result["instructions"].(string)
	return text
}

// TestHandshakeCarriesInstructions pins the one thing ADR-021 T1 delivers.
//
// A client with nowhere to put a protocol file — Claude Desktop is the case that
// opened this ADR — has exactly one channel through which the server can tell it
// anything, and this is it. Before this task the field was empty on every
// connection, so every such client reasoned about wing scoping from the tool
// schema alone. One of them concluded, confidently and wrongly, that a wing-less
// search "will come back with nothing".
func TestHandshakeCarriesInstructions(t *testing.T) {
	result := initializeResult(t)
	if _, ok := result["instructions"]; !ok {
		t.Fatalf("the initialize result has no `instructions` field at all — the server was "+
			"constructed without server.WithInstructions, so every client is told nothing. "+
			"Keys present: %v", keysOf(result))
	}
	text, _ := result["instructions"].(string)
	if strings.TrimSpace(text) == "" {
		t.Fatal("`instructions` is present but empty, which reaches a client identically to absent")
	}
	// Guard against the assertions below passing against a stub.
	if !strings.Contains(text, "am_search") {
		t.Errorf("the instructions never name the recall call, so they cannot be what makes a "+
			"client recall:\n%s", text)
	}
}

// TestInstructionsNameTheWingRule pins the specific sentence this ADR exists for.
//
// The failure was not "a client did not know about memory" — it knew, and read the
// palace correctly. It was that it invented a SCOPING rule and proposed
// `wing: "*"` on every recall, which searches every wing and retrieves measurably
// worse: unrelated projects do not remove the answer, they add competitors ahead
// of it. The instructions have to say the opposite in so many words.
func TestInstructionsNameTheWingRule(t *testing.T) {
	text := instructionsFromHandshake(t)
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "wing") {
		t.Fatalf("the instructions never mention wings, which is the thing a client got wrong "+
			"unaided:\n%s", text)
	}
	// This assertion used to demand the string "no wing", and that is how a FALSE
	// rule shipped: omitting the wing is only scoped when the registration carries
	// a wing header, and most do not. The honest instruction is that a client must
	// establish its own scope, so the test now pins the mechanism for doing that
	// rather than a slogan.
	if !strings.Contains(text, "am_status") {
		t.Errorf("the instructions do not tell a client how to find out its own scope. Omitting "+
			"the wing is scoped ONLY when the registration carries one; asserting it "+
			"unconditionally is what shipped a wrong rule:\n%s", text)
	}
	if !strings.Contains(lower, "default_wing") {
		t.Errorf("the instructions never name default_wing, which is the field that decides "+
			"whether an omitted wing is scoped or workspace-wide:\n%s", text)
	}
	if !strings.Contains(lower, "every wing") {
		t.Errorf("the instructions do not say what an omitted wing does when the registration "+
			"has none — it searches EVERY wing, and a client that is not told assumes "+
			"otherwise:\n%s", text)
	}
	// And the trap must be named, not merely avoided: "*" is a real argument a
	// client will reach for precisely because it looks safe.
	if !strings.Contains(text, `"*"`) {
		t.Errorf("the instructions never mention the \"*\" scope, so a client that reads them can "+
			"still conclude that searching everything is the cautious default:\n%s", text)
	}
}

// TestInstructionsStayShort pins the budget, asserted rather than intended.
//
// This text lands in EVERY client's context on EVERY session, forever. ADR-017
// already measured what long does not buy: the entire bootstrap protocol,
// delivered first and verbatim to a subagent, produced 0 recalls in 5 dispatches,
// while one short paragraph produced 5. The ceiling is deliberately tight enough
// that a future edit which starts restating the protocol fails rather than
// merely bloats.
func TestInstructionsStayShort(t *testing.T) {
	text := instructionsFromHandshake(t)
	const ceiling = 1200
	if n := len(text); n > ceiling {
		t.Errorf("instructions are %d chars, ceiling %d — this is context every client pays for "+
			"on every session, and length is measurably not what works. Point at am_skillset "+
			"instead of restating it", n, ceiling)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
