package qdrant

import (
	"context"
	"fmt"
	"net/http"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/google/uuid"
)

// Client is the Qdrant search index in the source-of-truth + index split: the
// durable copy lives in SQLite (see internal/store/sqlitevec) and this is the
// rebuildable query layer. These methods make *Client satisfy store.VectorStore
// so store.Hybrid can drive it as the index; the bootstrap helpers (CollectionName,
// EnsureCollection, do) live in qdrant.go.
var _ store.VectorStore = (*Client)(nil)

// payloadIDKey holds a point's original string ID inside its Qdrant payload.
// Qdrant point IDs must be unsigned ints or UUIDs, so we key points by a derived
// UUID and stash the caller's real ID here to return it on Search.
const payloadIDKey = "_id"

// pointID derives a deterministic UUID from the caller's (namespace, id). uuid5
// is stable, so re-upserting the same logical point overwrites rather than
// duplicates — the idempotency the seam promises.
func pointID(namespace, id string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(namespace+"\x00"+id)).String()
}

// EnsureNamespace maps the backend-agnostic namespace onto a Qdrant collection.
func (c *Client) EnsureNamespace(ctx context.Context, namespace string, dim int) error {
	return c.EnsureCollection(ctx, namespace, dim)
}

// upsertBatch bounds how many points go in one Qdrant upsert request. A bulk
// replay (sync / Rebuild) can hand Upsert tens of thousands of points at once;
// sending them in a single PUT builds a body of hundreds of MB that Qdrant times
// out or rejects, so the whole namespace silently fails to land. Chunking keeps
// every request small and fast regardless of how many points the caller passes.
const upsertBatch = 256

// Upsert writes points to the namespace's collection, chunked into batches of
// upsertBatch and waiting for each to be applied (wait=true) so a following Search
// sees them. Chunking is transparent to callers — pass any number of points.
func (c *Client) Upsert(ctx context.Context, namespace string, points []store.Point) error {
	for start := 0; start < len(points); start += upsertBatch {
		end := start + upsertBatch
		if end > len(points) {
			end = len(points)
		}
		if err := c.upsertChunk(ctx, namespace, points[start:end]); err != nil {
			return fmt.Errorf("qdrant upsert points [%d:%d] of %d: %w", start, end, len(points), err)
		}
	}
	return nil
}

// upsertChunk PUTs one bounded batch of points to the namespace's collection.
func (c *Client) upsertChunk(ctx context.Context, namespace string, points []store.Point) error {
	type qpoint struct {
		ID      string         `json:"id"`
		Vector  []float32      `json:"vector"`
		Payload map[string]any `json:"payload"`
	}
	body := struct {
		Points []qpoint `json:"points"`
	}{Points: make([]qpoint, 0, len(points))}
	for _, p := range points {
		// Copy the caller's payload and add the reserved id key; never mutate
		// the caller's map.
		payload := make(map[string]any, len(p.Payload)+1)
		for k, v := range p.Payload {
			payload[k] = v
		}
		payload[payloadIDKey] = p.ID
		body.Points = append(body.Points, qpoint{
			ID:      pointID(namespace, p.ID),
			Vector:  p.Vector,
			Payload: payload,
		})
	}
	path := "/collections/" + CollectionName(namespace) + "/points?wait=true"
	return c.do(ctx, http.MethodPut, path, body, nil)
}

// Search runs an approximate nearest-neighbour query against the namespace's
// collection and maps Qdrant's results back onto store.Hit, restoring each
// caller-facing ID from the payload.
func (c *Client) Search(ctx context.Context, namespace string, vector []float32, k int) ([]store.Hit, error) {
	if k <= 0 {
		return nil, nil
	}
	body := map[string]any{"vector": vector, "limit": k, "with_payload": true}
	var resp struct {
		Result []struct {
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	path := "/collections/" + CollectionName(namespace) + "/points/search"
	if err := c.do(ctx, http.MethodPost, path, body, &resp); err != nil {
		return nil, err
	}
	hits := make([]store.Hit, 0, len(resp.Result))
	for _, r := range resp.Result {
		id, _ := r.Payload[payloadIDKey].(string)
		delete(r.Payload, payloadIDKey) // the reserved key is internal; hide it
		hits = append(hits, store.Hit{ID: id, Score: r.Score, Payload: r.Payload})
	}
	return hits, nil
}

// Delete removes points by their derived UUIDs, waiting for the deletion to
// apply so search results are immediately consistent.
func (c *Client) Delete(ctx context.Context, namespace string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	pts := make([]string, len(ids))
	for i, id := range ids {
		pts[i] = pointID(namespace, id)
	}
	body := map[string]any{"points": pts}
	path := "/collections/" + CollectionName(namespace) + "/points/delete?wait=true"
	return c.do(ctx, http.MethodPost, path, body, nil)
}
