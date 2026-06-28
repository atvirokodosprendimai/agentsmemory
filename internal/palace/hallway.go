package palace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Hallways are derived, never authored: this file computes them from the entities
// already stamped on a wing's drawers. Two entities that co-occur in at least
// hallwayMinCount drawers of a wing form a hallway, recording how often and in
// which rooms they met. Ported from the frozen hallways.compute_hallways_for_wing.

// hallwayMinCount is the co-occurrence floor: a pair must share at least this many
// drawers in a wing to be a hallway (frozen min_count default).
const hallwayMinCount = 2

// entityPair is an unordered pair of entity names, always held sorted (a <= b) so
// A↔B and B↔A are the same key.
type entityPair struct{ a, b string }

// hallwayID is a hallway's stable, symmetric identity: the sorted pair hashed with
// the wing, matching the frozen scheme "hallway_{wing}_{a}_{b}_{sha8}". The id is
// team-scoped by the row's composite primary key, so it need not carry the team.
func hallwayID(wing, a, b string) string {
	if a > b {
		a, b = b, a
	}
	sum := sha256.Sum256([]byte(wing + "::" + a + "::" + b))
	return fmt.Sprintf("hallway_%s_%s_%s_%s", wing, a, b, hex.EncodeToString(sum[:])[:8])
}

// computeHallwaysForWing derives a wing's hallways from its drawers' entities. It
// counts every unordered entity pair that co-occurs in a drawer and the rooms they
// met in, keeps pairs at or above the threshold, and preserves the L7 dynamics of
// any matching prior hallway so a recompute does not reset a connection's history.
// The returned hallways are sorted for determinism. now stamps newly created ones.
func (s *Service) computeHallwaysForWing(ctx context.Context, teamID, wing, now string) ([]Hallway, error) {
	drawers, err := s.repo.DrawersForHallways(ctx, teamID, wing)
	if err != nil {
		return nil, fmt.Errorf("load wing drawers: %w", err)
	}

	counts := map[entityPair]int{}
	rooms := map[entityPair]map[string]struct{}{}
	for _, d := range drawers {
		ents := dedupePreserve(d.Entities)
		if len(ents) < 2 {
			continue
		}
		for i := 0; i < len(ents); i++ {
			for j := i + 1; j < len(ents); j++ {
				a, b := ents[i], ents[j]
				if a == b {
					continue
				}
				if a > b {
					a, b = b, a
				}
				k := entityPair{a, b}
				counts[k]++
				if d.Room != "" {
					if rooms[k] == nil {
						rooms[k] = map[string]struct{}{}
					}
					rooms[k][d.Room] = struct{}{}
				}
			}
		}
	}

	// Preserve dynamics across recompute: a pair that was already a hallway keeps
	// its strength/stability/etc. rather than being reset to the initial values.
	existing, err := s.repo.ListHallways(ctx, teamID, wing)
	if err != nil {
		return nil, err
	}
	priorDyn := make(map[entityPair]Dynamics, len(existing))
	for _, h := range existing {
		a, b := h.EntityA, h.EntityB
		if a > b {
			a, b = b, a
		}
		priorDyn[entityPair{a, b}] = h.Dynamics
	}

	out := make([]Hallway, 0, len(counts))
	for k, c := range counts {
		if c < hallwayMinCount {
			continue
		}
		rs := sortedSet(rooms[k])
		dyn, ok := priorDyn[k]
		if !ok {
			dyn = initDynamics(now)
		}
		out = append(out, Hallway{
			ID: hallwayID(wing, k.a, k.b), TeamID: teamID, Wing: wing,
			EntityA: k.a, EntityB: k.b, CoOccurrence: c, Rooms: rs,
			Label:     fmt.Sprintf("%s ↔ %s (co-occur in %d drawers across %d rooms: %s)", k.a, k.b, c, len(rs), strings.Join(rs, ", ")),
			CreatedAt: now, CreatedBy: "auto", Dynamics: dyn,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EntityA != out[j].EntityA {
			return out[i].EntityA < out[j].EntityA
		}
		return out[i].EntityB < out[j].EntityB
	})
	return out, nil
}

// ListHallways returns a team's hallways (optionally one wing), strongest first.
func (s *Service) ListHallways(ctx context.Context, teamID, wing string) ([]Hallway, error) {
	return s.repo.ListHallways(ctx, teamID, wing)
}

// DeleteHallway removes a hallway by id, reporting whether it existed.
func (s *Service) DeleteHallway(ctx context.Context, teamID, id string) (bool, error) {
	return s.repo.DeleteHallway(ctx, teamID, id)
}

// dedupePreserve removes duplicate entities while keeping first-seen order, so the
// pair-counting sees each entity once per drawer (frozen _parse_entities dedup).
func dedupePreserve(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it == "" {
			continue
		}
		if _, dup := seen[it]; dup {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}

// sortedSet returns a set's members as a sorted slice (the rooms a pair met in).
func sortedSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
