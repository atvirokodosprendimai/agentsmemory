package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// errWireIndexDown is returned by a wireProbeIndex whose collection has "lost"
// its points: every write that could heal the index fails with it.
var errWireIndexDown = errors.New("wire probe index down")

// wireProbeIndex is a hybrid index half whose population the test controls: it
// accepts the seed write, then "loses" the collection (its count drops to zero
// and every write that could heal it fails) — the exact behind-index shape the
// serving gate exists for. Deterministic where a real store would race the
// async rebuild: the rebuild cannot heal a collection that rejects every write.
type wireProbeIndex struct {
	mu    sync.Mutex
	count int
	down  bool
}

func (f *wireProbeIndex) EnsureNamespace(context.Context, string, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errWireIndexDown
	}
	return nil
}

func (f *wireProbeIndex) Upsert(_ context.Context, _ string, pts []store.Point) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errWireIndexDown
	}
	f.count += len(pts)
	return nil
}

func (f *wireProbeIndex) Search(context.Context, string, []float32, int, store.Filter) (store.SearchResult, error) {
	return store.SearchResult{}, nil
}

func (f *wireProbeIndex) Count(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return 0, nil
	}
	return f.count, nil
}

func (f *wireProbeIndex) Delete(context.Context, string, []string) error { return nil }
func (f *wireProbeIndex) PointsByIDs(context.Context, string, []string) ([]store.Point, error) {
	return nil, nil
}
func (f *wireProbeIndex) SetPayload(context.Context, string, []string, map[string]string) error {
	return nil
}

// TestSearchResponseCarriesStaleIndexOnTheWire guards the LAST hop of the
// stale-index chain: the searchHitView literal in the search handler. Every
// hop behind it is pinned by a test — the store stamps the flag, survivorsFrom
// threads it, TestEveryHitFieldIsOnTheWireOrExcused sees it on the view type —
// but the line that actually enters the JSON an agent reads was unguarded:
// deleting `StaleIndex: h.StaleIndex` from the handler's literal broke no test
// (review round 2, R2-1). This test drives the real handler against a hybrid
// store whose index half has fallen behind the source of truth, and asserts
// the flag is present in the marshalled am_search response — and absent on a
// healthy recall (omitempty keeps responses byte-identical).
func TestSearchResponseCarriesStaleIndexOnTheWire(t *testing.T) {
	gdb := graphTestDB(t)
	idx := &wireProbeIndex{}
	// CountTTL=0 makes the cached pair expire immediately, so every recall
	// re-counts — the test moves the index half behind the source of truth
	// between two searches and the second recall deterministically sees the
	// deficit. No wall-clock sleep, no margin for a loaded CI box.
	cfg := store.DefaultGateConfig()
	cfg.CountTTL = 0
	h := store.NewHybridWithConfig(sqlitevec.New(gdb), idx, cfg)
	drawers := palace.NewService(palace.NewRepo(gdb, gdb), graphTestEmbedder{}, h, budgetDim)
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{
		TeamID: budgetTeam, UserID: "u1", Role: tenant.RoleAdmin,
	})

	if _, err := drawers.Add(ctx, budgetTeam, palace.AddInput{
		Wing: budgetWing, Room: budgetRoom, SourceFile: "stale-wire-memory",
		Content: "stale index wire probe: content the search must recall",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	registerSearch(&registrar{srv: srv}, drawers,
		usage.NewService(usage.NewRepo(gdb), graphTestCaps{}), false)
	const tool = mcpprotocol.ToolPrefix + "search"
	st := srv.GetTool(tool)
	if st.Handler == nil {
		t.Fatal("search is not registered — this check has stopped checking anything")
	}
	search := func() string {
		res, err := st.Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name: tool, Arguments: map[string]any{
				"query": "stale index wire probe", "wing": budgetWing, "limit": 5,
			},
		}})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		return resultText(res)
	}

	// Healthy: the halves agree, the index serves, and omitempty keeps the flag
	// out of the JSON an agent reads.
	if body := search(); strings.Contains(body, "stale_index") {
		t.Fatalf("healthy recall carried stale_index on the wire: %s", body)
	}

	// The index half loses its collection (a wiped qdrant), and every write
	// that could heal it fails. The deficit is real; the next recall must serve
	// from the source of truth and say so on the wire.
	idx.mu.Lock()
	idx.down, idx.count = true, 0
	idx.mu.Unlock()
	body := search()
	var decoded struct {
		Hits []struct {
			StaleIndex bool `json:"stale_index"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode search result: %v\n%s", err, body)
	}
	if len(decoded.Hits) == 0 {
		t.Fatal("no hits from the source-of-truth fallback; the response proves nothing")
	}
	if !decoded.Hits[0].StaleIndex {
		t.Fatalf("recall served from a behind index without stale_index on the wire: %s", body)
	}
}
