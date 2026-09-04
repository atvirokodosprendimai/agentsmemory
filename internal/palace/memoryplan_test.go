package palace

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestMemoryChunkLookupSeeksRatherThanScansTheTenant is a gate on an access
// path, not on a result.
//
// ADR-024 put MemoryChunksByRoots on every recall, twice, in BOTH arms. Written
// as `id IN (...) OR parent_id IN (...)` it examined every drawer the tenant
// owned, because no planner seeks both sides of a disjunction in one index pass
// — and adding the parent_id index alone does NOT change that plan, which is
// why the fix is a union and an index together.
//
// Asserting on returned chunks cannot see any of this: the OR spelling returns
// exactly the same rows, correctly, while reading the whole table. So this reads
// the plan. Reverting either half — the UNION in memoryChunkQuery, or migration
// 00024 — puts `SCAN drawers` back and turns this red.
func TestMemoryChunkLookupSeeksRatherThanScansTheTenant(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// A real memory long enough to be stored as several chunks, so the plan is
	// resolved against a table that actually holds roots and children.
	added, err := svc.Add(ctx, "team-alpha", AddInput{
		Wing: "wing_alpha", Room: "decisions",
		Content: strings.Repeat("a memory long enough to be chunked into siblings ", 80),
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("content produced %d chunks; the fixture needs a root and children", len(added.Drawers))
	}
	root := added.Drawers[0].ID

	for _, columns := range []memoryChunkColumns{allDrawerColumns, chunkIdentityColumns} {
		plan := memoryChunkQueryPlan(t, svc, ctx, "team-alpha", []string{root}, columns)
		if strings.Contains(strings.ToUpper(plan), "SCAN DRAWERS") {
			t.Fatalf("memory chunk lookup (columns %q) scans the tenant's drawers instead of seeking:\n%s", columns.sql(), plan)
		}
		// Assert on the CONSTRAINED COLUMNS, not on the index name.
		//
		// This is the whole trap: with migration 00024 applied, the old OR
		// spelling produces `SEARCH drawers USING INDEX idx_drawers_team_parent
		// (team_id=?)`. It names the new index and contains no "SCAN", yet
		// constrains team_id ALONE — every row of the tenant, which is the
		// defect. Only the seek columns tell the two apart.
		for _, seek := range []string{"AND id=?", "AND parent_id=?"} {
			if !strings.Contains(plan, seek) {
				t.Fatalf("memory chunk lookup (columns %q) has no %q seek, so it is reading more than the requested roots:\n%s",
					columns.sql(), seek, plan)
			}
		}
	}
}

// TestAnchorChunkLookupDoesNotLoadContent pins the projection, because nothing
// about the RESULT can.
//
// Anchor resolution needs a list of chunk ids. Asking for whole memories
// produces exactly the same anchors, so a behavioural test passes either way
// while the request drags every chunk's content across on every search — right
// after the caller loaded those same memories in full. The only observable
// difference is the columns asked for, so that is what this asserts.
func TestAnchorChunkLookupDoesNotLoadContent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	added, err := svc.Add(ctx, "team-beta", AddInput{
		Wing: "wing_beta", Room: "decisions",
		Content: strings.Repeat("a memory long enough to be chunked into siblings ", 80),
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	root := added.Drawers[0].ID

	// Same ids as the whole-memory load: the projection must not change WHICH
	// chunks belong to the memory, only how much of each is read.
	ids, err := svc.repo.MemoryChunkIDsByRoots(ctx, "team-beta", []string{root})
	if err != nil {
		t.Fatalf("MemoryChunkIDsByRoots: %v", err)
	}
	full, err := svc.repo.MemoryChunksByRoots(ctx, "team-beta", []string{root})
	if err != nil {
		t.Fatalf("MemoryChunksByRoots: %v", err)
	}
	if len(ids[root]) != len(full[root]) || len(ids[root]) < 2 {
		t.Fatalf("projected lookup returned %d chunks, whole-memory lookup %d (want equal, and >1 so siblings are covered)",
			len(ids[root]), len(full[root]))
	}
	for i, d := range full[root] {
		if ids[root][i] != d.ID {
			t.Fatalf("chunk %d: projected id %q != whole-memory id %q", i, ids[root][i], d.ID)
		}
	}

	// Now the part that matters: what AnchorsForMemories ACTUALLY issues.
	//
	// Asserting that a projected query exists proves nothing about whether the
	// anchor path selects it — that is the "component tested, selection not
	// tested" gap this repo keeps shipping. So the statements are recorded off
	// the real call.
	if _, err := svc.AddAnchors(ctx, "team-beta", root, []AnchorInput{
		{Path: "internal/palace/repo.go", Snippet: "memoryChunkQuery"},
	}); err != nil {
		t.Fatalf("add anchor: %v", err)
	}

	rec := &sqlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
	svc.repo.db = svc.repo.db.Session(&gorm.Session{Logger: rec})
	svc.repo.reader = svc.repo.reader.Session(&gorm.Session{Logger: rec}) // reads run on the reader since ADR-052 T5

	anchors, err := svc.AnchorsForMemories(ctx, "team-beta", []string{root})
	if err != nil {
		t.Fatalf("AnchorsForMemories: %v", err)
	}
	if len(anchors[root]) != 1 {
		t.Fatalf("AnchorsForMemories returned %d anchors for the memory; want the 1 that was filed", len(anchors[root]))
	}
	issued := rec.statements()
	if len(issued) == 0 {
		t.Fatal("no statements recorded; the recorder is not recording and this gate proves nothing")
	}
	for _, sql := range issued {
		// `SELECT *` is the tell. A whole-row read never spells "content", so
		// searching for the column name would pass on exactly the query this
		// test exists to reject.
		if strings.Contains(sql, "SELECT * FROM `drawers`") {
			t.Fatalf("resolving anchors reads whole drawer rows to build a list of ids:\n%s", sql)
		}
	}
}

// sqlRecorder captures every statement GORM issues, so a test can assert on the
// query a code path CHOSE rather than on a query it could have chosen.
type sqlRecorder struct {
	logger.Interface
	mu   sync.Mutex
	sqls []string
}

func (r *sqlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sqls = append(r.sqls, sql)
}

func (r *sqlRecorder) statements() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sqls...)
}

// memoryChunkQueryPlan returns SQLite's plan for the REAL query the repo issues,
// one plan row per line. It renders the query through a dry-run session so the
// statement under test is the shipped one rather than a hand-copied echo of it.
func memoryChunkQueryPlan(t *testing.T, svc *Service, ctx context.Context, teamID string, roots []string, columns memoryChunkColumns) string {
	t.Helper()

	dry := repoOn(svc.repo.db.Session(&gorm.Session{DryRun: true}))
	stmt := dry.memoryChunkQuery(ctx, teamID, roots, columns).Find(&[]drawerRow{}).Statement
	sql := stmt.SQL.String()
	if sql == "" {
		t.Fatal("dry run produced no SQL; the probe is not probing")
	}

	rows, err := svc.repo.db.WithContext(ctx).Raw("EXPLAIN QUERY PLAN "+sql, stmt.Vars...).Rows()
	if err != nil {
		t.Fatalf("explain: %v (sql=%s)", err, sql)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		out = append(out, detail)
	}
	if len(out) == 0 {
		t.Fatalf("EXPLAIN QUERY PLAN returned no rows for %s", sql)
	}
	return strings.Join(out, "\n")
}

// TestAnchorsForMemoriesReturnStableOrder pins ordering that reaches the agent.
//
// AnchorsForMemories assembled its result by ranging two maps, so a memory with
// anchors on several chunks got them in a different order on every call. That
// order is user-visible: internal/mcpserver/drawers.go appends the slice
// straight into the search response, so two identical recalls disagreed. The
// repo already returns chunks in chunk_index order — the map round-trip was
// throwing that away.
func TestAnchorsForMemoriesReturnStableOrder(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	added, err := svc.Add(ctx, "team-order", AddInput{
		Wing: "wing_alpha", Room: "decisions",
		Content: strings.Repeat("a memory long enough to be chunked into several siblings ", 120),
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added.Drawers) < 4 {
		t.Fatalf("fixture produced %d chunks; ordering needs several anchored chunks", len(added.Drawers))
	}
	root := added.Drawers[0].ID

	// One anchor per chunk, so the result has something to shuffle.
	for i, d := range added.Drawers {
		if _, err := svc.AddAnchors(ctx, "team-order", d.ID, []AnchorInput{
			{Path: fmt.Sprintf("internal/palace/file%02d.go", i), Snippet: fmt.Sprintf("marker %02d", i)},
		}); err != nil {
			t.Fatalf("anchor chunk %d: %v", i, err)
		}
	}

	var first []string
	for call := range 40 {
		got, err := svc.AnchorsForMemories(ctx, "team-order", []string{root})
		if err != nil {
			t.Fatalf("AnchorsForMemories: %v", err)
		}
		paths := make([]string, 0, len(got[root]))
		for _, a := range got[root] {
			paths = append(paths, a.Path)
		}
		if len(paths) != len(added.Drawers) {
			t.Fatalf("call %d returned %d anchors; want one per chunk (%d)", call, len(paths), len(added.Drawers))
		}
		if first == nil {
			first = paths
			continue
		}
		if !slices.Equal(paths, first) {
			t.Fatalf("call %d returned anchors in a different order:\n first: %v\n  this: %v", call, first, paths)
		}
	}
}
