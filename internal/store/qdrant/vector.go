package qdrant

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/google/uuid"
)

// Client is the Qdrant search index in the source-of-truth + index split: the
// durable copy lives in SQLite (see internal/store/sqlitevec) and this is the
// rebuildable query layer. These methods make *Client satisfy store.VectorStore
// so store.Hybrid can drive it as the index; the bootstrap helpers (CollectionName,
// EnsureCollection, do) live in qdrant.go.
var _ store.VectorStore = (*Client)(nil)
var _ store.ApproximateCounter = (*Client)(nil)

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
		if _, taken := p.Payload[payloadIDKey]; taken {
			// The reserved key is how a point maps back to the caller's id. A
			// payload carrying it would be silently overwritten here and silently
			// stripped on read — the seam promises the payload round-trips
			// verbatim, and this is the one key for which that cannot be true.
			return fmt.Errorf("qdrant: payload key %q is reserved by this driver and cannot be stored", payloadIDKey)
		}
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
func (c *Client) Search(ctx context.Context, namespace string, vector []float32, k int, filter store.Filter) (store.SearchResult, error) {
	if k <= 0 {
		return store.SearchResult{}, nil
	}
	body := map[string]any{"vector": vector, "limit": k, "with_payload": true}
	if f := matchFilter(filter); f != nil {
		// Qdrant applies this during the search, so `limit` counts MATCHING
		// points. Without it the caller would have to over-fetch a pool wide
		// enough that the survivors still fill a page — a cost that grows with the
		// collection and is paid on every scoped query.
		body["filter"] = f
	}
	var resp struct {
		Result []struct {
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	path := "/collections/" + CollectionName(namespace) + "/points/search"
	if err := c.do(ctx, http.MethodPost, path, body, &resp); err != nil {
		return store.SearchResult{}, err
	}
	hits := make([]store.Hit, 0, len(resp.Result))
	for _, r := range resp.Result {
		id, _ := r.Payload[payloadIDKey].(string)
		delete(r.Payload, payloadIDKey) // the reserved key is internal; hide it
		hits = append(hits, store.Hit{ID: id, Score: r.Score, Payload: r.Payload})
	}
	return store.SearchResult{H: hits}, nil
}

// Count returns how many points the namespace's collection holds, exact for the
// unfiltered shape this client uses (count_points is exact without a filter;
// exact:true is the accuracy/cost lever under one). The coverage check compares
// counts, so an approximate answer would feed a false trigger or mask a real
// deficit.
func (c *Client) Count(ctx context.Context, namespace string) (int, error) {
	var resp struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}
	path := "/collections/" + CollectionName(namespace) + "/points/count"
	if err := c.do(ctx, http.MethodPost, path, map[string]any{"exact": true}, &resp); err != nil {
		return 0, err
	}
	return resp.Result.Count, nil
}

// ApproximateCount reports the namespace's population without the exact:true
// lever — the shape the serving gate reads above its ExactCountCap. The value
// may lag the durable count (that is the price of the cheap read), so the gate
// corroborates a sampled read against the index-ingested watermark before it
// lets it trigger a rebuild.
func (c *Client) ApproximateCount(ctx context.Context, namespace string) (int, error) {
	var resp struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}
	path := "/collections/" + CollectionName(namespace) + "/points/count"
	if err := c.do(ctx, http.MethodPost, path, map[string]any{"exact": false}, &resp); err != nil {
		return 0, err
	}
	return resp.Result.Count, nil
}

// matchFilter renders a payload filter as Qdrant's must-match clause, or nil when
// there is nothing to filter on. Keys are sorted so the request body is stable —
// which makes it comparable in tests and readable in a proxy log.
func matchFilter(filter store.Filter) map[string]any {
	if len(filter) == 0 {
		return nil
	}
	keys := make([]string, 0, len(filter))
	for k := range filter {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	must := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		must = append(must, map[string]any{"key": k, "match": map[string]any{"value": filter[k]}})
	}
	return map[string]any{"must": must}
}

// PointsByIDs retrieves points by their derived UUIDs and maps them back onto the
// caller's own IDs, exactly as Search does.
//
// It does not ask for vectors: this is the search index, it stores them only to
// search with, and a payload audit has no use for a thousand floats per point.
func (c *Client) PointsByIDs(ctx context.Context, namespace string, ids []string) ([]store.Point, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	uuids := make([]string, len(ids))
	byUUID := make(map[string]string, len(ids))
	for i, id := range ids {
		u := pointID(namespace, id)
		uuids[i] = u
		byUUID[u] = id
	}
	body := map[string]any{"ids": uuids, "with_payload": true, "with_vector": false}
	var resp struct {
		Result []struct {
			ID      string         `json:"id"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	path := "/collections/" + CollectionName(namespace) + "/points"
	if err := c.do(ctx, http.MethodPost, path, body, &resp); err != nil {
		return nil, err
	}
	// Qdrant omits ids it does not hold, which is the contract this method
	// promises, so there is nothing to filter here.
	out := make([]store.Point, 0, len(resp.Result))
	for _, r := range resp.Result {
		// Resolve the caller's id from the POINT ID we asked for, not from the
		// payload. The payload copy is mutable — a stray SetPayload could rewrite
		// it — and trusting it would let a request for one id answer with another.
		id, known := byUUID[r.ID]
		if !known {
			// A point nobody asked for. Returning it would let a driver bug
			// manufacture a drawer the caller never named.
			continue
		}
		delete(r.Payload, payloadIDKey)
		out = append(out, store.Point{ID: id, Payload: r.Payload})
	}
	return out, nil
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
