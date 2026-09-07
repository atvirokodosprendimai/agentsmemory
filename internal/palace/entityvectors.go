package palace

import (
	"context"
	"fmt"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// entitySuffix marks a vector namespace as holding entity labels rather than
// drawers. It is a constant shared by the mint and the predicate below so the
// two cannot answer differently about the same string.
const entitySuffix = "::kg_entities"

// entityNamespace holds embedded KG entity LABELS, separate from the drawer
// vectors.
//
// Separate rather than mixed, because the two answer different questions: a
// drawer vector says "this passage is about X", an entity vector says "this NODE
// is called X". Mixing them into one namespace would put nodes and passages in
// competition for the same k slots, and a scoped drawer search would start
// returning entities it cannot render.
func entityNamespace(teamID string) string { return teamID + entitySuffix }

// IsEntityNamespace reports whether a vector namespace holds KG entity labels,
// whose points carry a label and nothing a scoped search filters on.
//
// It is exported for the same reason qdrant.FilterKeys is: a caller that has to
// tell the two kinds of namespace apart would otherwise re-derive the suffix,
// and a copy of a naming rule is a copy that goes stale on the day the rule
// moves. The boot payload check is that caller — it samples every namespace the
// source of truth reports, and entity points legitimately carry no wing/room, so
// without this predicate it warns that scoped search is broken for points no
// scoped search ever touches (issue #164). entityMatches passes a nil filter,
// which is what makes the absence correct rather than merely tolerated.
func IsEntityNamespace(namespace string) bool {
	return strings.HasSuffix(namespace, entitySuffix)
}

// IndexEntityLabel makes one KG entity reachable by a natural-language question
// rather than only by exact name.
//
// This is the seam ADR-036 turns on: kg_entities has B-tree indexes on
// (team_id, subject/object/predicate) and no vector index at all, so before this
// a fact was reachable only by spelling its entity exactly. A question that named
// the thing in any other words found nothing, and reported that as "no facts".
func (s *Service) IndexEntityLabel(ctx context.Context, teamID, entityID, label string) error {
	if entityID == "" || label == "" {
		return nil
	}
	vec, err := s.embed.EmbedOne(ctx, label)
	if err != nil {
		return fmt.Errorf("embed entity label: %w", err)
	}
	ns := entityNamespace(teamID)
	if err := s.vectors.EnsureNamespace(ctx, ns, len(vec)); err != nil {
		return err
	}
	return s.vectors.Upsert(ctx, ns, []store.Point{{
		ID:      entityID,
		Vector:  vec,
		Payload: map[string]any{"label": label},
	}})
}

// DropEntityLabel removes an entity from the label index.
//
// The lifecycle has three parts and only having two is how an index goes stale
// while still answering: entities are indexed as they are written (KGAdd),
// removed when they go (here), and backfilled once for what already existed
// (BackfillEntityLabels). An index written only at backfill is wrong by its
// second day, and it never says so — it just answers with yesterday's graph.
func (s *Service) DropEntityLabel(ctx context.Context, teamID, entityID string) error {
	return s.vectors.Delete(ctx, entityNamespace(teamID), []string{entityID})
}

// entityLabelBatch bounds ONE embed call during the backfill.
//
// ⚠ WITHOUT IT THE BATCH IS THE CORPUS. The first version of this change handed
// `Embed` the whole label slice, on the reading that the backends chunk
// internally — teiembed does (against the limit it probes from /info), and
// ollama does NOT: `ollama.Embed` marshals every input into one JSON body and
// issues one HTTP request. So on an Ollama-backed palace the batch size was
// however many entities the palace had accumulated.
//
// 32 rather than a larger number, and the figure is measured rather than
// chosen: `--embed-timeout`'s own usage records "121s for a batch of 64 on a
// CPU-only host", against a five-minute default budget for ONE call. Halving
// that batch keeps a chunk comfortably inside the budget on the slowest host
// this project runs on, and it matches teiembed's own maxBatch fallback, so the
// two backends now behave alike here instead of only one of them being safe.
//
// The ratio argument survives the bound: a 5,000-entity palace goes from 5,000
// round trips to 157, not from 5,000 to 1. What it stops being is a single
// request that cannot finish — which on this path fails silently, because
// RecomputeGraph logs the error and reports EntityLabelsIndexed as 0.
const entityLabelBatch = 32

// BackfillEntityLabels indexes every entity the graph already holds.
//
// Idempotent: Upsert replaces by id, so running it twice is a no-op rather than a
// duplicate. It is a method rather than a migration because embedding needs a
// live embedder, which a SQL migration does not have.
//
// ⚠ IT EMBEDS IN ONE BATCH, NOT ONE CALL PER LABEL, AND THAT IS THE WHOLE COST.
// This loop used to call IndexEntityLabel per row, which is one EmbedOne round
// trip and one Upsert each. `Embed(ctx, []string)` has been on the Embedder
// interface the entire time and both backends implement it — teiembed's even
// chunks internally against the limit it probes from /info — so the batch
// primitive existed and this call site did not select it. That is this
// repository's named defect wearing a performance costume.
//
// The saving is a RATIO, not a duration: N round trips become one, on whatever
// hardware. Measured on the local stack the whole recompute is embedder-bound,
// but the seconds there are a fact about a CPU-only Ollama and bge-m3, not about
// this code — see issue #366, where a latency was briefly mistaken for a
// property of the tool.
//
// One behaviour change, stated because it is not free: a single embedding
// failure now fails the whole backfill rather than returning the labels indexed
// before it. The caller (RecomputeGraph) already treats this as non-fatal and
// logs, and a half-filled label index that reports a count is worse than one
// that says it did not run.
func (s *Service) BackfillEntityLabels(ctx context.Context, teamID string) (int, error) {
	rows, err := s.writer.AllKGEntities(ctx, teamID)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(rows))
	labels := make([]string, 0, len(rows))
	for _, r := range rows {
		// Structural entities are skipped. attachDerivedEdge creates a room node
		// and an entity for the drawer id itself, and neither is something a
		// QUESTION is ever about — but both would compete for the five nearest
		// label slots, and factsFor then discards every derived fact anyway. So
		// indexing them costs slots and returns nothing.
		//
		// The empty-field skip is a SEPARATE reason and is new here: the per-label
		// path returned nil for an empty id or label, so such a row was never
		// embedded and never counted. Batching would otherwise send "" to the
		// embedder and index a vector for the empty string. Skipping keeps the
		// old outcome; it changes the returned count only for a corpus that holds
		// such a row, and none is known to.
		if isStructuralEntity(r.Name) || r.ID == "" || r.Name == "" {
			continue
		}
		ids = append(ids, r.ID)
		labels = append(labels, r.Name)
	}
	if len(labels) == 0 {
		return 0, nil
	}
	ns := entityNamespace(teamID)
	indexed := 0
	for start := 0; start < len(labels); start += entityLabelBatch {
		end := min(start+entityLabelBatch, len(labels))
		vecs, err := s.embed.Embed(ctx, labels[start:end])
		if err != nil {
			return 0, fmt.Errorf("embed entity labels: %w", err)
		}
		// The embedder's contract is "one vector per input, in order". Checked
		// rather than trusted: a short return would otherwise pair label i with
		// entity i and mislabel every entity after the gap, silently.
		if len(vecs) != end-start {
			return 0, fmt.Errorf("embed entity labels: got %d vectors for %d labels", len(vecs), end-start)
		}
		if err := s.vectors.EnsureNamespace(ctx, ns, len(vecs[0])); err != nil {
			return 0, err
		}
		points := make([]store.Point, 0, len(vecs))
		for i, v := range vecs {
			points = append(points, store.Point{
				ID:      ids[start+i],
				Vector:  v,
				Payload: map[string]any{"label": labels[start+i]},
			})
		}
		if err := s.vectors.Upsert(ctx, ns, points); err != nil {
			return 0, err
		}
		indexed += len(points)
	}
	return indexed, nil
}

// isStructuralEntity reports whether an entity exists to hold the graph together
// rather than to be asked about: a room node, or a drawer id promoted to an
// entity so an edge could name it.
func isStructuralEntity(name string) bool {
	if strings.HasPrefix(name, "room:") {
		return true
	}
	// A drawer id is 64 lowercase hex characters. A real entity label is not.
	if len(name) == 64 {
		for _, c := range name {
			if !strings.ContainsRune("0123456789abcdef", c) {
				return false
			}
		}
		return true
	}
	return false
}

// entityMatches returns the KG entities whose labels are nearest the query.
func (s *Service) entityMatches(ctx context.Context, teamID string, vec []float32, k int) ([]store.Hit, error) {
	if k <= 0 {
		return nil, nil
	}
	res, err := s.vectors.Search(ctx, entityNamespace(teamID), vec, k, nil)
	if err != nil {
		// A missing namespace is "no entities indexed yet", not a failure. Every
		// palace is in that state until the first backfill runs, and refusing the
		// whole recall for it would make the feature impossible to roll out.
		return nil, nil
	}
	// res.StaleIndex is deliberately dropped here rather than propagated. A
	// degraded index is reported on the RECALL that served the hits (ADR-033);
	// this lookup only decides which entity labels a question is near, and a
	// second staleness signal from it would mark a page stale for a reason the
	// reader cannot act on.
	return res.H, nil
}
