package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Hybrid makes a SourceOfTruth (SQLite) the durable store and a second
// VectorStore (Qdrant) the search index, per the user's 2026-06-26 decision:
// "sqlite as source of truth", "sqlite only to store, qdrant for search".
//
// Write ordering is deliberate: the SoT is written first, so a vector is durable
// before the index ever sees it. The index is written second and is treated as
// rebuildable — if it lags or fails, the SoT still holds the truth and Rebuild
// can replay it.
//
// Reads are served by the index ONLY while the two halves agree on population;
// when the index holds fewer points, Search serves the source of truth instead
// and marks the result StaleIndex (ADR-033 R2, and see Search). This sentence
// used to say searches were served entirely by the index, which was true when
// written and stopped being true about thirty lines above the fallback that
// falsified it — recorded here rather than merely corrected, because a package
// comment that contradicts its own package is the drift AGENTS.md's
// "documentation is load-bearing" section is about, and it survived a review.
//
// Hybrid itself satisfies VectorStore, so callers depend only on the seam and
// never learn whether they are talking to one backend or two.
type Hybrid struct {
	sot   SourceOfTruth // durable truth (SQLite)
	index VectorStore   // derived search index (Qdrant)

	gate gate
}

// GateConfig pins the R2 serving gate's cost and safety bounds. The gate
// compares the index half's population against the source of truth's before
// serving (ADR-033 R2); these four numbers are the levers an operator needs:
// how long a cached count pair is trusted, where exact counts stop being worth
// their price, and how often a rebuild may fire.
type GateConfig struct {
	// CountTTL is how long a cached count pair is trusted. The comparison runs
	// once per query against the cached pair, so widening rounds (Search is
	// called up to 4x per query via k *= 2) never re-read. A write invalidates
	// the pair regardless of this TTL.
	CountTTL time.Duration

	// ExactCountCap is the largest expected population for which the gate reads
	// an exact index count. Above it the gate reads the approximate count, which
	// can lag; a lagged read is suppressed by the watermark and can never alone
	// trigger a rebuild. Defaults to the package-level ExactCountCap. It is not
	// yet operator-tunable: NewHybrid pins DefaultGateConfig, and wiring the four
	// gate knobs through flags is an open follow-up (ADR-033 T-list).
	ExactCountCap int

	// MinRebuildInterval is the floor between rebuild starts per namespace, so
	// a rebuild — false-triggered or not — cannot repeat in a loop.
	MinRebuildInterval time.Duration

	// RebuildFailureCooldown is how long a FAILED rebuild suppresses re-trigger
	// from degraded queries. A subsequent successful write re-arms the
	// namespace (the failure may have been transient).
	RebuildFailureCooldown time.Duration
}

// DefaultGateConfig is what NewHybrid installs: a 30s count-pair TTL, exact
// counts up to ExactCountCap, and rebuild pacing in minutes.
func DefaultGateConfig() GateConfig {
	return GateConfig{
		CountTTL:               30 * time.Second,
		ExactCountCap:          ExactCountCap,
		MinRebuildInterval:     5 * time.Minute,
		RebuildFailureCooldown: 5 * time.Minute,
	}
}

// compile-time proof Hybrid is a drop-in VectorStore.
var _ VectorStore = (*Hybrid)(nil)

// NewHybrid pairs a source of truth with a search index under the default gate
// configuration.
func NewHybrid(sot SourceOfTruth, index VectorStore) *Hybrid {
	return NewHybridWithConfig(sot, index, DefaultGateConfig())
}

// NewHybridWithConfig pairs a source of truth with a search index and pins the
// serving gate's parameters. Tests use it to shrink the TTL and intervals to
// sub-second values.
func NewHybridWithConfig(sot SourceOfTruth, index VectorStore, cfg GateConfig) *Hybrid {
	return &Hybrid{
		sot:   sot,
		index: index,
		gate: gate{
			cfg:        cfg,
			pair:       map[string]countPair{},
			watermark:  map[string]int{},
			rebuilding: map[string]bool{},
			lastStart:  map[string]time.Time{},
			lastFail:   map[string]time.Time{},
		},
	}
}

// gate holds the R2 serving-gate state. All fields are guarded by mu; the maps
// are created by NewHybridWithConfig and never nil.
type gate struct {
	cfg GateConfig
	mu  sync.Mutex

	// pair caches the last count comparison per namespace (expected, indexed).
	pair map[string]countPair
	// sf deduplicates a namespace's count refresh: N concurrent queries on an
	// expired pair issue one refresh and share its result (singleflight).
	sf singleflight.Group
	// watermark is the index count recorded at the last successful write to
	// the namespace (or rebuild completion). A sampled read below it is a
	// lagged count, not a deficit, and does not corroborate a rebuild.
	watermark map[string]int
	// rebuilding is the single-flight flag: concurrent degraded queries observe
	// one rebuild, none of them synchronously.
	rebuilding map[string]bool
	// lastStart is the last rebuild start, for MinRebuildInterval.
	lastStart map[string]time.Time
	// lastFail is the last rebuild failure, for RebuildFailureCooldown.
	lastFail map[string]time.Time
}

// countPair is one cached population comparison.
type countPair struct {
	expected int
	indexed  int
	// sampled marks an INDEXED count that came from the approximate path: the
	// read may lag the durable count, so the watermark rule suppresses a rebuild
	// triggered by it. An exact Count is never sampled, whatever the expected
	// size — a backend without ApproximateCounter counts exactly at any size,
	// and a deficit it reports is a genuine one.
	sampled bool
	at      time.Time
}

// EnsureNamespace prepares both backends. The SoT comes first for the same
// durability-before-index reason as Upsert.
func (h *Hybrid) EnsureNamespace(ctx context.Context, namespace string, dim int) error {
	if err := h.sot.EnsureNamespace(ctx, namespace, dim); err != nil {
		return fmt.Errorf("source of truth: %w", err)
	}
	if err := h.index.EnsureNamespace(ctx, namespace, dim); err != nil {
		return fmt.Errorf("index (source of truth ok): %w", err)
	}
	return nil
}

// Upsert writes to the source of truth first, then the index. A SoT failure
// aborts before the index is touched (nothing was made durable). An index
// failure is returned but flagged as recoverable: the write is already durable,
// so a Rebuild will reconcile the index without re-embedding.
func (h *Hybrid) Upsert(ctx context.Context, namespace string, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	if err := h.sot.Upsert(ctx, namespace, points); err != nil {
		return fmt.Errorf("source of truth upsert: %w", err)
	}
	if err := h.index.Upsert(ctx, namespace, points); err != nil {
		// Truth is persisted; only the search index lagged. Surface it so the
		// caller knows search may be stale until Rebuild, but do not lose data.
		h.afterWrite(ctx, namespace, false)
		return fmt.Errorf("index upsert (source of truth ok, run Rebuild): %w", err)
	}
	h.afterWrite(ctx, namespace, true)
	return nil
}

// Search is served by the index while the halves agree on population, and by
// the source of truth when the index has fallen behind (ADR-033 R2): a behind
// index cannot return an empty answer to a population gap. The fallback serves
// the SoT's own vector path — the same backend the default deployment serves
// from — marks the result StaleIndex, and triggers an asynchronous rebuild
// that never runs in the request path.
//
// Equal counts serve from the index, behavior unchanged; indexed > expected
// (orphans) also serves from the index — that is a reporting problem, not a
// serving one.
func (h *Hybrid) Search(ctx context.Context, namespace string, vector []float32, k int, filter Filter) (SearchResult, error) {
	p, err := h.countPairFor(ctx, namespace)
	if err != nil {
		// The source of truth could not be counted — the truth itself is down,
		// and there is nothing to serve. An index-count failure never reaches
		// here: countPairFor treats it as an empty index and the truth serves.
		return SearchResult{}, err
	}
	if p.indexed >= p.expected {
		return h.index.Search(ctx, namespace, vector, k, filter)
	}
	// Degraded: the index holds fewer points than the source of truth. Serve
	// the truth and rebuild the index off the request path.
	h.triggerRebuild(namespace, p)
	res, err := h.sot.Search(ctx, namespace, vector, k, filter)
	if err != nil {
		// No recall was served; the stale flag is a statement about a served
		// result, and stamping a failed one would hand a caller a result to trust
		// alongside the error it is expected to reject.
		return res, err
	}
	res.StaleIndex = true
	return res, nil
}

// countRefreshTimeout bounds a single-flight count refresh. The refresh is
// shared work: every concurrent caller on the namespace joins whichever one
// won the race, so it must not inherit any single caller's cancellation — a
// leader that disconnects mid-count would otherwise fail every waiter with
// "context canceled", precisely at the TTL-expiry stampede when concurrency is
// highest. The count therefore runs detached (context.WithoutCancel) with this
// bound: one namespace count is an indexed query per half, and 5s is an order
// of magnitude above the query budget while staying far below any client's
// patience; the bound exists so detached work cannot run forever.
const countRefreshTimeout = 5 * time.Second

// countPairFor returns the cached population comparison for the namespace,
// refreshing it when absent or past the TTL. The cache is what keeps the
// healthy path at one count pair per query rather than one per widening round.
func (h *Hybrid) countPairFor(ctx context.Context, namespace string) (countPair, error) {
	h.gate.mu.Lock()
	if p, ok := h.gate.pair[namespace]; ok && time.Since(p.at) < h.gate.cfg.CountTTL {
		h.gate.mu.Unlock()
		return p, nil
	}
	// The cache check and the singleflight join are one atomic unit under the
	// gate lock: a caller that misses the cache either joins a still-live
	// refresh or finds the pair stored by a completed one — it can never fall
	// into the gap between the two and start a duplicate refresh of its own.
	// DoChan itself never blocks (the refresh runs in its own goroutine), so
	// holding the lock here costs nothing.
	ch := h.gate.sf.DoChan(namespace, func() (result any, err error) {
		defer func() {
			if r := recover(); r != nil {
				// singleflight would otherwise re-raise a panicking refresh in a
				// crash-dump goroutine and never wake the waiters — a wedge with a
				// corpse. Surface the panic as the shared error instead; nothing is
				// cached, so the next query retries cleanly.
				err = fmt.Errorf("count refresh panicked: %v", r)
			}
		}()
		// The refresh is shared work, not the leader's work: ctx belongs to
		// whichever caller happened to win the race, and its cancellation must
		// not fail the waiters (review round 3, R3-1). Detach and bound it,
		// keeping the caller's values so tracing and tenant survive — the same
		// idiom as teiembed's capability probe.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), countRefreshTimeout)
		defer cancel()
		expected, err := h.sot.Count(cctx, namespace)
		if err != nil {
			// The source of truth could not be counted. Not cached: the next
			// query retries, and every concurrent caller sees this exact error.
			return countPair{}, fmt.Errorf("count source of truth: %w", err)
		}
		indexed, sampled, err := h.indexCount(cctx, namespace, expected)
		if err != nil {
			// The index half cannot be counted (a wiped or missing collection
			// answers a count with 404). That is not a population statement — treat
			// it as "index empty": the source of truth is the authority, so Search
			// serves from it and the rebuild trigger fires. A source-of-truth count
			// error above still errors: the truth itself is down and there is
			// nothing to serve.
			p := countPair{expected: expected, indexed: 0, sampled: false, at: time.Now()}
			h.gate.mu.Lock()
			h.gate.pair[namespace] = p
			h.gate.mu.Unlock()
			return p, nil
		}
		p := countPair{expected: expected, indexed: indexed, sampled: sampled, at: time.Now()}
		h.gate.mu.Lock()
		h.gate.pair[namespace] = p
		h.gate.mu.Unlock()
		return p, nil
	})
	h.gate.mu.Unlock()

	select {
	case r := <-ch:
		if r.Err != nil {
			return countPair{}, r.Err
		}
		return r.Val.(countPair), nil
	case <-ctx.Done():
		return countPair{}, ctx.Err()
	}
}

// indexCount reads the index half's population: exact below the ExactCountCap,
// the approximate count above it when the index exposes one (a chromem index
// counts exactly and cheaply at any size, so it keeps its exact count). The
// returned sampled flag says WHICH read was used, so the watermark suppression
// applies only to genuinely sampled reads — an exact count above the cap is a
// real population, not a lagged estimate, and must not be suppressed.
func (h *Hybrid) indexCount(ctx context.Context, namespace string, expected int) (count int, sampled bool, err error) {
	if expected > h.gate.cfg.ExactCountCap {
		if ac, ok := h.index.(ApproximateCounter); ok {
			n, err := ac.ApproximateCount(ctx, namespace)
			return n, true, err
		}
	}
	n, err := h.index.Count(ctx, namespace)
	return n, false, err
}

// triggerRebuild starts an asynchronous rebuild of the index half for the
// namespace, single-flighted and paced: a rebuild in flight suppresses its
// peers, the minimum interval floors the rate, a recent failure backs off, and
// a sampled (approximate) read below the watermark is a lagged count that does
// not corroborate a trigger — only a read at-or-above the watermark and below
// expected is a genuine deficit.
func (h *Hybrid) triggerRebuild(namespace string, p countPair) {
	h.gate.mu.Lock()
	defer h.gate.mu.Unlock()
	if h.gate.rebuilding[namespace] {
		return
	}
	now := time.Now()
	if ls, ok := h.gate.lastStart[namespace]; ok && now.Sub(ls) < h.gate.cfg.MinRebuildInterval {
		return
	}
	if lf, ok := h.gate.lastFail[namespace]; ok && now.Sub(lf) < h.gate.cfg.RebuildFailureCooldown {
		return
	}
	if p.sampled && p.indexed < h.gate.watermark[namespace] {
		return
	}
	h.gate.rebuilding[namespace] = true
	h.gate.lastStart[namespace] = now
	go h.rebuildAsync(namespace)
}

// rebuildAsync runs the rebuild detached from the query that triggered it —
// the query's context may die with the request — then reconciles the gate: the
// cached pair is dropped so the next query re-reads, the failure cooldown
// records a failed rebuild, and a successful rebuild records the fresh index
// count as the watermark.
func (h *Hybrid) rebuildAsync(namespace string) {
	ctx := context.Background()
	err := h.Rebuild(ctx, namespace)

	var watermark int
	if err == nil {
		if c, cerr := h.index.Count(ctx, namespace); cerr == nil {
			watermark = c
		}
	}

	h.gate.mu.Lock()
	defer h.gate.mu.Unlock()
	delete(h.gate.rebuilding, namespace)
	delete(h.gate.pair, namespace)
	if err != nil {
		h.gate.lastFail[namespace] = time.Now()
		return
	}
	h.gate.watermark[namespace] = watermark
}

// afterWrite reconciles the gate with a write that touched the namespace. The
// cached pair is invalidated whatever the index half's outcome — a failed
// index write must not be masked by a cached equal-count pair — and a
// successful index write records the fresh index count as the watermark and
// re-arms a backed-off namespace (the failure may have been transient).
func (h *Hybrid) afterWrite(ctx context.Context, namespace string, indexOK bool) {
	h.gate.mu.Lock()
	// Invalidate the cached pair whatever the index half's outcome — a failed
	// index write must not be masked by a cached equal-count pair.
	delete(h.gate.pair, namespace)
	h.gate.mu.Unlock()
	if !indexOK {
		return
	}
	// The watermark is the reference a deficit is measured against, so it is
	// recorded EXACTLY even above ExactCountCap: the approximate count is biased
	// high on qdrant (deleted-but-not-yet-compacted points inflate it), and an
	// inflated watermark makes a genuinely deficient sampled read look like a
	// lagged one, suppressing the very rebuild that is the mechanism's safety
	// net. Writes are orders of magnitude rarer than queries, so the exact
	// read's cost lands where the cap already concedes it.
	w, err := h.index.Count(ctx, namespace)
	if err != nil {
		return
	}
	h.gate.mu.Lock()
	defer h.gate.mu.Unlock()
	h.gate.watermark[namespace] = w
	delete(h.gate.lastFail, namespace)
	delete(h.gate.lastStart, namespace)
}

// Count reports the serving population: what the INDEX holds. Hybrid searches
// from the index, so the count that matters to a black-box caller is the index
// half's; the coverage check compares the halves itself via Halves().
func (h *Hybrid) Count(ctx context.Context, namespace string) (int, error) {
	return h.index.Count(ctx, namespace)
}

// Halves exposes the two stores a Hybrid pairs, for a caller that must compare
// them rather than use them.
//
// It exists for exactly one reason: the source of truth and the index each keep
// their own copy of a point's payload, and a scoped search filters on the
// INDEX's copy while every other part of the system trusts the row. Measured
// 2026-08-21, those copies had drifted apart on a live palace and the memories
// affected were unreachable from the wing they were filed in. A checker that
// could only see one of the two would have reported clean.
func (h *Hybrid) Halves() (SourceOfTruth, VectorStore) { return h.sot, h.index }

// SetPayload patches BOTH, source of truth first, for the same
// durability-before-index reason as Upsert — and for a second reason this method
// alone has: the index is what a scoped search filters on, and the source of
// truth is what the next Rebuild replays over it. Patching only the index fixes
// search until somebody syncs; patching only the truth fixes nothing today.
//
// Measured 2026-08-21 on a live palace, a wing merge had left 13 points stale in
// BOTH, and a repair of either half alone would have looked complete from the
// other side.
func (h *Hybrid) SetPayload(ctx context.Context, namespace string, ids []string, patch map[string]string) error {
	if len(ids) == 0 || len(patch) == 0 {
		return nil
	}
	if err := h.sot.SetPayload(ctx, namespace, ids, patch); err != nil {
		return fmt.Errorf("source of truth set payload: %w", err)
	}
	if err := h.index.SetPayload(ctx, namespace, ids, patch); err != nil {
		return fmt.Errorf("index set payload (source of truth ok, run Rebuild): %w", err)
	}
	h.afterWrite(ctx, namespace, true)
	return nil
}

// Delete removes from both, SoT first so the truth no longer claims a point the
// index has already dropped.
func (h *Hybrid) Delete(ctx context.Context, namespace string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := h.sot.Delete(ctx, namespace, ids); err != nil {
		return fmt.Errorf("source of truth delete: %w", err)
	}
	if err := h.index.Delete(ctx, namespace, ids); err != nil {
		return fmt.Errorf("index delete (source of truth ok, run Rebuild): %w", err)
	}
	h.afterWrite(ctx, namespace, true)
	return nil
}

// Rebuild reconstructs the search index for a namespace from the source of
// truth, reusing the stored vectors so nothing is re-embedded. Use it after the
// index is lost, falls behind, or is swapped for a different backend.
func (h *Hybrid) Rebuild(ctx context.Context, namespace string) error {
	points, err := h.sot.AllPoints(ctx, namespace)
	if err != nil {
		return fmt.Errorf("read source of truth: %w", err)
	}
	if len(points) == 0 {
		return nil
	}
	// Every stored vector shares the embedder's dimension; the first is enough
	// to (re)create the index namespace before loading points into it.
	if err := h.index.EnsureNamespace(ctx, namespace, len(points[0].Vector)); err != nil {
		return fmt.Errorf("ensure index namespace: %w", err)
	}
	if err := h.index.Upsert(ctx, namespace, points); err != nil {
		return fmt.Errorf("load index from source of truth: %w", err)
	}
	return nil
}

// Namespaces lists the source-of-truth namespaces, so a caller (the `sync`
// command) can Rebuild every one into the index.
func (h *Hybrid) Namespaces(ctx context.Context) ([]string, error) {
	return h.sot.Namespaces(ctx)
}

// PointsByIDs reads stored points from the source of truth (never the derived
// index), so a cross-tenant copy reuses the durable vectors without re-embedding.
func (h *Hybrid) PointsByIDs(ctx context.Context, namespace string, ids []string) ([]Point, error) {
	return h.sot.PointsByIDs(ctx, namespace, ids)
}

// DescribeVectorStore names both halves, satisfying palace.VectorDescriber.
//
// Both are named because the pair IS the interesting fact: a Hybrid serves reads
// from the index while the source of truth holds every point, so "which backend
// answered" has two answers and a trace saying only one of them cannot be read
// against a divergence between them.
func (h *Hybrid) DescribeVectorStore() string {
	return "hybrid(" + describeOr(h.sot, "?") + "->" + describeOr(h.index, "?") + ")"
}

// describeOr names v when it can name itself, and returns fallback otherwise.
func describeOr(v any, fallback string) string {
	d, ok := v.(interface{ DescribeVectorStore() string })
	if !ok {
		return fallback
	}
	return d.DescribeVectorStore()
}
