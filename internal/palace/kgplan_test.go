package palace

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// explainPlan returns SQLite's plan for a statement a repo path actually issues,
// one plan row per line.
//
// build renders the query through a DRY-RUN session, so what gets explained is the
// SHIPPED statement rather than a hand-copied echo of it that drifts the first time
// someone edits the real one. Same technique as memoryChunkQueryPlan.
func explainPlan(t *testing.T, svc *Service, ctx context.Context, build func(*Repo) *gorm.DB) string {
	t.Helper()

	dry := repoOn(svc.repo.db.Session(&gorm.Session{DryRun: true}))
	stmt := build(dry).Find(&[]kgTripleRow{}).Statement
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

// planConstraints returns the columns SQLite said it CONSTRAINED the index on —
// the parenthesised list in "SEARCH t USING INDEX i (team_id=? AND valid_to=?)".
//
// This is the whole point of these gates. `SEARCH … USING INDEX` is not evidence
// that an index did any work: a query that narrows to the tenant and then walks
// every row it owns prints exactly that, so a gate grepping for `SCAN` passes on a
// filter no index ever touched. Only the constraint list says which columns were
// actually used to find rows.
func planConstraints(t *testing.T, plan string) []string {
	t.Helper()
	m := regexp.MustCompile(`\(([^)]*)\)`).FindStringSubmatch(plan)
	if m == nil {
		return nil
	}
	var cols []string
	for _, term := range strings.Split(m[1], " AND ") {
		if i := strings.IndexAny(term, "=<>"); i > 0 {
			cols = append(cols, strings.TrimSpace(term[:i]))
		}
	}
	return cols
}

// seedKGCorpus fills a team with a graph shaped like the real one: many entities,
// a near-unique predicate per fact (the sprawl the corpus actually has), and about
// 3% of facts retracted — the ratio measured on the live graph for ADR-026.
//
// It deliberately does NOT run ANALYZE, because nothing in this repo does. The
// planner these gates read is the one production has: no stats.
func seedKGCorpus(t *testing.T, svc *Service, ctx context.Context, team string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		subj := fmt.Sprintf("entity-%d", i)
		pred := fmt.Sprintf("relates to %d", i)
		obj := fmt.Sprintf("org-%d", i%7)
		if _, err := svc.KGAdd(ctx, team, subj, pred, obj, "2024-01-01", "", "", "", ""); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if i%30 == 0 {
			if _, _, _, err := svc.KGInvalidate(ctx, team, subj, pred, obj, "2025-06-01", "superseded by the plan"); err != nil {
				t.Fatalf("seed invalidate: %v", err)
			}
		}
	}
}

// TestStatusCurrentIsIndexed is ADR-026 T2's gate: the team-wide endedness query
// must be resolved BY valid_to, not merely narrowed to the tenant and walked.
//
// It asserts valid_to is in the plan's CONSTRAINT LIST rather than asserting the
// absence of the word SCAN. Before migration 00025 this query printed
// `SEARCH … USING INDEX idx_kg_triples_team_predicate (team_id=?)` — an index, a
// SEARCH, and a full walk of every row the tenant owns. Drop the index and this
// goes red; a `SCAN`-grepping gate stays green through that and is worthless.
func TestStatusCurrentIsIndexed(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	const team = "team-plan"
	seedKGCorpus(t, svc, ctx, team, 300)

	plan := explainPlan(t, svc, ctx, func(r *Repo) *gorm.DB {
		return r.kgCurrentQuery(ctx, team)
	})
	if cols := planConstraints(t, plan); !slices.Contains(cols, "valid_to") {
		t.Fatalf("the team-wide current query must be constrained on valid_to, got %v\nplan: %s", cols, plan)
	}
}

// TestPredicateOnlyQueryIsIndexed is ADR-026 T5's gate: predicate standing alone
// must be a real entry point, resolved BY the predicate index.
//
// idx_kg_triples_team_predicate has existed since 00010_kg.sql and no query ever
// used it — the schema was built for predicate lookups and the query layer never
// arrived. That is the reverse of this repo's usual defect and just as invisible:
// an index nothing uses costs writes forever and shows up in no test. This asserts
// the entry point is genuinely served rather than being a filter over a tenant
// walk wearing an index's name.
func TestPredicateOnlyQueryIsIndexed(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	const team = "team-plan"
	seedKGCorpus(t, svc, ctx, team, 300)

	for _, status := range []string{KGStatusCurrent, KGStatusEnded, KGStatusAll} {
		plan := explainPlan(t, svc, ctx, func(r *Repo) *gorm.DB {
			return r.kgTripleQuery(ctx, team, kgTripleFilter{column: "predicate", value: "relates_to_5", status: status})
		})
		if cols := planConstraints(t, plan); !slices.Contains(cols, "predicate") {
			t.Errorf("predicate-only at status=%s must be constrained on predicate, got %v\nplan: %s", status, cols, plan)
		}
	}
}

// TestStatusFilterRefinesTheEntryPointRatherThanReplacingIt is the other half of
// T2, and it exists because measuring T2 turned up a trap the ADR did not predict.
//
// ADR-026 §2 measured the STATUS-ONLY shape and generalised the result to "the
// default path, which is the one every agent takes". The default path is not that
// shape — it is an entity lookup, which already had a selective index. Measured
// here with 300 facts and no ANALYZE (production's condition, since nothing runs
// it), adding idx_kg_triples_team_valid_to made the planner resolve
// `team_id AND subject AND valid_to` through VALID_TO:
//
//	SEARCH kg_triples USING INDEX idx_kg_triples_team_valid_to (team_id=? AND valid_to=?)
//
// An empty valid_to matches ~96% of a tenant's rows; a subject matches a handful. So
// the index added to make the default path cheaper made it read almost the whole
// tenant — while printing an index, on the column it filters, the whole time. The
// unary + in kgStatusScope is the fix, and this test is what keeps it there.
func TestStatusFilterRefinesTheEntryPointRatherThanReplacingIt(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	const team = "team-plan"
	seedKGCorpus(t, svc, ctx, team, 300)

	for _, c := range []struct{ column, value string }{
		{"subject", "entity-5"},
		{"object", "org-3"},
		{"predicate", "relates_to_5"},
	} {
		for _, status := range []string{KGStatusCurrent, KGStatusEnded, KGStatusAll} {
			plan := explainPlan(t, svc, ctx, func(r *Repo) *gorm.DB {
				return r.kgTripleQuery(ctx, team, kgTripleFilter{column: c.column, value: c.value, status: status})
			})
			cols := planConstraints(t, plan)
			if !slices.Contains(cols, c.column) {
				t.Errorf("%s at status=%s must be found BY %s, got constraints %v\nplan: %s",
					c.column, status, c.column, cols, plan)
			}
		}
	}
}
