package palace

import (
	"context"
	"fmt"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// Contextual chunking, as an eval arm.
//
// A memory longer than ChunkSize is split, and each chunk is embedded ALONE —
// so a chunk that says "the fix was to raise the batch size" carries no trace of
// which system it was about. The chunk that answers a question is often not the
// chunk that names the subject, and the embedder never sees the two together.
//
// The published fix is to give each chunk a short piece of its parent's context
// before embedding it (Anthropic's contextual retrieval; late chunking is the
// cheaper cousin). Both are reported to cut retrieval failures on long documents,
// and this palace has 60 multi-chunk memories that have never been tested for it.
//
// This builds the contextual index into a SEPARATE namespace so the comparison is
// against the live one rather than replacing it — an experiment has to be
// reversible or it is a migration.

// DefaultContextualLimit caps the experiment when the caller names no size. It
// is small because the cost is an embedding pass and a second copy of the
// vectors: an experiment that has to be planned around is one nobody runs.
const DefaultContextualLimit = 2000

// contextualNamespace is where the experimental embeddings live: alongside the
// team's real vectors, never in place of them.
func contextualNamespace(teamID string) string { return teamID + "::eval_ctx" }

// ContextPrefix is the header prepended to a chunk before embedding: where the
// memory lives and how it opens. It is deliberately small — the point is to
// restore the SUBJECT a mid-document chunk lost, not to re-embed the parent.
func ContextPrefix(d Drawer, parent string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s/%s", d.Wing, d.Room)
	if d.SourceFile != "" {
		fmt.Fprintf(&b, " %s", d.SourceFile)
	}
	b.WriteString("] ")
	if parent = strings.TrimSpace(parent); parent != "" {
		// The opening of the memory this chunk belongs to, which is where a
		// document usually says what it is about.
		if r := []rune(parent); len(r) > 220 {
			parent = string(r[:220])
		}
		b.WriteString(parent)
		b.WriteString(" — ")
	}
	return b.String()
}

// BuildContextualIndex re-embeds a team's drawers with their context prefix into
// the experimental namespace, returning how many it wrote.
//
// It only pays for itself on multi-chunk memories, but it indexes everything: a
// comparison that quietly excluded the single-chunk majority would flatter the
// arm by measuring it only where it is designed to win.
// It is BOUNDED on purpose. Re-embedding is the expensive operation in this
// system, and a palace can be very large: an unbounded pass would mean hours of
// inference and a second full set of vectors written beside the real ones. limit
// caps how many chunks the experiment covers, and the caller is expected to keep
// it small enough that the answer arrives today.
func (s *Service) BuildContextualIndex(ctx context.Context, teamID string, batch, limit int) (int, error) {
	if batch <= 0 {
		batch = 32
	}
	if limit <= 0 {
		limit = DefaultContextualLimit
	}
	drawers, err := s.repo.List(ctx, teamID, "", "", limit, 0)
	if err != nil {
		return 0, fmt.Errorf("list drawers: %w", err)
	}
	if len(drawers) == 0 {
		return 0, nil
	}

	// Parent openings, so a chunk can be told what its memory is about.
	opening := make(map[string]string, len(drawers))
	for _, d := range drawers {
		if d.ChunkIndex == 0 {
			opening[d.ID] = d.Content
		}
	}

	ns := contextualNamespace(teamID)
	written := 0
	for start := 0; start < len(drawers); start += batch {
		end := start + batch
		if end > len(drawers) {
			end = len(drawers)
		}
		window := drawers[start:end]

		texts := make([]string, len(window))
		for i, d := range window {
			parent := opening[d.ParentID]
			if d.ParentID == "" {
				parent = "" // chunk 0 already opens with its own subject
			}
			texts[i] = ContextPrefix(d, parent) + d.Content
		}
		vectors, err := s.embed.Embed(ctx, texts)
		if err != nil {
			return written, fmt.Errorf("embed contextual chunk batch: %w", err)
		}
		points := make([]storePoint, 0, len(window))
		for i, d := range window {
			points = append(points, storePoint{id: d.ID, vector: vectors[i], wing: d.Wing, room: d.Room})
		}
		if err := s.upsertEvalPoints(ctx, ns, points); err != nil {
			return written, err
		}
		written += len(window)
	}
	return written, nil
}

// storePoint is the minimal shape upsertEvalPoints needs, kept local so the eval
// path does not widen the store seam for an experiment.
type storePoint struct {
	id     string
	vector []float32
	wing   string
	room   string
}

// upsertEvalPoints writes experimental vectors into the scratch namespace,
// carrying the same payload keys the real index uses so a scoped search behaves
// identically.
func (s *Service) upsertEvalPoints(ctx context.Context, namespace string, points []storePoint) error {
	if len(points) == 0 {
		return nil
	}
	pts := make([]store.Point, 0, len(points))
	for _, p := range points {
		pts = append(pts, store.Point{
			ID:      p.id,
			Vector:  p.vector,
			Payload: map[string]any{"wing": p.wing, "room": p.room},
		})
	}
	if err := s.vectors.EnsureNamespace(ctx, namespace, len(points[0].vector)); err != nil {
		return fmt.Errorf("ensure contextual namespace: %w", err)
	}
	if err := s.vectors.Upsert(ctx, namespace, pts); err != nil {
		return fmt.Errorf("write contextual vectors: %w", err)
	}
	return nil
}

// DropContextualIndex removes the experiment's vectors. An experiment that
// cannot be undone is a migration, and this one writes a second copy of every
// chunk it touches — on a large palace that is real storage, so the eval offers
// to give it back.
func (s *Service) DropContextualIndex(ctx context.Context, teamID string, limit int) (int, error) {
	if limit <= 0 {
		limit = DefaultContextualLimit
	}
	drawers, err := s.writer.List(ctx, teamID, "", "", limit, 0)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(drawers))
	for _, d := range drawers {
		ids = append(ids, d.ID)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := s.vectors.Delete(ctx, contextualNamespace(teamID), ids); err != nil {
		return 0, fmt.Errorf("drop contextual vectors: %w", err)
	}
	return len(ids), nil
}
