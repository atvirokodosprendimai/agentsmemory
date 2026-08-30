package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/chromemvec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	glebarez "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// reconcileFixture builds a REAL sqlite source of truth and a REAL chromem
// index, loads sotN points into the former and indexN into the latter, and
// runs the boot reconcile against the pair. Two fakes would defeat the point:
// the reconcile's under/over numbers are only honest if both halves count
// exactly, which is exactly what the real backends do.
func reconcileFixture(t *testing.T, sotN, indexN int) (ReconcileReport, error) {
	t.Helper()
	dir := t.TempDir()
	gdb, err := gorm.Open(glebarez.Open(filepath.Join(dir, "sot.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sot := sqlitevec.New(gdb)
	idx, err := chromemvec.New(filepath.Join(dir, "chromem"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}

	ctx := context.Background()
	const ns = "team-local"
	if sotN > 0 {
		if err := sot.Upsert(ctx, ns, fixturePoints(sotN)); err != nil {
			t.Fatalf("seed source of truth: %v", err)
		}
	}
	if indexN > 0 {
		if err := idx.Upsert(ctx, ns, fixturePoints(indexN)); err != nil {
			t.Fatalf("seed index: %v", err)
		}
	}
	return reconcileChromem(ctx, sot, idx, store.NewHybrid(sot, idx))
}

// fixturePoints builds n points whose ids match position, so a 7-of-10 index
// and a 10-point source of truth disagree on exactly the last three.
func fixturePoints(n int) []store.Point {
	out := make([]store.Point, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, store.Point{
			ID:      string(rune('a' + i)),
			Vector:  []float32{float32(i), 0, 0},
			Payload: map[string]any{"wing": "wing_acme"},
		})
	}
	return out
}

// TestReconcileReportsPartialDrift pins the ADR-033 report: an index at 7 of 10
// at boot is not repaired (the empty case is) but is NAMED — the 3 missing
// points were invisible before this report existed.
func TestReconcileReportsPartialDrift(t *testing.T) {
	report, err := reconcileFixture(t, 10, 7)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(report.Rebuilt) != 0 {
		t.Errorf("a partially-behind namespace was rebuilt as if empty: %v", report.Rebuilt)
	}
	if got := report.Under["team-local"]; got != 3 {
		t.Errorf("partial fall-behind reported as %d missing, want 3", got)
	}
	if len(report.Over) != 0 {
		t.Errorf("a behind index reported as over: %v", report.Over)
	}
	if s := report.String(); !strings.Contains(s, "team-local") || !strings.Contains(s, "3 point(s) behind") {
		t.Errorf("the report does not name the namespace and the missing count:\n%s", s)
	}
}

// TestReconcileReportsOverCount pins the over branch: an index holding points
// the source of truth does not (orphans, or the transient upsert-before-stamp
// window) is named, not silently swallowed.
func TestReconcileReportsOverCount(t *testing.T) {
	report, err := reconcileFixture(t, 10, 12)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := report.Over["team-local"]; got != 2 {
		t.Errorf("over-count reported as %d, want 2", got)
	}
	if len(report.Under) != 0 && len(report.Rebuilt) != 0 {
		t.Errorf("an over-count index reported under/rebuilt: %+v", report)
	}
	if s := report.String(); !strings.Contains(s, "team-local") || !strings.Contains(s, "2 point(s)") {
		t.Errorf("the report does not name the namespace and the excess:\n%s", s)
	}
}

// TestReconcileReportsRebuilt pins both halves of the empty case: the report
// names the rebuilt namespace, and the rebuild actually LANDED — the report
// would be lying if the replay had silently written nothing.
func TestReconcileReportsRebuilt(t *testing.T) {
	dir := t.TempDir()
	gdb, err := gorm.Open(glebarez.Open(filepath.Join(dir, "sot.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sot := sqlitevec.New(gdb)
	idx, err := chromemvec.New(filepath.Join(dir, "chromem"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	ctx := context.Background()
	const ns = "team-local"
	if err := sot.Upsert(ctx, ns, fixturePoints(10)); err != nil {
		t.Fatalf("seed source of truth: %v", err)
	}

	report, err := reconcileChromem(ctx, sot, idx, store.NewHybrid(sot, idx))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(report.Rebuilt) != 1 || report.Rebuilt[0] != ns {
		t.Fatalf("the empty namespace was not reported rebuilt: %v", report.Rebuilt)
	}
	if n, err := idx.Count(ctx, ns); err != nil || n != 10 {
		t.Fatalf("the rebuild did not land: count=%d err=%v (want 10)", n, err)
	}
}

// TestReconcileReportsClean pins the vacuous case: a healthy boot reads one
// quiet line, not a namespace-by-namespace listing.
func TestReconcileReportsClean(t *testing.T) {
	report, err := reconcileFixture(t, 10, 10)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(report.Rebuilt) != 0 && len(report.Under) != 0 && len(report.Over) != 0 {
		t.Fatalf("a healthy boot reported findings: %+v", report)
	}
	if s := report.String(); !strings.Contains(s, "every namespace already holds a point") {
		t.Errorf("a healthy boot does not say so: %q", s)
	}
}

// TestDoctorIndexFailsOnPartialDrift keeps the symptom layer honest after the
// reconcile report made partial fall-behind visible: doctor --index must STILL
// fail on it (an operator with a deliberately partial index has to say so), and
// the absent points must keep naming the sync repair rather than a rebuild.
func TestDoctorIndexFailsOnPartialDrift(t *testing.T) {
	partial := palace.DriftReport{Checked: palace.NamespaceSplit{Drawers: 10}, Total: 3, Drifted: []palace.DriftedPoint{
		{Store: "index", DrawerID: "h", Actual: "wing_acme", Missing: true},
		{Store: "index", DrawerID: "i", Actual: "wing_acme", Missing: true},
		{Store: "index", DrawerID: "j", Actual: "wing_acme", Missing: true},
	}}

	var buf bytes.Buffer
	if err := reportDrift(&buf, partial); err == nil {
		t.Error("partial population fall-behind reported and the command exited 0")
	}
	out := buf.String()
	for _, want := range []string{"3 stored point(s) disagree", "ABSENT", "sync"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not name %q, so an operator cannot act on it:\n%s", want, out)
		}
	}
}

// TestReconcileReportStringIsDeterministic: the boot log iterates maps, and a
// map's iteration order is random — the same report must render identically
// every run, or an operator grepping the boot log for a namespace cannot rely
// on the clause order (indexdrift.go sorts ids for exactly this reason: a
// deterministic report is a diffable and greppable one).
func TestReconcileReportStringIsDeterministic(t *testing.T) {
	a := ReconcileReport{
		Rebuilt: []string{"wing-1"},
		Under:   map[string]int{"omega": 3, "alpha": 1},
		Over:    map[string]int{"zeta": 2, "beta": 4},
	}
	b := ReconcileReport{
		Rebuilt: []string{"wing-1"},
		Under:   map[string]int{"alpha": 1, "omega": 3},
		Over:    map[string]int{"beta": 4, "zeta": 2},
	}
	sa, sb := a.String(), b.String()
	if sa != sb {
		t.Errorf(`the same report rendered differently between runs:
%s
vs
%s`, sa, sb)
	}
	iAlpha := strings.Index(sa, "alpha")
	iOmega := strings.Index(sa, "omega")
	if iAlpha < 0 || iOmega < 0 {
		t.Fatalf(`the rendered report lost a namespace:
%s`, sa)
	}
	if iAlpha > iOmega {
		t.Errorf(`the report's namespace clauses are not sorted (alpha after omega):
%s`, sa)
	}
}
