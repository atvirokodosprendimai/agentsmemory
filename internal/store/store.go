// Package store defines the backend-agnostic vector-storage seam for
// agentsmemory drawers.
//
// The architecture is source-of-truth + derived index (user 2026-06-26:
// "sqlite as source of truth", "sqlite only to store, qdrant for search"):
//
//   - A SourceOfTruth (SQLite) durably holds every vector and its payload. It is
//     portable and can enumerate everything it stores, so any search index can
//     be rebuilt from it without re-embedding.
//   - A VectorStore search index (Qdrant) answers nearest-neighbour queries. It
//     is derived and disposable — losing it costs only a Rebuild from the SoT.
//
// Hybrid wires the two together: writes land in the SoT first, then the index;
// searches are served by the index. Swapping the search backend (Qdrant for
// something else later) therefore means writing one driver and rebuilding the
// index — the truth never moves.
//
// This package is a leaf: it imports nothing from internal/ so that drivers may
// depend on it for the shared value types without an import cycle. The
// driver-selecting factory lives in the composition root (cmd/server), the only
// place that imports every driver.
package store

import "context"

// Point is a single embedding to upsert. ID is the caller's stable identifier
// (e.g. a drawer ID); drivers key on it so a repeated Upsert replaces, never
// duplicates. Payload is opaque metadata the driver round-trips verbatim and may
// return on Search; nil is treated as an empty map.
type Point struct {
	ID      string
	Vector  []float32
	Payload map[string]any
}

// Hit is one nearest-neighbour result. Score is cosine similarity in [-1, 1];
// higher is closer. Payload is whatever was stored with the point.
type Hit struct {
	ID      string
	Score   float32
	Payload map[string]any
}

// SearchResult is what a search returns: the hits, plus whether the index that
// served them was behind the source of truth when it did.
//
// StaleIndex is the carrier of the behind-index flag (ADR-033). It lives on the
// interface's return type — not as a parallel method — because every production
// caller consumes the interface: the flag is reachable from serving or it is
// not reachable at all. A single backend (sqlite, qdrant, chromem) is its own
// truth and reports false; only Hybrid, which compares the two halves, can set
// it, and only when it served from the source of truth because the index lagged.
type SearchResult struct {
	H          []Hit
	StaleIndex bool
}

// ExactCountCap is the largest namespace a coverage check counts exactly. Exact
// counts on a large namespace are the accuracy/cost lever of the backing
// stores; above the cap the check may use an approximate count, flagged as
// sampled (the raw fields' count_quality), and an approximate count ALONE never
// triggers a rebuild (ADR-033) — the corroborating signal is the index-ingested
// watermark.
const ExactCountCap = 100_000

// Filter narrows a search to points whose payload matches every entry, compared
// as strings. An empty (or nil) Filter matches everything.
//
// It exists so the caller's wing/room scoping is answered BY the index rather
// than after it: filtering in the caller means over-fetching a pool wide enough
// that the survivors can still fill a page, which grows with the palace and is
// paid on every scoped search. Every backend here can do it natively — a Qdrant
// payload filter, a chromem metadata `where`, a comparison inside SQLite's
// brute-force scan — so the seam carries it.
type Filter map[string]string

// VectorStore is the swappable vector backend (a search index, or — for SQLite —
// the source of truth doubling as one).
//
// namespace is the per-tenant partition (the team ID). Each driver maps it to
// its own physical unit — a Qdrant collection, or a namespace column in the
// shared SQLite table — so tenants are isolated regardless of backend.
//
// Implementations must be safe for concurrent use by multiple goroutines.
type VectorStore interface {
	// EnsureNamespace makes the tenant's storage ready to hold dim-dimensional
	// vectors, creating it if absent. It is idempotent: calling it on an
	// existing namespace is a no-op.
	EnsureNamespace(ctx context.Context, namespace string, dim int) error

	// Upsert inserts or replaces points in the namespace, keyed by Point.ID.
	// An empty slice is a no-op.
	Upsert(ctx context.Context, namespace string, points []Point) error

	// Search returns up to k nearest neighbours of vector by cosine similarity,
	// ordered closest-first, restricted to points whose payload matches filter.
	// Fewer than k hits means the namespace held fewer matching points; a k <= 0
	// returns no hits. A nil or empty filter searches the whole namespace.
	//
	// The result's StaleIndex carries whether the index that served the query was
	// behind the source of truth when it did. A single backend is its own truth
	// and always reports false; only Hybrid, which can compare the two halves,
	// may set it.
	Search(ctx context.Context, namespace string, vector []float32, k int, filter Filter) (SearchResult, error)

	// Count returns how many points the namespace currently holds, for the
	// coverage check: comparing the two halves tells a caller whether the index
	// ingested everything the source of truth holds. The count is exact for an
	// unfiltered namespace — the only shape this check uses; a driver is not
	// asked to be exact under a filter, because the caller never filters a
	// count.
	Count(ctx context.Context, namespace string) (int, error)

	// Delete removes points by ID. IDs that are not present are ignored; an
	// empty slice is a no-op.
	Delete(ctx context.Context, namespace string, ids []string) error

	// PointsByIDs returns the stored points for the given ids within a namespace,
	// payloads included. Absent ids are simply omitted, matching Delete, so a
	// caller need not check existence first; an empty id list returns nothing.
	// Pass a bounded id list — the caller pages — so a SQL-backed driver stays
	// within its parameter limit.
	//
	// It sits on VectorStore rather than only on SourceOfTruth because a store
	// that can be written and searched but not READ cannot be audited. A payload
	// copy of the wing is what a scoped search actually filters on, so a payload
	// that stops agreeing with the drawer row makes a memory unreachable from the
	// wing it is filed in — measured 2026-08-21 on a live palace, 13 of 359
	// points had drifted exactly that way after wing merges, in BOTH stores.
	// Reading only the source of truth would have reported clean.
	//
	// Whether Vector is populated is the driver's choice: a search index need not
	// return vectors it stores only to search with, and a caller that needs the
	// vector asks a SourceOfTruth. Copying memory between tenants without
	// re-embedding relies on that, which is why this began on SourceOfTruth.
	PointsByIDs(ctx context.Context, namespace string, ids []string) ([]Point, error)

	// SetPayload merges patch into the payload of each named point, leaving the
	// VECTOR untouched. Fields not named in patch are unchanged; ids the store
	// does not hold are ignored; an empty id list or an empty patch is a no-op.
	//
	// It merges rather than replaces because its caller patches one field. A wing
	// merge corrects `wing` and nothing else, and a driver that replaced the
	// payload would erase `room` on every point it fixed — turning a repair of
	// one filter into a break of another.
	//
	// It exists so that correcting a LABEL is not a re-embed. The vector of a
	// relabelled memory is already right, because the text did not change; the
	// alternative is a model call per drawer to fix a string, unbounded in the
	// size of the merged wing.
	SetPayload(ctx context.Context, namespace string, ids []string, patch map[string]string) error
}

// SourceOfTruth is a durable VectorStore that can additionally enumerate
// everything it holds, so a derived search index can be rebuilt from it without
// re-embedding. SQLite is the source of truth for agentsmemory; Qdrant is not
// (it is rebuildable), so only SQLite implements this.
type SourceOfTruth interface {
	VectorStore

	// AllPoints returns every stored point in the namespace, vectors included,
	// for replay into a search index. Order is unspecified.
	AllPoints(ctx context.Context, namespace string) ([]Point, error)

	// Namespaces lists every namespace that currently holds at least one point —
	// the set a full sync replays into the search index. Order is unspecified.
	Namespaces(ctx context.Context) ([]string, error)

	// A SourceOfTruth additionally guarantees PointsByIDs returns the VECTOR as
	// well as the payload — the read half of copying memory between tenants
	// without re-embedding. The interface method is declared on VectorStore; this
	// is the stronger promise the durable store makes about it.
}

// ApproximateCounter is an OPTIONAL refinement of VectorStore, satisfied by an
// index that can report its population cheaply at the cost of exactness. The
// serving gate (Hybrid, ADR-033 R2) reads it instead of Count once a namespace
// is expected to hold more than the gate's ExactCountCap points; the value can
// lag the durable count, so the gate never lets a sampled read alone trigger a
// rebuild — it corroborates against the index-ingested watermark.
//
// A backend whose Count is exact and cheap at any size (chromem counts an
// in-memory collection) simply does not implement this; the gate keeps using
// Count for it.
type ApproximateCounter interface {
	ApproximateCount(ctx context.Context, namespace string) (int, error)
}
