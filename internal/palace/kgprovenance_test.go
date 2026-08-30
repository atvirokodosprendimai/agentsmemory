package palace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A fact's source_drawer_id is a claim that a row exists. Nothing checked it, so
// the claim was accepted and the reader discovered it was false — if ever — by
// following the pointer to nothing. Measured 2026-08-27 against one 2,037-drawer
// palace: 16 facts named no row, which is what `doctor --corpus` reports after the
// fact and what these tests refuse at the door.
//
// ⚠THE ACCEPT CASE IS THE LOAD-BEARING ONE. A fact citing an ENDED drawer is the
// system working — provenance is historical, and a memory that was corrected does
// not retract the fact somebody derived from it. A check that refuses those would
// be worse than no check: it would make every supersede break its own citations.
// That is ADR-038 T6's three-state rule, applied to the write path.

func TestAFactCannotCiteADrawerThatDoesNotExist(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-prov"

	_, err := svc.KGAdd(ctx, team, "svc", "documented in",
		"the deploy note", "", "", "", "", strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("a fact citing a drawer id that names no row was accepted; " +
			"provenance that resolves to nothing is the defect this refuses")
	}
	if !errors.Is(err, ErrSourceDrawerNotFound) {
		t.Errorf("err = %v; want ErrSourceDrawerNotFound so a caller can tell this "+
			"apart from a malformed value", err)
	}
}

func TestAFactMayCiteAnEndedDrawer(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-prov-ended"

	added, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "we deploy on Tuesdays"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	cited := added.Drawers[0].ID

	// The memory is corrected, so the cited row is now ENDED and a successor holds
	// the current text. The fact derived from it is still a true record of what was
	// believed then.
	if _, err := svc.Supersede(ctx, team, cited, "we deploy on Thursdays", "the release train moved"); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	if _, err := svc.KGAdd(ctx, team, "we", "deploy on", "Tuesdays", "", "2026-08-27", "", "", cited); err != nil {
		t.Fatalf("a fact citing an ENDED drawer must be accepted — provenance is "+
			"historical, and refusing it would make every correction break its own "+
			"citations: %v", err)
	}
}

func TestAFactMayStandAlone(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-prov-none"

	// source_drawer_id defaults to '' precisely so a fact need not come from a
	// drawer. An existence check that treated empty as "not found" would refuse
	// every fact the knowledge graph was designed to hold.
	if _, err := svc.KGAdd(ctx, team, "the reranker", "runs on", "CPU", "", "", "", "", ""); err != nil {
		t.Fatalf("a fact with no source drawer must be accepted: %v", err)
	}
}
