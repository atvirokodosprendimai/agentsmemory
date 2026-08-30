package store_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// The R2 serving-gate tests pair two purpose-built fakes: a source of truth
// whose Search actually answers (the fallback is only observable through the
// result it serves), and an index that can lag, fail, and block on demand.

type gateSoT struct {
	mu        sync.Mutex
	points    map[string][]store.Point
	countErr  error // failure injection for the truth-half count
	searchErr error // failure injection for the truth-half search
}

func newGateSoT() *gateSoT { return &gateSoT{points: map[string][]store.Point{}} }

func (f *gateSoT) EnsureNamespace(context.Context, string, int) error { return nil }

func (f *gateSoT) Upsert(_ context.Context, ns string, pts []store.Point) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.points[ns] = append(f.points[ns], pts...)
	return nil
}

// Search serves every stored point — enough for the gate tests, which assert
// counts and the stale flag, never ranking.
func (f *gateSoT) Search(_ context.Context, ns string, _ []float32, k int, _ store.Filter) (store.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.searchErr != nil {
		return store.SearchResult{}, f.searchErr
	}
	var hits []store.Hit
	for _, p := range f.points[ns] {
		hits = append(hits, store.Hit{ID: p.ID, Score: 1, Payload: p.Payload})
	}
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return store.SearchResult{H: hits}, nil
}

func (f *gateSoT) Count(_ context.Context, ns string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.countErr != nil {
		return 0, f.countErr
	}
	return len(f.points[ns]), nil
}

func (f *gateSoT) Delete(_ context.Context, ns string, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := f.points[ns][:0]
	for _, p := range f.points[ns] {
		if !slices.Contains(ids, p.ID) {
			keep = append(keep, p)
		}
	}
	f.points[ns] = keep
	return nil
}

func (f *gateSoT) PointsByIDs(_ context.Context, ns string, ids []string) ([]store.Point, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Point
	for _, p := range f.points[ns] {
		if slices.Contains(ids, p.ID) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *gateSoT) SetPayload(_ context.Context, ns string, ids []string, patch map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.points[ns] {
		if slices.Contains(ids, p.ID) {
			for k, v := range patch {
				p.Payload[k] = v
			}
		}
	}
	return nil
}

func (f *gateSoT) AllPoints(_ context.Context, ns string) ([]store.Point, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.points[ns], nil
}

func (f *gateSoT) Namespaces(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for ns := range f.points {
		out = append(out, ns)
	}
	return out, nil
}

// count reads the population under the lock — the tests poll it from the
// calling goroutine while the async rebuild writes it.
func (f *gateSoT) count(ns string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.points[ns])
}

type gateIndex struct {
	mu        sync.Mutex
	points    map[string][]store.Point // what the index holds
	ensure    int                      // rebuild attempts: Rebuild ensures before it upserts
	upsertErr error                    // failure injection for the index write
	countErr  error                    // failure injection for the index count read
	block     chan struct{}            // when set, Upsert signals then waits for release
}

func newGateIndex() *gateIndex {
	return &gateIndex{points: map[string][]store.Point{}}
}

func (f *gateIndex) EnsureNamespace(context.Context, string, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure++
	return nil
}

func (f *gateIndex) Upsert(_ context.Context, ns string, pts []store.Point) error {
	f.mu.Lock()
	if f.upsertErr != nil {
		f.mu.Unlock()
		return f.upsertErr
	}
	block := f.block
	f.mu.Unlock()
	if block != nil {
		block <- struct{}{}
		<-block
	}
	// Upsert REPLACES by ID, like every real backend: the rebuild of a 7/10
	// namespace must restore 10, not stack 10 on 7.
	f.mu.Lock()
	defer f.mu.Unlock()
	byID := map[string]store.Point{}
	for _, p := range f.points[ns] {
		byID[p.ID] = p
	}
	for _, p := range pts {
		byID[p.ID] = p
	}
	f.points[ns] = f.points[ns][:0]
	for _, p := range byID {
		f.points[ns] = append(f.points[ns], p)
	}
	return nil
}

func (f *gateIndex) Search(_ context.Context, ns string, _ []float32, k int, _ store.Filter) (store.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var hits []store.Hit
	for _, p := range f.points[ns] {
		hits = append(hits, store.Hit{ID: p.ID, Score: 1, Payload: p.Payload})
	}
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return store.SearchResult{H: hits}, nil
}

func (f *gateIndex) Count(_ context.Context, ns string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.countErr != nil {
		return 0, f.countErr
	}
	return len(f.points[ns]), nil
}

// approxGateIndex is gateIndex plus ApproximateCounter — the qdrant-shaped
// backend whose sampled count can lag and must be watermark-corroborated.
// Keeping the approximate capability OFF the base fake proves the exact path of
// indexCount: a backend without ApproximateCounter (chromem, sqlitevec) counts
// exactly at any size, and a deficit it reports above the cap is genuine, never
// suppressed as a lagged estimate.
type approxGateIndex struct {
	*gateIndex
	approx map[string]int // injected approximate counts
}

func newApproxGateIndex() *approxGateIndex {
	return &approxGateIndex{gateIndex: newGateIndex(), approx: map[string]int{}}
}

func (f *approxGateIndex) ApproximateCount(_ context.Context, ns string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.countErr != nil {
		return 0, f.countErr
	}
	if v, ok := f.approx[ns]; ok {
		return v, nil
	}
	return len(f.points[ns]), nil
}

func (f *gateIndex) Delete(_ context.Context, ns string, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := f.points[ns][:0]
	for _, p := range f.points[ns] {
		if !slices.Contains(ids, p.ID) {
			keep = append(keep, p)
		}
	}
	f.points[ns] = keep
	return nil
}

func (f *gateIndex) PointsByIDs(_ context.Context, ns string, ids []string) ([]store.Point, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Point
	for _, p := range f.points[ns] {
		if slices.Contains(ids, p.ID) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *gateIndex) SetPayload(_ context.Context, ns string, ids []string, patch map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.points[ns] {
		if slices.Contains(ids, p.ID) {
			for k, v := range patch {
				p.Payload[k] = v
			}
		}
	}
	return nil
}

// drop simulates the index falling behind: it removes n points from the index
// half while the source of truth keeps them.
func (f *gateIndex) drop(ns string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pts := f.points[ns]
	if n >= len(pts) {
		f.points[ns] = nil
		return
	}
	f.points[ns] = pts[:len(pts)-n]
}

// count reads the population under the lock — the tests poll it from the
// calling goroutine while the async rebuild writes it.
func (f *gateIndex) count(ns string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.points[ns])
}

// ensureCount reads the rebuild-attempt counter under the lock.
func (f *gateIndex) ensureCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ensure
}

// setUpsertErr swaps the failure injection under the lock, so a test cannot
// race the async rebuild's Upsert.
func (f *gateIndex) setUpsertErr(e error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertErr = e
}

// setCountErr swaps the index-count failure injection under the lock, so a test
// can make the index half look wiped or unreachable.
func (f *gateIndex) setCountErr(e error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countErr = e
}

// setCountErr swaps the source-of-truth-count failure injection under the lock.
func (f *gateSoT) setCountErr(e error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countErr = e
}

// setApprox injects the approximate-count value under the lock.
func (f *approxGateIndex) setApprox(ns string, v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approx[ns] = v
}

// newGateHybrid pairs the two fakes under a config the test can shrink.
func newGateHybrid(t *testing.T, cfg store.GateConfig) (*store.Hybrid, *gateSoT, *gateIndex) {
	t.Helper()
	sot := newGateSoT()
	idx := newGateIndex()
	return store.NewHybridWithConfig(sot, idx, cfg), sot, idx
}

// points builds n points with ids a, b, c... and a payload naming the half that
// stores them, so a test can tell which half served a hit.
func points(n int, half string) []store.Point {
	var out []store.Point
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		out = append(out, store.Point{
			ID:      id,
			Vector:  []float32{float32(i), 0, 0},
			Payload: map[string]any{"src": half, "i": i},
		})
	}
	return out
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

var errIndexDown = errors.New("index write down")

// TestSearchFallsBackWhenIndexBehind is the ADR-033 R2 core: an index at 7 of
// 10 serves the source-of-truth hits with stale_index set, never an empty
// result, and the behind index is rebuilt off the request path.
func TestSearchFallsBackWhenIndexBehind(t *testing.T) {
	h, _, idx := newGateHybrid(t, store.DefaultGateConfig())
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(10, "both")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	idx.drop("ns", 3) // the index falls behind by 3

	res, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.H) == 0 {
		t.Fatal("a behind index returned an empty answer")
	}
	if !res.StaleIndex {
		t.Fatal("a behind index served without the stale_index flag")
	}
	for _, hit := range res.H {
		if hit.Payload["src"] != "both" {
			t.Fatalf("fallback hit %q came from %v, want the source of truth", hit.ID, hit.Payload["src"])
		}
	}

	// The trigger is asynchronous: the index is eventually replayed from the
	// source of truth, and a later query serves from it again, unflagged.
	eventually(t, "rebuild to restore the index", func() bool { return idx.count("ns") == 10 })
	res2, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("search after rebuild: %v", err)
	}
	if res2.StaleIndex {
		t.Fatal("a rebuilt index still served with the stale_index flag")
	}
}

// TestSearchDoesNotBlockWhenIndexEmpty pins the anti-stampede property: an
// empty index serves immediately from the source of truth while the rebuild
// runs — no synchronous Rebuild on the request path.
func TestSearchDoesNotBlockWhenIndexEmpty(t *testing.T) {
	h, _, idx := newGateHybrid(t, store.DefaultGateConfig())
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(10, "sot")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	idx.drop("ns", 10) // the index holds nothing
	idx.block = make(chan struct{})

	type outcome struct {
		res store.SearchResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil)
		done <- outcome{res, err}
	}()

	select {
	case <-idx.block:
		// The rebuild started — asynchronously.
	case <-time.After(2 * time.Second):
		t.Fatal("the asynchronous rebuild never started")
	}
	select {
	case o := <-done:
		// The search returned while the rebuild was still blocked: the request
		// path never waited for it.
		if o.err != nil {
			t.Fatalf("search: %v", o.err)
		}
		if len(o.res.H) == 0 || !o.res.StaleIndex {
			t.Fatalf("empty index served empty (%d hits) or unflagged: %+v", len(o.res.H), o.res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("search blocked on the rebuild")
	}
	close(idx.block)
	eventually(t, "rebuild to fill the index", func() bool { return idx.count("ns") == 10 })
}

// TestRebuildBackoffOnFailure pins the cooldown: a failed rebuild does not
// re-run per degraded query, and a subsequent successful write re-arms it.
func TestRebuildBackoffOnFailure(t *testing.T) {
	h, _, idx := newGateHybrid(t, store.DefaultGateConfig()) // 5m cooldown
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(10, "both")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	idx.drop("ns", 3)

	idx.setUpsertErr(errIndexDown)
	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err != nil {
		t.Fatalf("search: %v", err)
	}
	eventually(t, "the failing rebuild to be attempted", func() bool { return idx.ensureCount() == 1 })
	time.Sleep(20 * time.Millisecond) // let the failure land in the gate

	// A second degraded query within the cooldown must NOT trigger another
	// rebuild.
	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err != nil {
		t.Fatalf("search: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := idx.ensureCount(); got != 1 {
		t.Fatalf("a backed-off rebuild re-ran per degraded query: ensure=%d", got)
	}

	// A successful write re-arms the namespace; the next degraded query rebuilds
	// again.
	idx.setUpsertErr(nil)
	if err := h.Upsert(ctx, "ns", points(1, "both")); err != nil {
		t.Fatalf("re-arm upsert: %v", err)
	}
	idx.setUpsertErr(errIndexDown)
	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err != nil {
		t.Fatalf("search after re-arm: %v", err)
	}
	eventually(t, "the re-armed rebuild to be attempted", func() bool { return idx.ensureCount() == 2 })
}

// TestApproximateCountAloneDoesNotTriggerRebuild pins the corroboration rule:
// above the exact-count cap, a lagged approximate count (below the watermark)
// — or the same cached pair read twice — never triggers a rebuild; only a read
// at-or-above the watermark and still below expected does.
func TestApproximateCountAloneDoesNotTriggerRebuild(t *testing.T) {
	cfg := store.DefaultGateConfig()
	cfg.ExactCountCap = 5 // everything below is a tiny namespace
	sot := newGateSoT()
	idx := newApproxGateIndex()
	h := store.NewHybridWithConfig(sot, idx, cfg)
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(10, "sot")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// The successful write recorded the index-ingested watermark: 10.

	idx.setApprox("ns", 8) // a lagged approximate read: below the watermark
	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err != nil {
		t.Fatalf("search: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := idx.ensureCount(); got != 0 {
		t.Fatalf("a lagged approximate count triggered a rebuild: ensure=%d", got)
	}

	// The same cached pair read twice is not a corroboration either.
	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err != nil {
		t.Fatalf("search: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := idx.ensureCount(); got != 0 {
		t.Fatalf("a repeated cached pair triggered a rebuild: ensure=%d", got)
	}

	// A genuine deficit: the source of truth grows past the index (a write whose
	// index half failed), and the approximate count reads at the watermark —
	// still below expected. This corroborates.
	idx.setUpsertErr(errIndexDown)
	if err := h.Upsert(ctx, "ns", points(3, "sot")); err == nil {
		t.Fatal("grow write with a failed index half succeeded")
	}
	idx.setUpsertErr(nil)
	idx.setApprox("ns", 10) // at the watermark, below the new expected 13
	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err != nil {
		t.Fatalf("search: %v", err)
	}
	eventually(t, "the corroborated rebuild to be attempted", func() bool { return idx.ensureCount() == 1 })
}

// TestMinimumInterRebuildInterval pins the pacing: two consecutive triggers
// within the interval observe one rebuild, and a subsequent write re-arms.
func TestMinimumInterRebuildInterval(t *testing.T) {
	cfg := store.DefaultGateConfig()
	cfg.MinRebuildInterval = time.Hour
	h, _, idx := newGateHybrid(t, cfg)
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(10, "both")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	idx.drop("ns", 3)
	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err != nil {
		t.Fatalf("search: %v", err)
	}
	eventually(t, "the first rebuild", func() bool { return idx.count("ns") == 10 })

	idx.drop("ns", 3)
	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err != nil {
		t.Fatalf("search: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := idx.ensureCount(); got != 1 {
		t.Fatalf("a rebuild re-ran within the minimum interval: ensure=%d", got)
	}

	// A write is a state change, not a loop: it re-arms the interval.
	if err := h.Upsert(ctx, "ns", points(1, "both")); err != nil {
		t.Fatalf("re-arm upsert: %v", err)
	}
	idx.drop("ns", 4)
	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err != nil {
		t.Fatalf("search after re-arm: %v", err)
	}
	eventually(t, "the re-armed second rebuild", func() bool { return idx.ensureCount() == 2 })
}

// TestCountCacheInvalidatedOnWrite pins the masking guard: a write whose index
// half failed invalidates the cached count pair, so the next query re-reads and
// serves from the source of truth instead of the behind index.
func TestCountCacheInvalidatedOnWrite(t *testing.T) {
	cfg := store.DefaultGateConfig()
	cfg.CountTTL = time.Hour // long enough that only invalidation explains a re-read
	h, _, idx := newGateHybrid(t, cfg)
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(10, "both")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Prime the cache with an equal-count pair.
	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err != nil {
		t.Fatalf("prime search: %v", err)
	}

	// The index half of the write fails: the source of truth grows, the index
	// does not. Without invalidation the cached (10,10) pair would serve the
	// behind index for the next hour.
	idx.setUpsertErr(errIndexDown)
	if err := h.Upsert(ctx, "ns", points(2, "sot")); err == nil {
		t.Fatal("write with a failed index half succeeded")
	}
	idx.setUpsertErr(nil)

	res, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !res.StaleIndex {
		t.Fatal("a failed index write was masked by the cached count pair: served from the behind index")
	}
}

// TestSearchServesTruthWhenIndexCountFails is the JD-002 gate: an index half
// that cannot be counted (a wiped or missing qdrant collection answers a count
// with 404) reads as "index empty" — the source of truth serves, flagged
// stale, and the rebuild trigger fires. Before the fix the count error made
// countPairFor fail and Hybrid fail-open to the index, which errors too, so the
// query errored and the trigger was never reached.
func TestSearchServesTruthWhenIndexCountFails(t *testing.T) {
	h, _, idx := newGateHybrid(t, store.DefaultGateConfig())
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(10, "sot")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	idx.setCountErr(errIndexDown) // the index half cannot report its population

	res, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("search: %v — an uncountable index must not error the query", err)
	}
	if len(res.H) == 0 {
		t.Fatal("an uncountable index returned an empty answer")
	}
	if !res.StaleIndex {
		t.Fatal("an uncountable index served without the stale_index flag")
	}
	for _, hit := range res.H {
		if hit.Payload["src"] != "sot" {
			t.Fatalf("hit %q came from %v, want the source of truth", hit.ID, hit.Payload["src"])
		}
	}
	// The rebuild trigger fires despite the count error — that is how the wiped
	// collection gets recreated and refilled.
	eventually(t, "the rebuild trigger to fire", func() bool { return idx.ensureCount() == 1 })
}

// TestSearchErrorsWhenTruthCountFails pins the other half of JD-002: a
// source-of-truth count failure is NOT an empty index — the truth itself is
// down, and there is nothing to serve.
func TestSearchErrorsWhenTruthCountFails(t *testing.T) {
	h, sot, _ := newGateHybrid(t, store.DefaultGateConfig())
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(10, "both")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	sot.setCountErr(errIndexDown)

	if _, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil); err == nil {
		t.Fatal("a source-of-truth count failure served a result instead of erroring")
	}
}

// exactGateIndex is the gate index WITHOUT ApproximateCounter — the chromem- or
// sqlitevec-shaped backend that counts exactly and cheaply at any size.
type exactGateIndex struct {
	mu     sync.Mutex
	points map[string][]store.Point
	ensure int // rebuild attempts
}

func newExactGateIndex() *exactGateIndex {
	return &exactGateIndex{points: map[string][]store.Point{}}
}

func (f *exactGateIndex) EnsureNamespace(context.Context, string, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure++
	return nil
}

func (f *exactGateIndex) Upsert(_ context.Context, ns string, pts []store.Point) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	byID := map[string]store.Point{}
	for _, p := range f.points[ns] {
		byID[p.ID] = p
	}
	for _, p := range pts {
		byID[p.ID] = p
	}
	f.points[ns] = f.points[ns][:0]
	for _, p := range byID {
		f.points[ns] = append(f.points[ns], p)
	}
	return nil
}

func (f *exactGateIndex) Search(_ context.Context, ns string, _ []float32, k int, _ store.Filter) (store.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var hits []store.Hit
	for _, p := range f.points[ns] {
		hits = append(hits, store.Hit{ID: p.ID, Score: 1, Payload: p.Payload})
	}
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return store.SearchResult{H: hits}, nil
}

func (f *exactGateIndex) Count(_ context.Context, ns string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.points[ns]), nil
}

func (f *exactGateIndex) Delete(_ context.Context, ns string, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := f.points[ns][:0]
	for _, p := range f.points[ns] {
		if !slices.Contains(ids, p.ID) {
			keep = append(keep, p)
		}
	}
	f.points[ns] = keep
	return nil
}

func (f *exactGateIndex) PointsByIDs(_ context.Context, ns string, ids []string) ([]store.Point, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Point
	for _, p := range f.points[ns] {
		if slices.Contains(ids, p.ID) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *exactGateIndex) SetPayload(_ context.Context, ns string, ids []string, patch map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.points[ns] {
		if slices.Contains(ids, p.ID) {
			for k, v := range patch {
				p.Payload[k] = v
			}
		}
	}
	return nil
}

// drop simulates the index falling behind, like gateIndex.drop.
func (f *exactGateIndex) drop(ns string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pts := f.points[ns]
	if n >= len(pts) {
		f.points[ns] = nil
		return
	}
	f.points[ns] = pts[:len(pts)-n]
}

// ensureCount reads the rebuild-attempt counter under the lock.
func (f *exactGateIndex) ensureCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ensure
}

// TestExactCountAboveCapTriggersRebuild is the JD-004 gate: sampled must mean
// "an approximate count was actually read", not "expected exceeded the cap".
// Above the cap on a backend without ApproximateCounter (chromem, sqlitevec)
// the read is exact, so the watermark suppression must not swallow a genuine
// deficit — a namespace at 7 of 10 must self-heal. Empirically reproduced
// before the fix: 1997/2000 never rebuilt.
func TestExactCountAboveCapTriggersRebuild(t *testing.T) {
	cfg := store.DefaultGateConfig()
	cfg.ExactCountCap = 5 // 10 expected is above the cap
	sot := newGateSoT()
	idx := newExactGateIndex()
	h := store.NewHybridWithConfig(sot, idx, cfg)
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(10, "sot")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	idx.drop("ns", 3) // the index falls behind by 3

	res, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !res.StaleIndex {
		t.Fatal("an exact-read deficit above the cap served unflagged")
	}
	// The deficit is genuine (an exact read, not a lagged estimate), so the
	// rebuild must fire — the watermark suppression is for sampled reads only.
	eventually(t, "the exact-count rebuild to fire", func() bool { return idx.ensureCount() == 1 })
}

// TestSearchServesFromIndexWhenHealthy pins that the gate is neutral on the
// healthy path: equal counts serve from the index, unflagged.
func TestSearchServesFromIndexWhenHealthy(t *testing.T) {
	h, _, idx := newGateHybrid(t, store.DefaultGateConfig())
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(5, "index")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	res, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if res.StaleIndex {
		t.Fatal("a healthy index served with the stale_index flag")
	}
	if len(res.H) != 5 {
		t.Fatalf("healthy search returned %d hits, want 5", len(res.H))
	}
	_ = idx
}

// TestSearchDoesNotStampStaleIndexOnSoTError: a degraded recall whose
// source-of-truth search errors must not return a result carrying the stale
// flag — the flag is a statement about a served recall, and there is no recall
// to stamp. Setting it anyway would hand a caller a result to trust alongside
// the error it is expected to reject.
func TestSearchDoesNotStampStaleIndexOnSoTError(t *testing.T) {
	h, sot, idx := newGateHybrid(t, store.DefaultGateConfig())
	ctx := context.Background()
	if err := h.Upsert(ctx, "ns", points(1, "both")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	idx.drop("ns", 1) // the index falls behind to empty
	sot.searchErr = errors.New("truth down")

	res, err := h.Search(ctx, "ns", []float32{0, 0, 0}, 5, nil)
	if err == nil {
		t.Fatal("the source-of-truth search error must surface")
	}
	if res.StaleIndex {
		t.Error("a failed recall was stamped stale — the flag claims a served result that does not exist")
	}
}
