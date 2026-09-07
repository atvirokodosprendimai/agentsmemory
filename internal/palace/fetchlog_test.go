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

// TestTheFetchRatioNamesItsPopulation is ADR-028 T4's whole claim (ADR-007's
// rule applied to this table): a fetch rate means nothing without the ranking
// profile that produced it and the denominator it was divided by.
//
// The three failures it pins are the three ways this number goes wrong, and none
// of them is caught by a test that only checks the arithmetic. Two profiles
// averaged together produce a figure that describes no configuration anyone ran.
// A NULL profile — every row written before the migration — counted as a group
// mints a phantom ranking nobody can identify. And a window with no logged
// recall reporting 0% says "nothing was fetched" when the truth is "nothing was
// measured"; ADR-028's own deferral is that those two must never render alike.
func TestTheFetchRatioNamesItsPopulation(t *testing.T) {
	ctx := context.Background()
	const team = "t-fetchratio"
	svc := newTestService(t)

	// An empty window reports NO rate rather than a zero one. Asserted first
	// because every later zero is read against this: an instrument that reports
	// 0% on an empty window cannot be told from one reporting 0% on a real miss.
	if rates, err := svc.FetchRatesByProfile(ctx, team, time.Hour); err != nil {
		t.Fatalf("rates: %v", err)
	} else if len(rates) != 0 {
		t.Fatalf("a window holding no logged recall must report NO rate, not a zero one; got %+v", rates)
	}

	const profileA = "fusion=rrf lex-weight=n/a rerank=on"
	const profileB = "fusion=linear lex-weight=0.50 rerank=off"

	// recordSearch rather than Search, because the population is the point: this
	// test needs two profiles in one window, and a service instance runs under
	// exactly one. The live write path is covered by the last subtest.
	seed := func(profile string) string {
		id := randomID()
		p := profile
		svc.repo.recordSearch(ctx, searchEventRow{
			ID: id, TeamID: team, Wing: "wing_acme",
			Query: "a recall taken under a named profile", Hits: 3, ProfileID: &p,
		})
		return id
	}
	a1, _ := seed(profileA), seed(profileA)
	b1 := seed(profileB)

	res, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "a memory worth fetching twice"})
	if err != nil {
		t.Fatalf("seed drawer: %v", err)
	}
	drawerID := res.Drawers[0].ID

	// TWO clicks on ONE page. The numerator counts recalls that were fetched, not
	// fetches, so this must move profile A by one — the distinction that decides
	// whether the rate can ever exceed 1.
	svc.RecordFetch(ctx, team, a1, drawerID, false)
	svc.RecordFetch(ctx, team, a1, drawerID, true)
	svc.RecordFetch(ctx, team, b1, drawerID, false)

	rates, err := svc.FetchRatesByProfile(ctx, team, time.Hour)
	if err != nil {
		t.Fatalf("rates: %v", err)
	}
	if len(rates) != 2 {
		t.Fatalf("two profiles ran in this window and must not collapse into one number; got %+v", rates)
	}
	byProfile := make(map[string]FetchRate, len(rates))
	for _, r := range rates {
		byProfile[r.ProfileID] = r
	}
	// ★ This is step 5's canary as well as the arithmetic: a POSITIVE rate from a
	// real fetch. Every zero below is only readable because this line has been
	// seen to move.
	if got := byProfile[profileA]; got.RecallsLogged != 2 || got.RecallsFetched != 1 || got.Rate() != 0.5 {
		t.Errorf("profile A: two logged recalls, one of them fetched twice = 2/1 at 0.5; got %d/%d at %v",
			got.RecallsLogged, got.RecallsFetched, got.Rate())
	}
	if got := byProfile[profileB]; got.RecallsLogged != 1 || got.RecallsFetched != 1 || got.Rate() != 1 {
		t.Errorf("profile B: one logged recall, fetched = 1/1 at 1; got %d/%d at %v",
			got.RecallsLogged, got.RecallsFetched, got.Rate())
	}

	t.Run("aRowWithNoProfileIsExcludedRatherThanCountedAsOne", func(t *testing.T) {
		// Every row written before the migration carries NULL. Folding those into a
		// real profile inflates its denominator with recalls taken under a ranking
		// nobody can name; making them their own group publishes a profile whose
		// id is the empty string. The honest answer is that they are not evidence
		// about any profile and are excluded from the report entirely.
		svc.repo.recordSearch(ctx, searchEventRow{
			ID: randomID(), TeamID: team, Wing: "wing_acme",
			Query: "a recall from before the column existed", Hits: 1,
		})
		rates, err := svc.FetchRatesByProfile(ctx, team, time.Hour)
		if err != nil {
			t.Fatalf("rates: %v", err)
		}
		if len(rates) != 2 {
			t.Fatalf("a NULL profile must not become a third group; got %+v", rates)
		}
		for _, r := range rates {
			if r.ProfileID == profileA && r.RecallsLogged != 2 {
				t.Errorf("a NULL-profile row was folded into profile A's denominator: %d, want 2", r.RecallsLogged)
			}
		}
	})

	t.Run("theDenominatorIsRecallsThatWereLogged", func(t *testing.T) {
		// SkipTelemetry writes no search_events row at all, which is what makes the
		// denominator quotable: the population is recalls this palace RECORDED,
		// never recalls that happened. The eval runs thousands of these, so a
		// denominator counting them would move with sweeps nobody was measuring.
		before, err := svc.FetchRatesByProfile(ctx, team, time.Hour)
		if err != nil {
			t.Fatalf("rates: %v", err)
		}
		if _, err := svc.Search(ctx, team, SearchQuery{
			Query: "a memory worth fetching twice", Wing: "wing_acme", SkipTelemetry: true,
		}); err != nil {
			t.Fatalf("search: %v", err)
		}
		after, err := svc.FetchRatesByProfile(ctx, team, time.Hour)
		if err != nil {
			t.Fatalf("rates: %v", err)
		}
		if len(after) != len(before) {
			t.Fatalf("an unlogged recall must not mint a profile group: %d groups became %d", len(before), len(after))
		}
		for i := range before {
			if before[i].RecallsLogged != after[i].RecallsLogged {
				t.Errorf("an unlogged recall entered the denominator of %q: %d became %d",
					before[i].ProfileID, before[i].RecallsLogged, after[i].RecallsLogged)
			}
		}
	})

	t.Run("isScopedToItsTeam", func(t *testing.T) {
		// The rate is read per team; a query without the filter would report
		// another tenant's reading as this one's ranking evidence.
		if rates, err := svc.FetchRatesByProfile(ctx, "t-someone-else", time.Hour); err != nil {
			t.Fatalf("rates: %v", err)
		} else if len(rates) != 0 {
			t.Fatalf("another team must see none of these recalls, got %+v", rates)
		}
	})

	t.Run("theSearchPathStampsTheProfileItRanUnder", func(t *testing.T) {
		// Without this the column is a field nothing assigns — the defect
		// AGENTS.md §Reachability keeps recording — and the aggregate would go on
		// returning an honest-looking empty list for ever, because every row it
		// could have grouped carries NULL.
		//
		// ⚠ Its OWN team, and not because of tidiness. `created_at` is RFC3339 at
		// SECOND precision, so every row this test writes shares a timestamp and
		// `lastSearchEvent` falls through to its `id DESC` tiebreak — a random hex
		// id. Read against the shared team it returned a SEEDED fixture row and
		// reported the wrong profile with total confidence. A team holding exactly
		// one event has nothing to tie-break.
		const fresh = "t-fetchratio-livepath"
		if _, err := svc.Search(ctx, fresh, SearchQuery{
			Query: "a query nothing in this team answers", Wing: "wing_acme",
		}); err != nil {
			t.Fatalf("search: %v", err)
		}
		row := lastSearchEvent(t, svc, fresh)
		if row.ProfileID == nil {
			t.Fatal("Search recorded a recall with no profile; the rate this task publishes would have nothing to group by")
		}
		if *row.ProfileID != svc.RankingProfile() {
			t.Fatalf("Search stamped %q; want the profile it actually ran under, %q", *row.ProfileID, svc.RankingProfile())
		}
	})
}
