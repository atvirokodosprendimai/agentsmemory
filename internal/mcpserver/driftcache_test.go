package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// countingFetch returns a fetch that counts its own invocations, so a test can
// assert how many times the O(N) coverage audit actually ran.
func countingFetch(counter *atomic.Int64, report palace.DriftReport, err error) driftFetch {
	return func(context.Context, string) (palace.DriftReport, error) {
		counter.Add(1)
		return report, err
	}
}

// TestDriftCacheServesWithinTTL: two status calls inside the TTL must run the
// coverage audit once. am_status is the call the protocol mandates first for
// every session, so the O(N) two-sided audit must not be re-run for each of
// them — the whole point of the cache.
func TestDriftCacheServesWithinTTL(t *testing.T) {
	var calls atomic.Int64
	dc := newDriftCache(countingFetch(&calls, palace.DriftReport{}, nil), time.Minute)

	r1, err1 := dc.get(context.Background(), "team-a")
	r2, err2 := dc.get(context.Background(), "team-a")
	if err1 != nil || err2 != nil {
		t.Fatalf("get errors: %v, %v", err1, err2)
	}
	if calls.Load() != 1 {
		t.Errorf("two gets inside the TTL ran the audit %d times; want 1", calls.Load())
	}
	if r1.Coverage() != r2.Coverage() {
		t.Errorf("the cached report changed between two gets inside the TTL: %v vs %v", r1.Coverage(), r2.Coverage())
	}
}

// TestDriftCacheRefreshesAfterTTL: past the TTL a status call re-runs the
// audit, so a palace that falls behind is reported as such within one TTL.
func TestDriftCacheRefreshesAfterTTL(t *testing.T) {
	var calls atomic.Int64
	dc := newDriftCache(countingFetch(&calls, palace.DriftReport{}, nil), time.Millisecond)

	if _, err := dc.get(context.Background(), "team-a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := dc.get(context.Background(), "team-a"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("gets across the TTL ran the audit %d times; want 2", calls.Load())
	}
}

// TestDriftCacheSeparatesTeams: the cache is per-team, so one team's status
// cannot consume another team's report.
func TestDriftCacheSeparatesTeams(t *testing.T) {
	var calls atomic.Int64
	dc := newDriftCache(countingFetch(&calls, palace.DriftReport{}, nil), time.Minute)

	if _, err := dc.get(context.Background(), "team-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := dc.get(context.Background(), "team-b"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("two teams got %d audits; want one per team", calls.Load())
	}
}

// TestDriftCacheErrorsAreNotCached: a failed audit is not a fact about the
// palace, so the next call retries it rather than serving a stale failure.
func TestDriftCacheErrorsAreNotCached(t *testing.T) {
	var calls atomic.Int64
	dc := newDriftCache(countingFetch(&calls, palace.DriftReport{}, errors.New("audit failed")), time.Minute)

	if _, err := dc.get(context.Background(), "team-a"); err == nil {
		t.Fatal("a failed audit must surface as an error")
	}
	if _, err := dc.get(context.Background(), "team-a"); err == nil {
		t.Fatal("a failed audit must surface as an error on retry")
	}
	if calls.Load() != 2 {
		t.Errorf("a cached failure would have been served; the audit ran %d times, want 2", calls.Load())
	}
}

// TestStatusRoutesThroughDriftCache pins the SELECTION: the cache existing and
// registerStatus calling drawers.IndexDrift directly are both green — the
// audit only stops being O(N)-per-status when the handler actually routes
// through the cache. Same reachability rule as TestStatusResponseCarriesTheInbox.
func TestStatusRoutesThroughDriftCache(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "newDriftCache(") {
		t.Error("registerStatus never constructs the drift cache, so every am_status re-runs the O(N) audit")
	}
	if !strings.Contains(body, ".get(ctx") && !strings.Contains(body, ".get(") {
		t.Error("registerStatus never consults the drift cache, so the cache exists and nothing uses it")
	}
	i := strings.Index(body, "func registerStatus")
	if i < 0 {
		t.Fatal("registerStatus moved")
	}
	if strings.Contains(body[i:], "drawers.IndexDrift(ctx, t.TeamID") {
		t.Error("registerStatus still calls drawers.IndexDrift directly — the audit runs on every status call")
	}
}

// TestDriftCacheEvictsOldestWhenFull: the per-team map must stay bounded on a
// multi-tenant server. Each entry is small (a report of up to driftSample
// drifted points), but nothing ever removed one, so a tenant that stopped
// calling status held its slot forever (review round 2, R2-6). The cap must
// hold even when every entry is fresh — no TTL expiry to lean on — and the
// evicted team is the least-recently-refreshed one.
func TestDriftCacheEvictsOldestWhenFull(t *testing.T) {
	dc := newDriftCache(func(_ context.Context, teamID string) (palace.DriftReport, error) {
		return palace.DriftReport{}, nil
	}, time.Hour)

	for i := 0; i < maxDriftTeams+1; i++ {
		if _, err := dc.get(context.Background(), fmt.Sprintf("team-%03d", i)); err != nil {
			t.Fatalf("get team-%03d: %v", i, err)
		}
	}
	if n := len(dc.perTeam); n > maxDriftTeams {
		t.Fatalf("cache holds %d teams; cap is %d", n, maxDriftTeams)
	}

	// The evicted team is the least-recently-refreshed one, deterministically:
	// with all refreshes landing within the same clock tick the tie breaks on
	// the smallest team ID, which is also the oldest refresh order here.
	dc.mu.Lock()
	_, survived := dc.perTeam["team-000"]
	dc.mu.Unlock()
	if survived {
		t.Error("team-000 (the least-recently-refreshed) survived the cap — eviction did not pick the oldest")
	}

	// A fresh get re-adds it (evicting someone else); the cache is a cache, not
	// a tombstone for teams that merely aged out.
	if _, err := dc.get(context.Background(), "team-000"); err != nil {
		t.Fatalf("re-get team-000: %v", err)
	}
	dc.mu.Lock()
	_, back := dc.perTeam["team-000"]
	dc.mu.Unlock()
	if !back {
		t.Error("re-added team-000 is not in the cache")
	}
}
