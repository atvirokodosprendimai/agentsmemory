package palace

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// seedMemory files one memory of roughly n chunks and returns its root id.
func seedMemory(t *testing.T, svc *Service, team, wing, room, marker string, chunks int) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "%s opening line. ", marker)
	for line := 0; b.Len() < ChunkSize*(chunks-1)+ChunkSize/2; line++ {
		fmt.Fprintf(&b, "%s filler line %d with distinct wording so overlap removal finds the real seam. ",
			marker, line)
	}
	res, err := svc.Add(context.Background(), team, AddInput{Wing: wing, Room: room, Content: b.String()})
	if err != nil {
		t.Fatalf("seed %s: %v", marker, err)
	}
	if len(res.Drawers) < 2 {
		t.Fatalf("seed %s produced %d chunk(s); this test needs a multi-chunk memory to tell "+
			"rows from memories", marker, len(res.Drawers))
	}
	return res.Drawers[0].ID
}

// TestAWakeUpCountCountsLiveMemories is the wake-up surface's arithmetic, and it
// was wrong in two independent ways at once.
//
// Measured 2026-08-29 by a session in another project, using nothing but the
// tools: one room reported EIGHT waiting where two memories were live. Both
// causes are here.
//
//  1. RETRACTED DRAWERS WERE COUNTED. am_list_drawers excludes them and this
//     count did not, so the two disagreed about the same room in the same minute.
//     That is worse than an off-by-two: the protocol asks sessions to close out
//     inbox items so a stale lead is not rediscovered monthly, and the number
//     that greets the next session never fell. A session that dutifully closes
//     items watches the count stay put and concludes closing does nothing.
//
//  2. CHUNKS WERE COUNTED AND CALLED "memories". The count scaled with how long
//     the sender WROTE rather than with how much was waiting, so one thorough
//     four-chunk handoff outranked two short ones. am_search went to real trouble
//     to collapse chunks and report a memory-level unit — am_status's own ranking
//     line says unit=memory — which left the wake-up surface as the one place
//     still speaking in rows.
func TestAWakeUpCountCountsLiveMemories(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing, room = "team-wake", "wing_alpha", "inbox"

	seedMemory(t, svc, team, wing, room, "ALPHA", 3)
	seedMemory(t, svc, team, wing, room, "BETA", 4)
	retracted := seedMemory(t, svc, team, wing, room, "GAMMA", 3)
	if err := svc.InvalidateDrawer(ctx, team, retracted, "closed out: confirmed fixed upstream"); err != nil {
		t.Fatalf("retract: %v", err)
	}

	got, err := svc.InboxCount(ctx, team, wing, room)
	if err != nil {
		t.Fatalf("InboxCount: %v", err)
	}
	if got != 2 {
		var rows int64
		svc.repo.db.Model(&drawerRow{}).Where("team_id = ? AND wing = ? AND room = ?", team, wing, room).Count(&rows)
		t.Errorf("InboxCount = %d, want 2 — two memories are live and a third was retracted; "+
			"the room holds %d rows, so a count of rows would report %d and a count that "+
			"included the retraction would report more still",
			got, rows, rows)
	}

	t.Run("a retraction moves the number", func(t *testing.T) {
		// The closing convention's only feedback signal. If this does not fall, a
		// session that closes an item has no way to tell that closing worked.
		before, err := svc.InboxCount(ctx, team, wing, room)
		if err != nil {
			t.Fatalf("InboxCount: %v", err)
		}
		var live []Drawer
		live, err = svc.repo.CurrentBySource(ctx, team, wing, room, "")
		_ = live
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		id := seedMemory(t, svc, team, wing, room, "DELTA", 2)
		mid, err := svc.InboxCount(ctx, team, wing, room)
		if err != nil {
			t.Fatalf("InboxCount: %v", err)
		}
		if mid != before+1 {
			t.Fatalf("filing one memory moved the count %d -> %d, want +1", before, mid)
		}
		if err := svc.InvalidateDrawer(ctx, team, id, "closed out"); err != nil {
			t.Fatalf("retract: %v", err)
		}
		after, err := svc.InboxCount(ctx, team, wing, room)
		if err != nil {
			t.Fatalf("InboxCount: %v", err)
		}
		if after != before {
			t.Errorf("after retracting the memory just filed, the count is %d, want %d — "+
				"closing an inbox item must move the number that greets the next session, "+
				"or the convention buys nothing at the only place it needed to show up",
				after, before)
		}
	})
}

// TestTheTaxonomyDoesNotCountRetractedDrawers covers the other surface reading
// the same aggregation: am_status's wing/room tree, am_list_wings and
// am_list_rooms all come from Wings and Rooms.
func TestTheTaxonomyDoesNotCountRetractedDrawers(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing, room = "team-tax", "wing_alpha", "decisions"

	seedMemory(t, svc, team, wing, room, "LIVE", 3)
	gone := seedMemory(t, svc, team, wing, room, "GONE", 3)
	if err := svc.InvalidateDrawer(ctx, team, gone, "retracted"); err != nil {
		t.Fatalf("retract: %v", err)
	}

	tax, err := svc.GetTaxonomy(ctx, team)
	if err != nil {
		t.Fatalf("GetTaxonomy: %v", err)
	}
	var drawers, memories int
	for _, w := range tax.Wings {
		if w.Wing != wing {
			continue
		}
		drawers = w.Drawers
		memories = w.Memories
		for _, r := range w.Rooms {
			if r.Room == room && r.Drawers != drawers {
				t.Errorf("room %q reports %d drawers against the wing's %d", r.Room, r.Drawers, drawers)
			}
		}
	}
	var liveRows int64
	svc.repo.db.Model(&drawerRow{}).Where("team_id = ? AND valid_to = ''", team).Count(&liveRows)
	if drawers != int(liveRows) {
		t.Errorf("taxonomy reports %d drawers against %d LIVE rows — a retracted drawer is not "+
			"something a session can read, and am_list_drawers already excludes it, so counting "+
			"it here makes the two surfaces disagree about the same room", drawers, liveRows)
	}
	if memories != 1 {
		t.Errorf("taxonomy reports %d memories, want 1 — the unit am_search reports is the "+
			"memory, and the wake-up surface was the one place still speaking in rows", memories)
	}
}
