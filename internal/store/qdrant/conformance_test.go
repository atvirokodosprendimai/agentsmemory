package qdrant

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest"
)

// fakeQdrant is a minimal Qdrant that stores what it is sent and returns it,
// faithful to the two request shapes this client uses: PUT .../points to write
// and POST .../points with an id list to read back.
//
// It is deliberately not a stub that returns a canned answer. What can be wrong
// in an HTTP driver is the mapping — the path, the body shape, the reserved id
// key, the direction of the payload — and a canned response tests none of that.
// This one round-trips through the real serialization in both directions.
func fakeQdrant(t *testing.T) *httptest.Server {
	t.Helper()
	type point struct {
		vector  []float32
		payload map[string]any
	}
	stored := map[string]map[string]*point{} // collection -> point uuid -> what was written

	collection := func(path string) string { return strings.SplitN(path, "?", 2)[0] }
	collectionName := func(path string) string {
		return strings.SplitN(strings.TrimPrefix(path, "/collections/"), "/", 2)[0]
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := collection(r.URL.Path)
		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(path, "/points"):
			var body struct {
				Points []struct {
					ID      string         `json:"id"`
					Vector  []float32      `json:"vector"`
					Payload map[string]any `json:"payload"`
				} `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			coll := stored[collectionName(path)]
			if coll == nil {
				coll = map[string]*point{}
				stored[collectionName(path)] = coll
			}
			for _, p := range body.Points {
				coll[p.ID] = &point{vector: p.Vector, payload: p.Payload}
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/points/payload"):
			// Qdrant MERGES a set-payload rather than replacing, and so does this.
			// A fake that replaced would let a driver that replaces look correct.
			var body struct {
				Payload map[string]any `json:"payload"`
				Points  []string       `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			coll := stored[collectionName(path)]
			for _, id := range body.Points {
				p, ok := coll[id]
				if !ok {
					continue // an id it does not hold is ignored
				}
				for k, v := range body.Payload {
					p.payload[k] = v
				}
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/points/search"):
			// Qdrant applies the payload filter DURING the search, and so does
			// this: a fake that ignored it would let a driver whose filtered
			// search is broken — or whose payload patch never reached the copy the
			// filter reads — pass every assertion about scoping.
			var body struct {
				Vector []float32 `json:"vector"`
				Limit  int       `json:"limit"`
				Filter *struct {
					Must []struct {
						Key   string `json:"key"`
						Match struct {
							Value string `json:"value"`
						} `json:"match"`
					} `json:"must"`
				} `json:"filter"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			matches := func(pl map[string]any) bool {
				if body.Filter == nil {
					return true
				}
				for _, m := range body.Filter.Must {
					if v, _ := pl[m.Key].(string); v != m.Match.Value {
						return false
					}
				}
				return true
			}
			type res struct {
				Score   float32        `json:"score"`
				Payload map[string]any `json:"payload"`
			}
			var out struct {
				Result []res `json:"result"`
			}
			for _, p := range stored[collectionName(path)] {
				if !matches(p.payload) {
					continue
				}
				out.Result = append(out.Result, res{Score: cosine(body.Vector, p.vector), Payload: p.payload})
			}
			sort.Slice(out.Result, func(a, b int) bool { return out.Result[a].Score > out.Result[b].Score })
			if body.Limit > 0 && len(out.Result) > body.Limit {
				out.Result = out.Result[:body.Limit]
			}
			_ = json.NewEncoder(w).Encode(out)

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/points/count"):
			// count_points: exact for the unfiltered shape this client uses; a
			// collection that was never created counts zero, like the real server.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"count": len(stored[collectionName(path)])},
			})

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/points"):
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			type res struct {
				ID      string         `json:"id"`
				Payload map[string]any `json:"payload"`
			}
			var out struct {
				Result []res `json:"result"`
			}
			coll := stored[collectionName(path)]
			for _, id := range body.IDs {
				if p, ok := coll[id]; ok { // an id it does not hold is simply absent
					out.Result = append(out.Result, res{ID: id, Payload: p.payload})
				}
			}
			_ = json.NewEncoder(w).Encode(out)

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/points/delete"):
			// Delete by point id list; ids the store does not hold are ignored.
			var body struct {
				Points []string `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			coll := stored[collectionName(path)]
			for _, id := range body.Points {
				delete(coll, id)
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))

		default: // collection creation and anything else
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	}))
}

// cosine is enough similarity for the fake to order two orthogonal vectors.
func cosine(a, b []float32) float32 {
	var dot, na, nb float64
	for i := range a {
		if i >= len(b) {
			break
		}
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

// TestQdrantRunsTheConformanceSuite drives the real client, through real HTTP,
// against a store that keeps what it is given.
func TestQdrantRunsTheConformanceSuite(t *testing.T) {
	storetest.RunPointsConformance(t, "qdrant", func(t *testing.T) store.VectorStore {
		srv := fakeQdrant(t)
		t.Cleanup(srv.Close)
		return New(srv.URL, "", 10*time.Second)
	})
}

// The same backend, the write half.
func TestQdrantRunsTheSetPayloadConformanceSuite(t *testing.T) {
	storetest.RunCountConformance(t, "qdrant", func(t *testing.T) store.VectorStore {
		srv := fakeQdrant(t)
		t.Cleanup(srv.Close)
		return New(srv.URL, "", 10*time.Second)
	})
	storetest.RunSetPayloadConformance(t, "qdrant", func(t *testing.T) store.VectorStore {
		srv := fakeQdrant(t)
		t.Cleanup(srv.Close)
		return New(srv.URL, "", 10*time.Second)
	})
}
