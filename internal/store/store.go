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
	// ordered closest-first. Fewer than k hits means the namespace held fewer
	// points; a k <= 0 returns no hits.
	Search(ctx context.Context, namespace string, vector []float32, k int) ([]Hit, error)

	// Delete removes points by ID. IDs that are not present are ignored; an
	// empty slice is a no-op.
	Delete(ctx context.Context, namespace string, ids []string) error
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
}
