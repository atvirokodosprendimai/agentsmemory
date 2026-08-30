package palace

import (
	"context"
	"testing"
)

// The production shape this test is built from: every one of 1,338 hallways
// carried a created_at from the most recent recompute and a last_activated from
// the first derivation, eight days earlier. Real values, measured 2026-08-25.
const (
	firstDerived  = "2026-08-15T17:29:29Z"
	lastRecompute = "2026-08-23T19:23:59Z"
)

// TestRecomputePreservesWhenAHallwayWasFirstDerived fails when a rebuild moves a
// hallway's creation stamp forward.
//
// computeHallwaysForWing preserves the L7 dynamics across a recompute on purpose,
// so a rebuild does not reset a connection's history. It used to re-stamp
// CreatedAt: now in the same breath, and that half-preservation is what made both
// fields lie: created_at came to mean "last rebuilt", last_activated kept meaning
// "first derived", and every hallway in the palace claimed a last activation
// predating its own creation. Anything that ages a connection off that pair reads
// a negative lifetime.
//
// The test seeds the inverted shape directly rather than recomputing twice, and
// that is load-bearing: the stamps are RFC3339 at SECOND precision, so two
// recomputes inside one test would very likely produce the same string and the
// assertion would pass without the fix. A gate that cannot tell the two apart is
// the failure mode this repository keeps shipping.
func TestRecomputePreservesWhenAHallwayWasFirstDerived(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const (
		team = "team-1"
		wing = "alpha"
	)

	mineForGraph(t, svc, team, wing, "db", "Redis", "Postgres")
	if _, err := svc.RecomputeGraph(ctx, team, wing, false); err != nil {
		t.Fatalf("first recompute: %v", err)
	}
	seeded, err := svc.repo.ListHallways(ctx, team, wing)
	if err != nil {
		t.Fatalf("list hallways: %v", err)
	}
	if len(seeded) == 0 {
		t.Fatal("mining two co-occurring entities derived no hallway, so this test proves nothing")
	}

	// Rewrite them into the exact shape production was found in.
	for i := range seeded {
		seeded[i].CreatedAt = lastRecompute
		seeded[i].LastActivated = firstDerived
	}
	if _, err := svc.repo.ReplaceWingHallways(ctx, team, wing, seeded); err != nil {
		t.Fatalf("seed the inverted stamps: %v", err)
	}

	if _, err := svc.RecomputeGraph(ctx, team, wing, false); err != nil {
		t.Fatalf("second recompute: %v", err)
	}
	rebuilt, err := svc.repo.ListHallways(ctx, team, wing)
	if err != nil {
		t.Fatalf("list hallways after rebuild: %v", err)
	}
	if len(rebuilt) != len(seeded) {
		t.Fatalf("rebuild changed the hallway count from %d to %d", len(seeded), len(rebuilt))
	}

	for _, h := range rebuilt {
		if h.CreatedAt == lastRecompute {
			t.Errorf("hallway %s kept created_at=%s, the stamp of a PRIOR rebuild.\n"+
				"  Nothing has ever written LastActivated after initDynamics, so the older\n"+
				"  stamp (%s) is when this hallway was really first derived. A fix that only\n"+
				"  stops re-stamping freezes existing rows at the wrong date instead of\n"+
				"  repairing them — see earliestStamp.", h.ID, h.CreatedAt, firstDerived)
			continue
		}
		if h.CreatedAt != firstDerived {
			t.Errorf("hallway %s has created_at=%s, want the first-derivation stamp %s.\n"+
				"  A recompute must carry the creation date across, not mint a new one.",
				h.ID, h.CreatedAt, firstDerived)
		}
		if h.LastActivated < h.CreatedAt {
			t.Errorf("hallway %s reports last_activated=%s BEFORE created_at=%s, so it was "+
				"activated before it existed and any decay computed from the pair is negative.",
				h.ID, h.LastActivated, h.CreatedAt)
		}
	}
}

// TestEarliestStampNeverRepairsToNoDate pins the trap in picking the older of two
// timestamps by string comparison: "" sorts before every real stamp, so the naive
// minimum turns a row missing one field into a row with no date at all.
func TestEarliestStampNeverRepairsToNoDate(t *testing.T) {
	for _, tc := range []struct{ name, a, b, want string }{
		{"created is older", firstDerived, lastRecompute, firstDerived},
		{"activated is older", lastRecompute, firstDerived, firstDerived},
		{"identical", firstDerived, firstDerived, firstDerived},
		{"missing created", "", firstDerived, firstDerived},
		{"missing activated", firstDerived, "", firstDerived},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := earliestStamp(tc.a, tc.b); got != tc.want {
				t.Errorf("earliestStamp(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
