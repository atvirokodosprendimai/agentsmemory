// Package sqlitevec is the SQLite source-of-truth vector store.
//
// It durably persists every embedding and payload in the shared "vectors" table
// (schema owned by goose, package db/migrations) and serves brute-force cosine
// search. In production Qdrant answers queries (see store.Hybrid) and this store
// is "store only"; but it doubles as the search backend on a dev box with no
// Qdrant, and its Search is handy for verifying a rebuilt index. Vectors are
// stored as little-endian float32 BLOBs so the file stays a single, portable,
// litestream-backupable artifact from which any index can be rebuilt without
// re-embedding.
//
// gorm is the query layer only — goose owns the schema, so AutoMigrate is never
// called. The driver is glebarez/sqlite (pure Go, no cgo), so search is plain Go
// arithmetic rather than a SQLite extension.
package sqlitevec

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"sort"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// vectorRow is the gorm view of one row in the shared "vectors" table. The
// composite primary key (namespace, id) is what makes Upsert replace-by-ID.
type vectorRow struct {
	Namespace string `gorm:"column:namespace;primaryKey"`
	ID        string `gorm:"column:id;primaryKey"`
	Dim       int    `gorm:"column:dim"`
	Vector    []byte `gorm:"column:vector"`
	Payload   []byte `gorm:"column:payload"`
}

// TableName pins the table name so gorm does not pluralise the struct name.
func (vectorRow) TableName() string { return "vectors" }

// Store is the SQLite-backed source of truth.
type Store struct {
	db *gorm.DB
}

// compile-time proof Store is a full source of truth (and thus a VectorStore).
var _ store.SourceOfTruth = (*Store)(nil)

// New wraps an open gorm DB. The caller owns the connection lifecycle; the
// "vectors" table must already exist (applied by goose migrations).
func New(db *gorm.DB) *Store { return &Store{db: db} }

// EnsureNamespace is a no-op: every tenant shares one table partitioned by the
// namespace column, and the schema is owned by goose, so there is nothing to
// create per namespace. It exists to satisfy the VectorStore seam uniformly.
func (s *Store) EnsureNamespace(ctx context.Context, namespace string, dim int) error {
	return nil
}

// Upsert inserts or replaces points by (namespace, id) in a single batched
// statement.
func (s *Store) Upsert(ctx context.Context, namespace string, points []store.Point) error {
	if len(points) == 0 {
		return nil
	}
	rows := make([]vectorRow, 0, len(points))
	for _, p := range points {
		payload, err := json.Marshal(orEmpty(p.Payload))
		if err != nil {
			return err
		}
		rows = append(rows, vectorRow{
			Namespace: namespace,
			ID:        p.ID,
			Dim:       len(p.Vector),
			Vector:    encodeVector(p.Vector),
			Payload:   payload,
		})
	}
	// ON CONFLICT (namespace, id) DO UPDATE — keep the latest vector/payload.
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&rows).Error
}

// Search scans the namespace and ranks every stored vector by cosine similarity.
// Brute force is intentional: the SQLite tier is the dev/fallback path, while
// Qdrant is the scale path for real query volume.
func (s *Store) Search(ctx context.Context, namespace string, vector []float32, k int) ([]store.Hit, error) {
	if k <= 0 {
		return nil, nil
	}
	rows, err := s.rows(ctx, namespace)
	if err != nil {
		return nil, err
	}
	queryNorm := norm(vector) // precomputed once; reused for every candidate
	hits := make([]store.Hit, 0, len(rows))
	for _, r := range rows {
		hits = append(hits, store.Hit{
			ID:      r.ID,
			Score:   cosine(vector, queryNorm, decodeVector(r.Vector)),
			Payload: decodePayload(r.Payload),
		})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// Delete removes the given IDs within the namespace; absent IDs are ignored.
func (s *Store) Delete(ctx context.Context, namespace string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).
		Where("namespace = ? AND id IN ?", namespace, ids).
		Delete(&vectorRow{}).Error
}

// AllPoints returns every stored point in the namespace, vectors included, so a
// search index can be rebuilt from the source of truth without re-embedding.
func (s *Store) AllPoints(ctx context.Context, namespace string) ([]store.Point, error) {
	rows, err := s.rows(ctx, namespace)
	if err != nil {
		return nil, err
	}
	points := make([]store.Point, 0, len(rows))
	for _, r := range rows {
		points = append(points, store.Point{
			ID:      r.ID,
			Vector:  decodeVector(r.Vector),
			Payload: decodePayload(r.Payload),
		})
	}
	return points, nil
}

// Namespaces lists every namespace that currently holds at least one vector — the
// set the `sync` command replays into the search index. DISTINCT over the shared
// partitioned table; order is unspecified.
func (s *Store) Namespaces(ctx context.Context) ([]string, error) {
	var nss []string
	if err := s.db.WithContext(ctx).
		Model(&vectorRow{}).
		Distinct("namespace").
		Pluck("namespace", &nss).Error; err != nil {
		return nil, err
	}
	return nss, nil
}

// rows loads all rows for a namespace — the shared read path for Search and
// AllPoints.
func (s *Store) rows(ctx context.Context, namespace string) ([]vectorRow, error) {
	var rows []vectorRow
	if err := s.db.WithContext(ctx).Where("namespace = ?", namespace).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// --- encoding helpers -------------------------------------------------------

// encodeVector packs float32s as little-endian bytes for BLOB storage.
func encodeVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

// decodeVector reverses encodeVector. A length not divisible by 4 yields a
// truncated vector rather than a panic — defensive against a corrupt row.
func decodeVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// orEmpty normalises a nil payload to an empty map so JSON never stores "null".
func orEmpty(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return p
}

// decodePayload tolerates empty or malformed JSON by returning an empty map,
// because a payload is metadata — a bad one must not break a search result.
func decodePayload(b []byte) map[string]any {
	if len(b) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// --- cosine similarity ------------------------------------------------------

// norm is the Euclidean length of v (float64 accumulation to limit error).
func norm(v []float32) float64 {
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	return math.Sqrt(sum)
}

// cosine returns the cosine similarity of a and b in [-1, 1]. aNorm is a's
// precomputed length. Mismatched dimensions or a zero-length vector score 0,
// which sorts them last without poisoning the ranking with NaN.
func cosine(a []float32, aNorm float64, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	bNorm := norm(b)
	if aNorm == 0 || bNorm == 0 {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return float32(dot / (aNorm * bNorm))
}
