package palace

import (
	"context"
	"testing"
	"time"
)

// TestAFetchIsRecordedAgainstTheRecallThatSentIt pins the recorder itself
// (ADR-028 T3). The half that proves something SELECTS it — that the served
// am_get_drawer actually calls it — lives in internal/mcptest, because a
// recorder with no caller is this repository's most-shipped defect and a test in
// this package cannot see the call site.
func TestAFetchIsRecordedAgainstTheRecallThatSentIt(t *testing.T) {
	ctx := context.Background()
	const team = "t-fetchlog"
	svc := newTestService(t)

	res, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "a memory worth fetching"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	sid := randomID()

	svc.RecordFetch(ctx, team, sid, res.Drawers[0].ID, false)
	fetches, recalls, err := svc.CountFetches(ctx, team, time.Hour)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if fetches != 1 || recalls != 1 {
		t.Fatalf("one fetch naming one recall should count 1/1, got %d/%d", fetches, recalls)
	}

	// Two fetches from ONE recall are two clicks on one page, so the distinct
	// recall count must not move. This is the shape a ratio would get wrong.
	other, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "a second memory on the same page"})
	if err != nil {
		t.Fatalf("seed second: %v", err)
	}
	svc.RecordFetch(ctx, team, sid, other.Drawers[0].ID, true)
	if fetches, recalls, err = svc.CountFetches(ctx, team, time.Hour); err != nil {
		t.Fatalf("count: %v", err)
	} else if fetches != 2 || recalls != 1 {
		t.Fatalf("two fetches from one recall should count 2 fetches / 1 recall, got %d/%d", fetches, recalls)
	}

	t.Run("refusesWhatWouldPolluteTheJoin", func(t *testing.T) {
		before, _, err := svc.CountFetches(ctx, team, time.Hour)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		// Each of these is a way the join could acquire a row that means nothing.
		// A shape check is all that is available — an id for a recall that never
		// happened is a client bug worth seeing, an arbitrary string is a leak.
		svc.RecordFetch(ctx, team, "", res.Drawers[0].ID, false)                // no recall named
		svc.RecordFetch(ctx, team, "NOT-A-SEARCH-ID", res.Drawers[0].ID, false) // wrong shape
		svc.RecordFetch(ctx, team, sid, "", false)                              // no drawer returned
		svc.RecordFetch(ctx, "", sid, res.Drawers[0].ID, false)                 // no tenant
		after, _, err := svc.CountFetches(ctx, team, time.Hour)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if after != before {
			t.Fatalf("malformed fetches must record nothing: %d rows became %d", before, after)
		}
	})

	t.Run("isScopedToItsTeam", func(t *testing.T) {
		// Not a formality: the count is read per team, and a table without the
		// filter would report another tenant's reading as this one's.
		if fetches, _, err := svc.CountFetches(ctx, "t-someone-else", time.Hour); err != nil {
			t.Fatalf("count: %v", err)
		} else if fetches != 0 {
			t.Fatalf("another team must see none of these fetches, got %d", fetches)
		}
	})

	t.Run("theWindowExcludesWhatIsOlderThanIt", func(t *testing.T) {
		// ⚠ The row is dated explicitly rather than by waiting, because
		// `created_at` is RFC3339 at SECOND precision — the same format
		// `search_events` uses — so no sub-second window can exclude a row
		// written in the same second. A first draft of this subtest asked for a
		// one-nanosecond window and failed against correct code, which is the
		// window's real granularity showing up as a test failure rather than as
		// documentation. Anything reading these counts gets second resolution.
		svc.repo.recordFetch(ctx, drawerFetchRow{
			TeamID: team, SearchID: randomID(), DrawerID: res.Drawers[0].ID,
			CreatedAt: time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339),
		})
		if fetches, _, err := svc.CountFetches(ctx, team, 24*time.Hour); err != nil {
			t.Fatalf("count: %v", err)
		} else if fetches != 2 {
			t.Fatalf("a 48h-old fetch must fall outside a 24h window: want the 2 recent ones, got %d", fetches)
		}
		if fetches, _, err := svc.CountFetches(ctx, team, 72*time.Hour); err != nil {
			t.Fatalf("count: %v", err)
		} else if fetches != 3 {
			t.Fatalf("a 72h window must include it: want 3, got %d", fetches)
		}
	})
}
