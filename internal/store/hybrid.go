package store

import (
	"context"
	"fmt"
)

// Hybrid makes a SourceOfTruth (SQLite) the durable store and a second
// VectorStore (Qdrant) the search index, per the user's 2026-06-26 decision:
// "sqlite as source of truth", "sqlite only to store, qdrant for search".
//
// Write ordering is deliberate: the SoT is written first, so a vector is durable
// before the index ever sees it. The index is written second and is treated as
// rebuildable — if it lags or fails, the SoT still holds the truth and Rebuild
// can replay it. Searches are served entirely by the index.
//
// Hybrid itself satisfies VectorStore, so callers depend only on the seam and
// never learn whether they are talking to one backend or two.
type Hybrid struct {
	sot   SourceOfTruth // durable truth (SQLite)
	index VectorStore   // derived search index (Qdrant)
}

// compile-time proof Hybrid is a drop-in VectorStore.
var _ VectorStore = (*Hybrid)(nil)

// NewHybrid pairs a source of truth with a search index.
func NewHybrid(sot SourceOfTruth, index VectorStore) *Hybrid {
	return &Hybrid{sot: sot, index: index}
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
		return fmt.Errorf("index upsert (source of truth ok, run Rebuild): %w", err)
	}
	return nil
}

// Search is served by the index — the SoT only stores, it does not serve
// production queries.
func (h *Hybrid) Search(ctx context.Context, namespace string, vector []float32, k int) ([]Hit, error) {
	return h.index.Search(ctx, namespace, vector, k)
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
