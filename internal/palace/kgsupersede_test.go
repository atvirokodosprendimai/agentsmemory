package palace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// ADR-038 T4. Replacing a fact is ONE verb and ONE transaction. Before it, the
// only expressible replacement was a hand-rolled invalidate-then-add, which is
// what issue #74 reproduced: two calls, a day-scale overlap between them, and no
// atomicity if the session died in the middle.

func TestKgSupersedeLeavesNoBoundaryOverlap(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-kgsup"

	if _, err := svc.KGAdd(ctx, team, "svc", "deploys to", "old-host", "2026-01-01T00:00:00Z", "", "", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	boundary, err := svc.KGSupersede(ctx, team, "svc", "deploys to", "old-host", "new-host", "migrated off the old rack")
	if err != nil {
		t.Fatalf("kg supersede: %v", err)
	}
	if isDateOnly(boundary) {
		t.Fatalf("boundary = %q; it must be an RFC3339 DATETIME, never a date. temporalEndKey stretches "+
			"a date-only valid_to to T23:59:59Z, which is the 86,400-second overlap issue #74 reproduced", boundary)
	}

	// Both endpoints carry the SAME instant, which is what collapses the overlap
	// from a day to a single instant.
	rows, err := svc.repo.CurrentTriples(ctx, team, "svc", "deploys_to", "new-host")
	if err != nil {
		t.Fatalf("read successor: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("current successor rows = %d; want 1", len(rows))
	}
	if rows[0].ValidFrom != boundary {
		t.Errorf("successor valid_from = %q; want the shared boundary %q — a successor that starts "+
			"later leaves a GAP where the graph knows nothing", rows[0].ValidFrom, boundary)
	}

	// ⚠ Deliberately NOT asserted AT the boundary instant. inEffectAt is inclusive
	// on BOTH ends, so a shared endpoint is in effect for both rows; that closed-
	// versus-half-open question is issue #74's, and answering it re-reads every
	// already-ended fact. This assertion therefore passes under inclusive AND
	// half-open semantics alike — it CANNOT fail on what #74 will decide, and says
	// so here so the next reader does not mistake it for boundary coverage.
	res, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "svc", Status: KGStatusCurrent})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var hosts []string
	for _, f := range res.Facts {
		hosts = append(hosts, f.Object)
	}
	if len(hosts) != 1 || hosts[0] != "new-host" {
		t.Errorf("current facts = %v; want exactly [new-host]. The day-scale overlap is what this verb removes", hosts)
	}
}

func TestKgSupersedeRecordsWhyTheOldFactEnded(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-kgwhy"

	if _, err := svc.KGAdd(ctx, team, "svc", "deploys to", "old-host", "2026-01-01", "", "", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.KGSupersede(ctx, team, "svc", "deploys to", "old-host", "new-host", "the rack was decommissioned"); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	var row kgTripleRow
	if err := svc.repo.db.WithContext(ctx).Model(&kgTripleRow{}).
		Where("team_id = ? AND object = ? AND valid_to != ''", team, "old-host").
		First(&row).Error; err != nil {
		t.Fatalf("read ended fact: %v", err)
	}
	if row.EndedReason != "the rack was decommissioned" {
		t.Errorf("ended_reason = %q; the store already kept THAT a fact ended, in valid_to. The reason "+
			"is the expensive half and it had nowhere to land before 00032", row.EndedReason)
	}
	// A reason is required, not merely accepted.
	for _, reason := range []string{"", "   "} {
		if _, err := svc.KGSupersede(ctx, team, "svc", "deploys to", "new-host", "third-host", reason); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("supersede with reason %q returned %v; want ErrInvalidInput", reason, err)
		}
	}
}

func TestKgSupersedeIsAtomic(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-kgatomic"

	if _, err := svc.KGAdd(ctx, team, "svc", "deploys to", "old-host", "2026-01-01T00:00:00Z", "", "", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Fail the INSERT of the successor, which is the instant between the end and
	// the add. Without a transaction the old fact stays ended and nothing replaces
	// it: the graph reports ZERO current values for a subject the team never
	// stopped knowing about, permanently, and the failure is invisible because
	// valid_to is set and looks deliberate.
	//
	// Injected as a gorm Create callback rather than by breaking an argument,
	// because every argument that fails validation fails BEFORE the end — which
	// is a different code path and would leave this test unable to fail on
	// atomicity at all.
	boom := errors.New("injected: the successor could not be written")
	const cbName = "test:fail_kg_triple_insert"
	err := svc.repo.db.Callback().Create().Before("gorm:create").Register(cbName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "kg_triples" {
			_ = tx.AddError(boom)
		}
	})
	if err != nil {
		t.Fatalf("register callback: %v", err)
	}
	defer func() {
		if err := svc.repo.db.Callback().Create().Remove(cbName); err != nil {
			t.Fatalf("remove callback: %v", err)
		}
	}()

	if _, err := svc.KGSupersede(ctx, team, "svc", "deploys to", "old-host", "new-host", "migrated"); err == nil {
		t.Fatal("a supersede whose successor cannot be written must fail, not report success")
	}

	// The graph is exactly as it was: one current fact, the OLD one, unended.
	res, qerr := svc.KGQuery(ctx, team, KGQueryInput{Entity: "svc", Status: KGStatusCurrent})
	if qerr != nil {
		t.Fatalf("query: %v", qerr)
	}
	got := make([]string, 0, len(res.Facts))
	for _, f := range res.Facts {
		got = append(got, fmt.Sprintf("%s(valid_to=%q)", f.Object, f.ValidTo))
	}
	if len(res.Facts) != 1 || res.Facts[0].Object != "old-host" {
		t.Fatalf("current facts after a failed supersede = %v; want exactly [old-host]. A failure "+
			"between the end and the add must leave the graph UNCHANGED — not with zero current "+
			"values, which reads as a deliberate retraction nobody made", strings.Join(got, " "))
	}
}
