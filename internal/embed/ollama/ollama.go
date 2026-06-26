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
)

// Embedder is a client for Ollama's /api/embed endpoint.
type Embedder struct {
	endpoint string
	model    string
	http     *http.Client
}

// New constructs an Embedder for the given Ollama base URL and model.
func New(baseURL, model string, timeout time.Duration) *Embedder {
	return &Embedder{
		endpoint: strings.TrimRight(baseURL, "/") + "/api/embed",
		model:    model,
		http:     &http.Client{Timeout: timeout},
	}
}

// embedRequest is Ollama's batch embed payload: one model, many input strings.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
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
	raw, err := json.Marshal(embedRequest{Model: e.model, Input: inputs})
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
