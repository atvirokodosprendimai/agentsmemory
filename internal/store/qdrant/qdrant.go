// Package qdrant is a thin REST client for the Qdrant vector store. It is the
// only vector backend agentsmemory ships (Ollama + Qdrant from day one — no
// Chroma, no local ONNX), so there is no pluggable backend registry like the
// Python tool had; just this concrete client behind a small interface defined
// at its consumers.
//
// Tenancy is physical: each team gets its own collection (decided 2026-06-26),
// named deterministically from the team id. That keeps one team's vectors in a
// separate Qdrant collection from every other team's — a missing query filter
// can never leak across the boundary because the data is not even colocated.
package qdrant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CollectionName returns the deterministic Qdrant collection name for a team.
//
// Format: "mempalace_<sha256(teamID)[:16]>_drawers". The 16-hex-char team hash
// mirrors the Python palace-hash scheme (sha256(palace.id)[:16]) so the naming
// is familiar, opaque, and URL-safe. It is a pure function so it can be unit
// tested and called anywhere without a client.
func CollectionName(teamID string) string {
	sum := sha256.Sum256([]byte(teamID))
	return "mempalace_" + hex.EncodeToString(sum[:])[:16] + "_drawers"
}

// Client is a minimal Qdrant REST client built on net/http (no SDK, matching
// the Python urllib client). It is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New constructs a Client for the given Qdrant base URL. apiKey may be empty for
// an unauthenticated local Qdrant.
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// EnsureCollection creates the team's collection if it does not already exist,
// configured for cosine distance at the embedder's dimension (1024 for bge-m3).
// It is idempotent: an existing collection of the right shape is left alone.
func (c *Client) EnsureCollection(ctx context.Context, teamID string, dim int) error {
	name := CollectionName(teamID)

	// A HEAD-equivalent GET tells us whether the collection already exists.
	if exists, err := c.collectionExists(ctx, name); err != nil {
		return err
	} else if exists {
		return nil
	}

	body := map[string]any{
		"vectors": map[string]any{"size": dim, "distance": "Cosine"},
	}
	return c.do(ctx, http.MethodPut, "/collections/"+name, body, nil)
}

// collectionExists reports whether a collection is present, treating 404 as a
// clean "no" rather than an error.
func (c *Client) collectionExists(ctx context.Context, name string) (bool, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/collections/"+name, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
	switch {
	case resp.StatusCode == http.StatusOK:
		return true, nil
	case resp.StatusCode == http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("qdrant: unexpected status %d checking collection", resp.StatusCode)
	}
}

// newRequest builds a JSON request with the optional api-key header set.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
	return req, nil
}

// do performs a JSON request, decoding the response into out when non-nil. A
// non-2xx status is turned into an error carrying the response body for
// diagnosis.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var rdr io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := c.newRequest(ctx, method, path, rdr)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant: %s %s -> %d: %s", method, path, resp.StatusCode, string(data))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
