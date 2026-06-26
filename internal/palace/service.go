package palace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"

	"gorm.io/gorm"
)

// Sentinel errors the MCP layer maps to tool-level results. Keeping them here
// lets the transport stay ignorant of gorm: the service is the only place that
// knows the persistence library.
var (
	// ErrNotFound is returned when a drawer id does not exist for the team.
	ErrNotFound = errors.New("drawer not found")
	// ErrInvalidInput is returned when a required argument is missing or empty.
	ErrInvalidInput = errors.New("invalid input")
)

// Defaults and bounds for search/recall, mirroring the frozen Python contract so
// the tool surface behaves identically (search: limit 1-100 def5, max_distance
// 0-2 def1.5; check_duplicate: threshold def0.9).
const (
	DefaultSearchLimit  = 5
	MaxSearchLimit      = 100
	DefaultMaxDistance  = 1.5
	DefaultDupThreshold = 0.9

	// searchCandidatePool is how many nearest neighbours to pull before applying
	// a wing/room filter. The vector seam's Search has no server-side filter, so
	// an over-fetch leaves enough survivors after filtering to fill the page.
	searchCandidatePool = 200
)

// AAAKSpec is the compressed memory dialect agents use for diary and closet
// lines. It is static reference text (the get_aaak_spec tool returns it verbatim)
// so it lives as a constant rather than in storage.
const AAAKSpec = `AAAK is a compressed memory dialect MemPalace uses for efficient, human- and LLM-readable storage.

FORMAT:
  ENTITIES: 3-letter uppercase codes (ALC=Alice, JOR=Jordan).
  EMOTIONS: *markers* before text (*warm*=joy, *fierce*=determined, *raw*=vulnerable, *bloom*=tenderness).
  STRUCTURE: pipe-separated fields. FAM: family | PROJ: projects | ⚠: warnings.
  DATES: ISO (2026-03-31). COUNTS: Nx = N mentions. IMPORTANCE: ★ to ★★★★★.

Read AAAK naturally — expand codes mentally, treat *markers* as emotional context.
When writing AAAK: use entity codes, mark emotions, keep structure tight.`

// Embedder turns text into vectors. It is declared at the consumer (per Go's
// "accept interfaces" guidance) so the service depends on the capability, not on
// the concrete Ollama client — which also makes it trivial to fake in tests.
type Embedder interface {
	// Embed returns one vector per input string, in order.
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	// EmbedOne is the single-string convenience used by search and check_duplicate.
	EmbedOne(ctx context.Context, input string) ([]float32, error)
}

// Service is the core memory loop: it files drawers (chunk -> embed -> store) and
// recalls them (embed query -> nearest-neighbour -> join metadata). It composes
// the metadata Repo, an Embedder, and the vector store seam; everything is
// tenant-scoped by the teamID argument, which is also the vector namespace.
type Service struct {
	repo    *Repo
	embed   Embedder
	vectors store.VectorStore
	dim     int // embedding dimension new namespaces are created with (bge-m3 = 1024)
}

// NewService wires the collaborators. dim is the embedding width used to create a
// tenant's vector namespace on first write (the actual width of returned vectors
// is authoritative and used in Add; dim is only the seed/fallback).
func NewService(repo *Repo, embed Embedder, vectors store.VectorStore, dim int) *Service {
	return &Service{repo: repo, embed: embed, vectors: vectors, dim: dim}
}

// AddInput is the add_drawer payload: where the memory goes (wing, room — both
// required), the verbatim text, and optional provenance/date metadata.
type AddInput struct {
	Wing        string
	Room        string
	Content     string
	SourceFile  string
	ContentDate string
}

// Add files a memory: it chunks oversized content, embeds every chunk in one
// batch, writes the vectors, then writes the metadata rows. Vectors are written
// before rows so a row never exists without its embedding — search joins row to
// vector, and the inverse orphan (a vector with no row) is harmless because
// search skips ids it cannot resolve. It returns the drawers created (one per
// chunk), so the tool can report their ids.
func (s *Service) Add(ctx context.Context, teamID string, in AddInput) ([]Drawer, error) {
	wing := strings.TrimSpace(in.Wing)
	room := strings.TrimSpace(in.Room)
	content := strings.TrimSpace(in.Content)
	if wing == "" || room == "" || content == "" {
		return nil, fmt.Errorf("%w: wing, room and content are required", ErrInvalidInput)
	}

	chunks := ChunkText(content, ChunkSize, ChunkOverlap, ChunkMin)
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vectors, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed drawer: %w", err)
	}

	// The model's actual vector width is authoritative for namespace creation, so
	// a mis-set dim can never make EnsureNamespace and Upsert disagree.
	dim := s.dim
	if len(vectors) > 0 {
		dim = len(vectors[0])
	}
	if err := s.vectors.EnsureNamespace(ctx, teamID, dim); err != nil {
		return nil, fmt.Errorf("ensure namespace: %w", err)
	}

	filedAt := time.Now().UTC().Format(time.RFC3339)
	drawers := make([]Drawer, len(chunks))
	points := make([]store.Point, len(chunks))
	// The first chunk's id is the parent the rest of a multi-chunk write point
	// back to; the first chunk itself has no parent.
	rootID := DrawerID(teamID, wing, room, in.SourceFile, 0)
	for i, c := range chunks {
		id := DrawerID(teamID, wing, room, in.SourceFile, c.Index)
		parentID := rootID
		if i == 0 {
			parentID = ""
		}
		drawers[i] = Drawer{
			ID:          id,
			TeamID:      teamID,
			Wing:        wing,
			Room:        room,
			SourceFile:  in.SourceFile,
			ChunkIndex:  c.Index,
			Content:     c.Content,
			FiledAt:     filedAt,
			ContentDate: strings.TrimSpace(in.ContentDate),
			ParentID:    parentID,
		}
		// Payload carries only the cheap filter keys; the verbatim content stays
		// single-sourced in the drawers table, joined back by id at search time.
		points[i] = store.Point{
			ID:      id,
			Vector:  vectors[i],
			Payload: map[string]any{"wing": wing, "room": room},
		}
	}

	if err := s.vectors.Upsert(ctx, teamID, points); err != nil {
		return nil, fmt.Errorf("upsert vectors: %w", err)
	}
	if err := s.repo.Save(ctx, drawers); err != nil {
		return nil, fmt.Errorf("save drawers: %w", err)
	}
	return drawers, nil
}

// Get returns one drawer, mapping an unknown id to ErrNotFound.
func (s *Service) Get(ctx context.Context, teamID, id string) (Drawer, error) {
	d, err := s.repo.Get(ctx, teamID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Drawer{}, ErrNotFound
	}
	return d, err
}

// Update edits an existing drawer's content/wing/room in place (its id is
// stable). Any change re-embeds the drawer's final content and re-upserts the
// vector so search and its payload filter keys stay consistent with the row —
// even a wing-only change refreshes the payload. A no-op patch just returns the
// current drawer.
func (s *Service) Update(ctx context.Context, teamID, id string, patch DrawerPatch) (Drawer, error) {
	updated, err := s.repo.Update(ctx, teamID, id, patch)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Drawer{}, ErrNotFound
	}
	if err != nil {
		return Drawer{}, err
	}
	// Only re-touch the vector when something that affects it actually changed.
	if patch.Content == nil && patch.Wing == nil && patch.Room == nil {
		return updated, nil
	}
	vec, err := s.embed.EmbedOne(ctx, updated.Content)
	if err != nil {
		return Drawer{}, fmt.Errorf("re-embed updated drawer: %w", err)
	}
	point := store.Point{
		ID:      updated.ID,
		Vector:  vec,
		Payload: map[string]any{"wing": updated.Wing, "room": updated.Room},
	}
	if err := s.vectors.Upsert(ctx, teamID, []store.Point{point}); err != nil {
		return Drawer{}, fmt.Errorf("re-upsert updated vector: %w", err)
	}
	return updated, nil
}

// Delete removes a drawer's metadata row and its vector. The row goes first so
// the authoritative record is gone before the derived index; a failed vector
// delete leaves an orphan the next search harmlessly skips.
func (s *Service) Delete(ctx context.Context, teamID, id string) error {
	if err := s.repo.Delete(ctx, teamID, id); err != nil {
		return fmt.Errorf("delete drawer row: %w", err)
	}
	if err := s.vectors.Delete(ctx, teamID, []string{id}); err != nil {
		return fmt.Errorf("delete drawer vector: %w", err)
	}
	return nil
}

// List paginates a team's drawers, optionally narrowed to a wing and/or room.
func (s *Service) List(ctx context.Context, teamID, wing, room string, limit, offset int) ([]Drawer, error) {
	return s.repo.List(ctx, teamID, wing, room, limit, offset)
}

// SearchQuery is the mempalace_search input.
type SearchQuery struct {
	Query       string
	Wing        string  // optional filter
	Room        string  // optional filter
	Limit       int     // 1..100, defaults to DefaultSearchLimit
	MaxDistance float64 // drop hits farther than this; <=0 disables the filter
}

// Search recalls drawers by semantic similarity to a query. It embeds the query,
// pulls nearest neighbours from the vector index, applies the wing/room and
// max-distance filters, then joins the surviving ids back to their verbatim
// rows. Ranking is vector-only for now; the Python BM25 + closet-boost fusion is
// a later phase, so Score is the cosine similarity and Distance is 1-similarity.
func (s *Service) Search(ctx context.Context, teamID string, q SearchQuery) ([]SearchHit, error) {
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return nil, fmt.Errorf("%w: query is required", ErrInvalidInput)
	}
	if len(query) > 250 {
		query = query[:250] // the contract caps queries at 250 chars
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	vec, err := s.embed.EmbedOne(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Over-fetch when filtering so enough candidates survive to fill the page;
	// otherwise the page size is exactly what we need.
	candidateK := limit
	filtering := q.Wing != "" || q.Room != ""
	if filtering && candidateK < searchCandidatePool {
		candidateK = searchCandidatePool
	}
	hits, err := s.vectors.Search(ctx, teamID, vec, candidateK)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// Resolve candidate ids to rows in one query, then walk hits in score order.
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	rows, err := s.repo.GetMany(ctx, teamID, ids)
	if err != nil {
		return nil, fmt.Errorf("load drawer rows: %w", err)
	}

	results := make([]SearchHit, 0, limit)
	for _, h := range hits {
		d, ok := rows[h.ID]
		if !ok {
			continue // orphan vector (row deleted) — skip
		}
		if q.Wing != "" && d.Wing != q.Wing {
			continue
		}
		if q.Room != "" && d.Room != q.Room {
			continue
		}
		distance := distanceFromScore(h.Score)
		if q.MaxDistance > 0 && distance > q.MaxDistance {
			continue
		}
		results = append(results, SearchHit{
			Drawer:   d,
			Score:    float64(h.Score),
			Distance: distance,
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

// DuplicateResult is the check_duplicate verdict: whether the most similar
// existing drawer crosses the threshold, that similarity, and the match (nil
// when nothing is similar enough).
type DuplicateResult struct {
	IsDuplicate bool
	Similarity  float64
	Drawer      *Drawer
}

// CheckDuplicate reports whether content is near-identical to an existing drawer.
// It embeds the content, takes the single nearest neighbour, and compares its
// cosine similarity to threshold (callers pass DefaultDupThreshold when unset).
func (s *Service) CheckDuplicate(ctx context.Context, teamID, content string, threshold float64) (DuplicateResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return DuplicateResult{}, fmt.Errorf("%w: content is required", ErrInvalidInput)
	}
	vec, err := s.embed.EmbedOne(ctx, content)
	if err != nil {
		return DuplicateResult{}, fmt.Errorf("embed content: %w", err)
	}
	hits, err := s.vectors.Search(ctx, teamID, vec, 1)
	if err != nil {
		return DuplicateResult{}, fmt.Errorf("vector search: %w", err)
	}
	if len(hits) == 0 {
		return DuplicateResult{IsDuplicate: false}, nil
	}
	top := hits[0]
	sim := float64(top.Score)
	res := DuplicateResult{IsDuplicate: sim >= threshold, Similarity: sim}
	if res.IsDuplicate {
		if d, err := s.repo.Get(ctx, teamID, top.ID); err == nil {
			res.Drawer = &d
		}
	}
	return res, nil
}

// Taxonomy is the get_taxonomy view: every wing with its rooms and counts.
type Taxonomy struct {
	Wings []TaxonomyWing `json:"wings"`
}

// TaxonomyWing is one wing in the taxonomy: its totals and the rooms inside it.
type TaxonomyWing struct {
	Wing    string     `json:"wing"`
	Drawers int        `json:"drawers"`
	Rooms   []RoomStat `json:"rooms"`
}

// GetTaxonomy assembles the wing -> rooms tree from the two indexed
// aggregations, so an agent can see the shape of a team's memory before searching.
func (s *Service) GetTaxonomy(ctx context.Context, teamID string) (Taxonomy, error) {
	wings, err := s.repo.Wings(ctx, teamID)
	if err != nil {
		return Taxonomy{}, err
	}
	rooms, err := s.repo.Rooms(ctx, teamID, "")
	if err != nil {
		return Taxonomy{}, err
	}
	byWing := make(map[string][]RoomStat, len(wings))
	for _, r := range rooms {
		byWing[r.Wing] = append(byWing[r.Wing], r)
	}
	tax := Taxonomy{Wings: make([]TaxonomyWing, 0, len(wings))}
	for _, w := range wings {
		tax.Wings = append(tax.Wings, TaxonomyWing{
			Wing:    w.Wing,
			Drawers: w.Drawers,
			Rooms:   byWing[w.Wing],
		})
	}
	return tax, nil
}

// Wings and Rooms expose the list_wings / list_rooms aggregations directly.
func (s *Service) Wings(ctx context.Context, teamID string) ([]WingStat, error) {
	return s.repo.Wings(ctx, teamID)
}

// Rooms lists a team's rooms, optionally within one wing.
func (s *Service) Rooms(ctx context.Context, teamID, wing string) ([]RoomStat, error) {
	return s.repo.Rooms(ctx, teamID, wing)
}

// Reconnect re-readies a tenant's vector namespace and confirms the store is
// reachable. The Python tool invalidated a cached Qdrant client; this server is
// stateless (no per-session cache), so reconnect has no client to drop — it is
// instead a cheap liveness probe agents can call to verify the backend before a
// burst of writes. EnsureNamespace is idempotent, so re-running it is safe.
func (s *Service) Reconnect(ctx context.Context, teamID string) error {
	if err := s.vectors.EnsureNamespace(ctx, teamID, s.dim); err != nil {
		return fmt.Errorf("reconnect: vector store unreachable: %w", err)
	}
	return nil
}

// distanceFromScore converts a cosine similarity in [-1, 1] into a distance in
// [0, 2] (0 = identical), matching the Python contract's max_distance scale.
func distanceFromScore(score float32) float64 {
	d := 1 - float64(score)
	if d < 0 {
		return 0
	}
	if d > 2 {
		return 2
	}
	return d
}
