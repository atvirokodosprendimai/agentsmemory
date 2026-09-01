package gen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGenClientPostsOllamaGenerate pins the wire shape the move must not change.
//
// The whole risk of this extraction is that two working commands stop working,
// and neither of them has a test that would notice a changed request body.
func TestGenClientPostsOllamaGenerate(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = io.WriteString(w, `{"response":"  a line\nand another  "}`)
	}))
	defer srv.Close()

	c := &Client{URL: srv.URL, Model: "m", APIKey: "k", HTTP: srv.Client()}
	got, err := c.Generate(context.Background(), "PROMPT", 0.2)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if gotPath != "/api/generate" {
		t.Errorf("posted to %q, want /api/generate", gotPath)
	}
	if gotAuth != "Bearer k" {
		t.Errorf("Authorization = %q, want Bearer k — hosted endpoints need the key", gotAuth)
	}
	if gotBody["model"] != "m" || gotBody["prompt"] != "PROMPT" || gotBody["stream"] != false {
		t.Errorf("request body = %v, want model/prompt/stream unchanged by the move", gotBody)
	}
	opts, _ := gotBody["options"].(map[string]any)
	if opts["temperature"] != 0.2 {
		t.Errorf("temperature = %v, want the caller's 0.2 — each caller keeps its own", opts["temperature"])
	}
	// RAW, not first-line-cleaned: kgextract parses several lines out of this, and
	// a transport that cleaned the answer would silently change what it extracts.
	if got != "  a line\nand another  " {
		t.Errorf("Generate returned %q, want the raw response — parsing belongs to the caller", got)
	}
}

// TestGenClientPostsOllamaGenerateEvenForAV1URL pins an ABSENCE.
//
// ADR-047's Primitives Audit and T3's first draft both claimed questionGen had
// an "OpenAI-compatible /v1 branch". It never did: openAIShaped() has one
// caller, hint(), and ask() always posts /api/generate. This test exists so the
// claim cannot come back as a silent behaviour change wearing the description of
// a refactor.
func TestGenClientPostsOllamaGenerateEvenForAV1URL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"response":"ok"}`)
	}))
	defer srv.Close()

	c := &Client{URL: srv.URL + "/v1", Model: "m", HTTP: srv.Client()}
	if _, err := c.Generate(context.Background(), "P", 0.1); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(gotPath, "chat/completions") {
		t.Fatalf("a /v1 URL produced %q — this client speaks Ollama's API only, and adding an "+
			"OpenAI request path is a decision nobody has taken", gotPath)
	}
	if !strings.HasSuffix(gotPath, "/api/generate") {
		t.Errorf("posted to %q, want the /api/generate path appended to the /v1 URL", gotPath)
	}
}

// TestGenClientHintNamesTheKeyForAV1URL covers the one thing OpenAIShaped
// actually governs: the wording of a failure message.
func TestGenClientHintNamesTheKeyForAV1URL(t *testing.T) {
	c := &Client{URL: "https://api.example.com/v1", Model: "m", HTTP: http.DefaultClient}
	if !c.OpenAIShaped() {
		t.Fatal("a /v1 URL must read as OpenAI-shaped — that is the discriminator hint() uses")
	}
	hint := c.Hint(context.Background())
	if !strings.Contains(hint, "EVAL_GEN_API_KEY") {
		t.Errorf("hint for a /v1 endpoint must name the key variable, got: %s", hint)
	}
	// And it must NOT try to reach /api/tags on such an endpoint: that probe is an
	// Ollama-only affordance, and calling it against a hosted provider turns a
	// bad-model error into a confusing 404.
	if strings.Contains(hint, "api/tags") {
		t.Errorf("hint probed /api/tags on an OpenAI-shaped endpoint: %s", hint)
	}
}
