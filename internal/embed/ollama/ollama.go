// Package ollama embeds text via an Ollama server's REST API. Embeddings are
// the entry point to the whole memory system — both mining (store) and search
// (query) turn text into vectors here — so it is its own small package behind
// an interface the callers define. Day-one model is bge-m3 (1024 dimensions),
// matching the frozen Python palace so vectors remain comparable.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"
)

// Embedder is a client for Ollama's /api/embed endpoint.
type Embedder struct {
	endpoint  string
	model     string
	numThread int
	http      *http.Client
}

// New constructs an Embedder for the given Ollama base URL and model.
//
// numThread, when positive, is sent as options.num_thread on every request and
// exists because a cgroup quota alone makes embedding SLOWER, not smaller.
// llama.cpp sizes its thread pool from the HOST's core count and knows nothing
// about the container's quota, so a capped Ollama runs permanently throttled —
// and lowering the cap widens the mismatch, which is the opposite of what the
// knob's name promises. Measured 2026-08-31 on a 16-core host with a quota of 12
// (issue #149): 16 threads 4.1s, 12 threads 0.054s, 1 thread 0.172s. The cliff is
// at threads > quota, not at too few CPUs, and even ONE thread is ~24x faster
// than the throttled default.
//
// Zero sends no option at all, so an unconstrained Ollama keeps choosing for
// itself — that is the right default for a host install, where there is no quota
// to match and llama.cpp's own choice is already correct.
func New(baseURL, model string, timeout time.Duration, numThread int) *Embedder {
	return &Embedder{
		endpoint:  strings.TrimRight(baseURL, "/") + "/api/embed",
		model:     model,
		numThread: numThread,
		http:      telemetry.HTTPClient(timeout),
	}
}

// embedRequest is Ollama's batch embed payload: one model, many input strings.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
	// Options is omitted entirely when empty: Ollama merges what it is given
	// over the model's own defaults, and sending `"options":{}` is harmless but
	// makes a request that says nothing look like one that says something.
	Options *embedOptions `json:"options,omitempty"`
}

// embedOptions carries the llama.cpp knobs we set per request. Only num_thread
// today, and it is a pointer field on the request rather than a value so that
// "not configured" and "configured to zero" cannot both marshal to the same
// thing.
type embedOptions struct {
	NumThread int `json:"num_thread"`
}

// embedResponse carries the parallel list of embedding vectors.
type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed returns one vector per input string, in order. An empty input slice
// short-circuits to nil so callers need not special-case it.
func (e *Embedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	body := embedRequest{Model: e.model, Input: inputs}
	if e.numThread > 0 {
		body.Options = &embedOptions{NumThread: e.numThread}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama: embed -> %d: %s", resp.StatusCode, string(data))
	}

	var out embedResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	// Guard the invariant the rest of the system relies on: one vector per input.
	if len(out.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("ollama: expected %d embeddings, got %d", len(inputs), len(out.Embeddings))
	}
	return out.Embeddings, nil
}

// EmbedOne is a convenience for the common single-string case (e.g. a search
// query), returning just that one vector.
func (e *Embedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	vecs, err := e.Embed(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// DescribeEmbedder names this backend and its model for a span.
//
// The window is always 0: Ollama exposes no capability endpoint that states a
// model's input length, so this backend genuinely cannot report one and says so
// rather than repeating the 8192 that appears in comments elsewhere in this
// repository. An absent number is a fact; a guessed one would be the same
// unmeasured folklore in a new place.
func (e *Embedder) DescribeEmbedder() (backend, model string, windowTokens int) {
	return "ollama", e.model, 0
}
