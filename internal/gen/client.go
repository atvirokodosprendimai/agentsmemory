// Package gen is this repository's one client for a generative model endpoint.
//
// It exists because the same request/response path had been written twice —
// `questionGen` in cmd/server/eval.go and `tripleGen` in cmd/server/kgextract.go
// — and ADR-047 needed a third caller for its reader and judge. A third copy is
// the point at which copies stop being an accident, so the transport moved here
// and both existing callers were repointed in the same commit.
//
// What moved is the TRANSPORT only. Each caller keeps its own prompt, its own
// temperature and its own parsing, because those are where the two callers
// genuinely differ: kgextract parses several lines out of the raw body while the
// question generator keeps a cleaned first line, and unifying that would have
// silently changed what kgextract extracts.
package gen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client talks to an Ollama-compatible endpoint.
//
// ⚠It speaks Ollama's own POST /api/generate and NOTHING ELSE. An
// OpenAI/Anthropic-compatible endpoint expects /v1/chat/completions with a
// different request and a different reply, and this client does not implement
// it — deliberately, as the code it was extracted from also did.
//
// That is worth stating because ADR-047 and its T3 task both claimed for a while
// that an "OpenAI-compatible /v1 branch" existed here. It never did: the /v1
// test is consulted only by Hint, to word a failure message. The claim was
// written from a method name rather than from its call sites, and had it been
// implemented as described it would have added a request path nobody decided on
// under the description of a behaviour-preserving move.
type Client struct {
	URL    string
	Model  string
	APIKey string // sent as Authorization: Bearer when set; hosted providers need it

	HTTP    *http.Client
	Verbose io.Writer
}

// OpenAIShaped reports whether the endpoint looks like an OpenAI-compatible one.
//
// The /v1 convention is what every hosted provider and every local shim agrees
// on, so it is the honest discriminator. ⚠It governs the wording of Hint and
// nothing else: Generate does not branch on it. See the type's comment.
func (c *Client) OpenAIShaped() bool { return strings.Contains(c.URL, "/v1") }

// Result is one completion together with what the endpoint reported about it.
//
// PromptTokens is the endpoint's OWN count of the prompt it received, or 0 when
// it reports none. It is the only token figure this repository can obtain, since
// there is no tokenizer here — and it is what makes ADR-047's rune budget
// AUDITABLE rather than assumed: a run records the realised token spread across
// its cells instead of trusting that equal rune counts carried equal tokens.
//
// Ollama returns it as prompt_eval_count on /api/generate. An endpoint that
// omits it leaves this 0, which the results file reports as "not supplied"
// rather than as zero tokens.
type Result struct {
	Text         string
	PromptTokens int
}

// Generate asks the model for one completion and returns its RAW response.
//
// Raw because parsing belongs to the caller: kgextract reads several lines out
// of this and the question generator keeps a cleaned first line, so a transport
// that trimmed the answer would change one of them without saying so.
//
// temperature is the caller's, not a default here, for the same reason —
// extraction runs at 0.1 because creativity there is fabrication, and question
// generation at 0.2 because a re-run should reproduce its questions.
func (c *Client) Generate(ctx context.Context, prompt string, temperature float64) (Result, error) {
	body, err := json.Marshal(map[string]any{
		"model":   c.Model,
		"prompt":  prompt,
		"stream":  false,
		"options": map[string]any{"temperature": temperature},
	})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.URL, "/")+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("generate: %d: %s", resp.StatusCode, FirstLine(string(raw), 120))
	}
	var out struct {
		Response string `json:"response"`
		// prompt_eval_count is Ollama's count of the prompt it actually tokenized.
		PromptEvalCount int `json:"prompt_eval_count"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Result{}, err
	}
	return Result{Text: out.Response, PromptTokens: out.PromptEvalCount}, nil
}

// Hint turns a generator failure into something actionable: which models the
// endpoint actually serves, when it can be asked.
//
// The /api/tags probe is an Ollama-only affordance, so an OpenAI-shaped endpoint
// gets the variable names instead — asking a hosted provider for /api/tags turns
// a wrong-model error into a confusing 404.
func (c *Client) Hint(ctx context.Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  endpoint: %s\n  model:    %s\n", c.URL, c.Model)
	if c.OpenAIShaped() {
		b.WriteString("  Set EVAL_GEN_MODEL to a model this endpoint serves, and EVAL_GEN_API_KEY if it needs a key.\n")
		return b.String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(c.URL, "/")+"/api/tags", nil)
	if err != nil {
		return b.String()
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return b.String()
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return b.String()
	}
	if len(tags.Models) > 0 {
		names := make([]string, 0, len(tags.Models))
		for _, m := range tags.Models {
			names = append(names, m.Name)
		}
		fmt.Fprintf(&b, "  this endpoint serves: %s\n", strings.Join(names, ", "))
	}
	b.WriteString("  Set EVAL_GEN_MODEL to one of those, pull the one you want (ollama pull <model>),\n")
	b.WriteString("  or point EVAL_GEN_URL at another endpoint.\n")
	return b.String()
}

// FirstLine returns s's first line, truncated to maxLen runes.
//
// It is exported because the error text it shapes is shared with the callers
// that were repointed here, and that message has saved a real debugging session:
// an embedder asked to answer /api/generate returns a body whose first line says
// exactly that.
func FirstLine(s string, maxLen int) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > maxLen {
		return string(r[:maxLen])
	}
	return s
}
