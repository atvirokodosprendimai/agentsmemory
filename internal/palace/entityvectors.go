package palace

import (
	"context"
	"fmt"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// entityNamespace holds embedded KG entity LABELS, separate from the drawer
// vectors.
//
// Separate rather than mixed, because the two answer different questions: a
// drawer vector says "this passage is about X", an entity vector says "this NODE
// is called X". Mixing them into one namespace would put nodes and passages in
// competition for the same k slots, and a scoped drawer search would start
// returning entities it cannot render.
func entityNamespace(teamID string) string { return teamID + "::kg_entities" }

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

// BackfillEntityLabels indexes every entity the graph already holds.
//
// Idempotent: Upsert replaces by id, so running it twice is a no-op rather than a
// duplicate. It is a method rather than a migration because embedding needs a
// live embedder, which a SQL migration does not have.
func (s *Service) BackfillEntityLabels(ctx context.Context, teamID string) (int, error) {
	rows, err := s.repo.AllKGEntities(ctx, teamID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		// Structural entities are skipped. attachDerivedEdge creates a room node
		// and an entity for the drawer id itself, and neither is something a
		// QUESTION is ever about — but both would compete for the five nearest
		// label slots, and factsFor then discards every derived fact anyway. So
		// indexing them costs slots and returns nothing.
		if isStructuralEntity(r.Name) {
			continue
		}
		if err := s.IndexEntityLabel(ctx, teamID, r.ID, r.Name); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
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
