package palace

import (
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// TestIndexDriftIsSilentOnACleanPalace: a check that fires on a healthy palace
// is one people learn to skip, so this pins the negative case first.
func TestIndexDriftIsSilentOnACleanPalace(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-drift-clean"

	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the rerank pool ships at ten"})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_alpha", Room: "diary", Content: "a memory in another wing entirely"})

	report, err := svc.IndexDrift(ctx, team)
	if err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	if report.Checked.Drawers+report.Checked.Closets == 0 {
		t.Fatal("the check examined nothing, so it cannot have found nothing")
	}
	if !report.Clean() {
		t.Errorf("a freshly written palace reports drift: %+v", report.Drifted)
	}
}

// TestIndexDriftIsFound: a payload whose wing no longer matches its drawer is
// reported, and reported against the store it is wrong in.
//
// The relabel here is exactly what MergeWing does — the drawer row moves and the
// stored payload does not — which is how 13 of one live palace's 359 points came
// to be unreachable from the wing they were filed in.
func TestIndexDriftIsFound(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-drift"

	d := mustAddOne(t, svc, team, AddInput{Wing: "wing_acme-legacy", Room: "decisions",
		Content: "a decision filed before the wings were merged"})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "a decision filed after"})

	// Relabel the ROW only, leaving every stored payload behind — the merge as it
	// behaves today.
	if _, err := svc.repo.RelabelDrawerWing(ctx, team, []string{"wing_acme-legacy"}, "wing_acme"); err != nil {
		t.Fatalf("relabel: %v", err)
	}

	report, err := svc.IndexDrift(ctx, team)
	if err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	if report.Clean() {
		t.Fatal("a drawer was relabelled and every stored payload still says the old wing, " +
			"and the check reports clean")
	}
	for _, dp := range report.Drifted {
		if dp.DrawerID != d.ID {
			t.Errorf("reported drift on %q, which was not relabelled", dp.DrawerID)
		}
		if dp.Indexed != "wing_acme-legacy" || dp.Actual != "wing_acme" {
			t.Errorf("drift reported as %q -> %q, want %q -> %q",
				dp.Indexed, dp.Actual, "wing_acme-legacy", "wing_acme")
		}
		if dp.Store == "" {
			t.Error("the drift does not name which store it is in; a repair that fixed only one " +
				"would look complete from the other side")
		}
	}
}

// TestIndexDriftReadsEveryStore: the test service is a single store, so this
// pins the two-store case explicitly — a Hybrid must be checked on BOTH halves,
// because the index is what a scoped search filters on and the source of truth
// is what the next sync will replay over it.
func TestIndexDriftReadsEveryStore(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-drift-split"

	sot := &recordingStore{VectorStore: svc.vectors, name: "sot"}
	index := &recordingStore{VectorStore: svc.vectors, name: "index"}
	svc.vectors = &fakeSplit{VectorStore: svc.vectors, sot: sot, index: index}

	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "one memory"})
	if _, err := svc.IndexDrift(ctx, team); err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	if sot.reads == 0 {
		t.Error("the source of truth was never read; a drift it alone carries survives the check " +
			"and comes back on the next sync")
	}
	if index.reads == 0 {
		t.Error("the index was never read; that is the copy a scoped search actually filters on")
	}
}

// fakeSplit presents one store as two halves, so the split path is exercised
// without standing up a second backend.
type fakeSplit struct {
	store.VectorStore
	sot   store.SourceOfTruth
	index store.VectorStore
}

func (f *fakeSplit) Halves() (store.SourceOfTruth, store.VectorStore) { return f.sot, f.index }

// recordingStore counts PointsByIDs calls and otherwise delegates.
type recordingStore struct {
	store.VectorStore
	name  string
	reads int
}

func (r *recordingStore) PointsByIDs(ctx context.Context, ns string, ids []string) ([]store.Point, error) {
	r.reads++
	return r.VectorStore.PointsByIDs(ctx, ns, ids)
}

func (r *recordingStore) AllPoints(ctx context.Context, ns string) ([]store.Point, error) {
	if sot, ok := r.VectorStore.(store.SourceOfTruth); ok {
		return sot.AllPoints(ctx, ns)
	}
	return nil, nil
}

func (r *recordingStore) Namespaces(ctx context.Context) ([]string, error) {
	if sot, ok := r.VectorStore.(store.SourceOfTruth); ok {
		return sot.Namespaces(ctx)
	}
	return nil, nil
}

// TestIndexDriftReportsAnAbsentPoint: a drawer the store holds NO point for is a
// worse fault than a mislabelled one, and it was reported as agreement.
//
// The first version of this check looked only at the points a store RETURNED, so
// a memory the index had lost entirely — unreachable by any search, not merely by
// a scoped one — came back Clean(). Found by review, not by a test.
func TestIndexDriftReportsAnAbsentPoint(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-drift-absent"

	gone := mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "one"})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "two"})

	// Drop the POINT and leave the row: the index has lost a memory the palace
	// still believes it holds.
	if err := svc.vectors.Delete(ctx, team, []string{gone.ID}); err != nil {
		t.Fatalf("delete point: %v", err)
	}

	report, err := svc.IndexDrift(ctx, team)
	if err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	if report.Clean() {
		t.Fatal("a drawer whose point is gone reported clean — the memory is unreachable by any " +
			"search at all and the check cannot see it")
	}
	var found bool
	for _, d := range report.Drifted {
		if d.DrawerID == gone.ID {
			found = true
			if !d.Missing {
				t.Errorf("the absent point is reported as a wrong label rather than an absence: %+v", d)
			}
		}
	}
	if !found {
		t.Errorf("the drawer with no point is not in the report: %+v", report.Drifted)
	}
}

// TestIndexDriftDoesNotFaultAPendingEmbedding: a drawer awaiting its first
// embedding legitimately has no point, so a busy palace must not look broken.
func TestIndexDriftDoesNotFaultAPendingEmbedding(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-drift-pending"

	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "embedded"})
	if err := svc.repo.SaveUnembedded(ctx, []Drawer{{
		ID: "pending-1", TeamID: team, Wing: "wing_acme", Room: "decisions",
		Content: "queued", FiledAt: "2026-08-21T00:00:00Z",
	}}); err != nil {
		t.Fatalf("SaveUnembedded: %v", err)
	}

	report, err := svc.IndexDrift(ctx, team)
	if err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	if !report.Clean() {
		t.Errorf("a drawer awaiting its first embedding was reported as drift: %+v", report.Drifted)
	}
	if report.Pending.Drawers != 1 {
		t.Errorf("Pending.Drawers = %d, want 1 — the queue must be counted, not hidden", report.Pending.Drawers)
	}
}

// TestIndexDriftChecksClosetsToo: closets keep a second copy of the wing in their
// stored payload, and a check that looked only at drawers reported clean over a
// split closet index.
//
// Closet search passes no filter today, so nothing ranks wrongly yet — which is
// exactly why this would have gone unnoticed until somebody scoped it, and then
// looked like a search bug rather than a merge one.
func TestIndexDriftChecksClosetsToo(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-drift-closets"

	if _, err := svc.Mine(ctx, team, MineInput{
		Wing: "wing_acme-legacy", Room: "decisions", Source: "notes.md",
		Content: strings.Repeat("Redis powers it and Postgres backs it. Redis is fast, Postgres is durable. ", 40),
	}); err != nil {
		t.Fatalf("Mine: %v", err)
	}

	// Relabel the closet ROWS only, which is what the merge did before it patched
	// their payloads.
	if _, err := svc.repo.RelabelClosetWing(ctx, team, []string{"wing_acme-legacy"}, "wing_acme"); err != nil {
		t.Fatalf("relabel closets: %v", err)
	}
	if _, err := svc.repo.RelabelDrawerWing(ctx, team, []string{"wing_acme-legacy"}, "wing_acme"); err != nil {
		t.Fatalf("relabel drawers: %v", err)
	}

	report, err := svc.IndexDrift(ctx, team)
	if err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	var sawCloset bool
	for _, d := range report.Drifted {
		if strings.Contains(d.Store, "closet") {
			sawCloset = true
		}
	}
	if !sawCloset {
		t.Errorf("closet payloads were left behind and the check does not mention them: %+v", report.Drifted)
	}
}

// TestIndexDriftCarriesRealIndexPopulation is the JD-003 gate: an over-count
// index (orphans, or the transient upsert-before-stamp window) is invisible to
// the per-id audit by construction — it only asks for drawer ids — so the
// report must carry the index half's REAL population count. Without it the
// coverage view renders indexed == expected, indistinguishable from a perfect
// index (ADR-033 R3's raw-fields promise).
func TestIndexDriftCarriesRealIndexPopulation(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-drift-overcount"

	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "one memory"})

	// An orphan: a point no drawer asked for. PointsByIDs never returns it, so
	// only the index's own Count can show it.
	vec := make([]float32, fakeDim)
	vec[0] = 1
	if err := svc.vectors.Upsert(ctx, team, []store.Point{{
		ID: "orphan-1", Vector: vec, Payload: map[string]any{"wing": "wing_acme"},
	}}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	report, err := svc.IndexDrift(ctx, team)
	if err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	if report.Checked.Drawers != 1 {
		t.Fatalf("Checked.Drawers = %d, want 1 — the orphan is not a drawer row", report.Checked.Drawers)
	}
	if report.Total != 0 {
		t.Fatalf("the orphan must not count as drift: Total = %d", report.Total)
	}
	v := report.CoverageView()["drawers"]
	if v.Indexed != 2 || v.Expected != 1 {
		t.Fatalf("coverage view = %+v, want indexed 2 over expected 1 — the over-count must be visible", v)
	}
	if v.Indexed <= v.Expected {
		t.Fatal("an orphaned index renders indexed == expected; the raw fields cannot show the over-count")
	}
}

// TestMergePatchesClosetPayloads: the merge's own closet half, end to end.
func TestMergePatchesClosetPayloads(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-merge-closets"

	if _, err := svc.Mine(ctx, team, MineInput{
		Wing: "wing_acme-legacy", Room: "decisions", Source: "notes.md",
		Content: strings.Repeat("Redis powers it and Postgres backs it. Redis is fast, Postgres is durable. ", 40),
	}); err != nil {
		t.Fatalf("Mine: %v", err)
	}
	if _, err := svc.MergeWing(ctx, team, []string{"wing_acme-legacy"}, "wing_acme"); err != nil {
		t.Fatalf("MergeWing: %v", err)
	}
	report, err := svc.IndexDrift(ctx, team)
	if err != nil {
		t.Fatalf("IndexDrift: %v", err)
	}
	if !report.Clean() {
		t.Errorf("after a merge, closet or drawer payloads still claim the old wing: %+v", report.Drifted)
	}
}
