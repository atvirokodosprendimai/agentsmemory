package palace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestInvalidateAFactThatIsNotThereIsRefused pins the defect M reported on
// 2026-08-27: kg_invalidate answered success for a fact it had never touched.
//
// The mechanism was one underscore. Service.KGInvalidate called
// InvalidateKGTriples — which returns RowsAffected precisely so a caller can
// tell a real ending from a miss — and discarded the count with `_`, then
// returned nil. The MCP handler then rendered a hardcoded "success": true.
//
// Reproduced against the running server before the fix: invalidating
// subject/predicate/object that had never existed returned
// {"success":true,...} while kg_triples gained nothing and ended nothing.
//
// This is the repo's characteristic defect wearing a temporal hat — a write that
// reports success and changes nothing — and it is worse here than elsewhere,
// because the whole point of an invalidation is that the fact stops being
// returned. An agent that retracts a wrong fact, is told it worked, and finds
// the fact still current has been lied to by the one operation that exists to
// make the store honest.
func TestInvalidateAFactThatIsNotThereIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-kginv"

	n, _, _, err := svc.KGInvalidate(ctx, team, "no such subject", "never asserted", "no such object", "2026-08-27", "the fact was withdrawn")
	if err == nil {
		t.Fatalf("KGInvalidate on a fact that does not exist returned nil error and n=%d; "+
			"it must refuse, because the caller asked for a fact to stop being current and none did", n)
	}
	if !errors.Is(err, ErrFactNotFound) {
		t.Errorf("error = %v; want ErrFactNotFound so a caller can tell a miss from a malformed request", err)
	}
	// The message has to name what was SEARCHED for, not what was sent: the
	// likeliest cause of a legitimate miss is normalization — normalizeEntityID
	// and normalizePredicate rewrite all three terms — and an error echoing the
	// caller's own spelling back at them explains nothing.
	if !strings.Contains(err.Error(), normalizeEntityID("no such subject")) {
		t.Errorf("error %q does not name the NORMALIZED subject it looked for; "+
			"a miss caused by normalization is undiagnosable without it", err)
	}
}

// TestInvalidateReportsHowManyFactsEnded covers the other half: a real
// invalidation must say how much it ended, for the same reason Delete reports
// how many chunks went. One (subject, predicate, object) can match several rows
// — the same fact re-asserted with different valid_from — so "it worked" is not
// the same answer as "three facts ended".
func TestInvalidateReportsHowManyFactsEnded(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-kginv2"

	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	n, _, _, err := svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "2026-08-27", "she left")
	if err != nil {
		t.Fatalf("KGInvalidate on a fact that exists: %v", err)
	}
	if n != 1 {
		t.Errorf("ended %d facts; want 1", n)
	}

	// And ending it twice is a miss, not a second success: the fact is already
	// historical, so there is no CURRENT row left to end.
	if _, _, _, err := svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "2026-08-27", "she left"); !errors.Is(err, ErrFactNotFound) {
		t.Errorf("re-invalidating an already-ended fact returned %v; want ErrFactNotFound", err)
	}
}
