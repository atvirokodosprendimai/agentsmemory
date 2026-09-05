package palace

import (
	"context"
	"testing"
)

// The class this file governs was enumerated, not recalled, and the command is
// kept beside its one known miss (ADR-055 T1):
//
//	grep -rn 'room != \x27\x27\|DISTINCT room\|Group("wing, room")\|COUNT(DISTINCT room)\|Select("room' --include='*.go' internal cmd | grep -v _test
//
// Measured 2026-09-04 at 8c8945f: Repo.Wings and Repo.Rooms (both filtered on
// valid_to = '') and Repo.GraphRoomWings (unfiltered — the source of
// GraphStats' count). A fifth hit, Repo.DrawersForHallways, neither lists nor
// counts rooms and is deferred in BACKLOG.md. The first draft's pattern missed
// the member that mattered, which is why the pattern is recorded with its miss.

// TestEveryRoomListingAgreesOnARetractedRoom: a room whose memories are all ended
// is absent from every surface that lists or counts rooms. Measured 2026-09-04:
// am_list_rooms said one room, am_graph_stats said two, in the same minute —
// GraphStats read every row while the listings read live ones.
func TestEveryRoomListingAgreesOnARetractedRoom(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	const team = "t-roomlife"
	if _, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the ledger service owns invoice numbering"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	typo, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisons", Content: "filed into a mistyped room"})
	if err != nil {
		t.Fatalf("add typo: %v", err)
	}
	if err := svc.InvalidateDrawer(ctx, team, typo.Drawers[0].ID, "mistyped room; refiled"); err != nil {
		t.Fatalf("retract: %v", err)
	}

	rooms, err := svc.Rooms(ctx, team, "wing_acme")
	if err != nil {
		t.Fatalf("rooms: %v", err)
	}
	if len(rooms) != 1 || rooms[0].Room != "decisions" {
		t.Errorf("Rooms lists %+v; a room with no live memory must be absent", rooms)
	}

	wings, err := svc.Wings(ctx, team)
	if err != nil {
		t.Fatalf("wings: %v", err)
	}
	if len(wings) != 1 || wings[0].Rooms != 1 {
		t.Errorf("Wings reports %+v; wing_acme must count one room", wings)
	}

	stats, err := svc.GraphStats(ctx, team)
	if err != nil {
		t.Fatalf("graph stats: %v", err)
	}
	if stats.TotalRooms != 1 || stats.RoomsPerWing["wing_acme"] != 1 {
		t.Errorf("GraphStats counts total=%d per-wing=%v; the retracted room is still counted", stats.TotalRooms, stats.RoomsPerWing)
	}
}
