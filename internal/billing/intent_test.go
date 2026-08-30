package billing

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// TestIntentTagIsStableAndURLSafe pins the two properties the tag must have: the
// same workspace always produces the same tag (or reconciliation could never match
// it back), and it survives a URL query without escaping (it is placed in the
// contribution link). ADR-042-T2.
func TestIntentTagIsStableAndURLSafe(t *testing.T) {
	const teamID = "8f14e45f-ceea-467a-9f6a-6b2d0f2c5a11"
	tag := intentTag(teamID)
	if tag == "" {
		t.Fatal("intentTag returned empty")
	}
	if got := intentTag(teamID); got != tag {
		t.Fatalf("intentTag is not stable: %q then %q", tag, got)
	}
	if intentTag("a-different-team") == tag {
		t.Fatal("intentTag collides across distinct workspaces")
	}
	if url.QueryEscape(tag) != tag {
		t.Fatalf("tag %q is not URL-safe (escapes to %q)", tag, url.QueryEscape(tag))
	}
	// The raw workspace id must not be recoverable from a public contribution
	// record: the tag is world-readable on opencollective.com.
	if strings.Contains(tag, teamID) {
		t.Fatalf("tag %q leaks the raw team id into a public record", tag)
	}
}

// TestCheckoutIntentRoundTrips proves an intent can be found again by both
// attribution channels. The email leg is the fallback for when the tag does not
// survive the hosted checkout — the ADR's named open risk.
func TestCheckoutIntentRoundTrips(t *testing.T) {
	svc, _, _, gdb, teamID := newTestEnv(t)
	_ = svc
	intents := NewIntentRepo(gdb)
	ctx := context.Background()

	tag := intentTag(teamID)
	if err := intents.Record(ctx, CheckoutIntent{
		TeamID: teamID, PlanCode: "pro_monthly", Tag: tag, Email: "buyer@example.com",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := intents.MatchByTag(ctx, tag, "pro_monthly")
	if err != nil {
		t.Fatalf("MatchByTag: %v", err)
	}
	if got.TeamID != teamID {
		t.Fatalf("MatchByTag team = %q, want %q", got.TeamID, teamID)
	}

	got, err = intents.MatchByEmail(ctx, "buyer@example.com", "pro_monthly")
	if err != nil {
		t.Fatalf("MatchByEmail: %v", err)
	}
	if got.TeamID != teamID {
		t.Fatalf("MatchByEmail team = %q, want %q", got.TeamID, teamID)
	}
}

// TestUnknownIntentIsNotFoundRatherThanZero is the one that matters for safety: a
// miss must be a distinguishable not-found, never a zero-valued CheckoutIntent
// whose empty TeamID would read as a real match and attribute a payment to "".
func TestUnknownIntentIsNotFoundRatherThanZero(t *testing.T) {
	_, _, _, gdb, _ := newTestEnv(t)
	intents := NewIntentRepo(gdb)
	ctx := context.Background()

	if _, err := intents.MatchByTag(ctx, "no-such-tag", "pro_monthly"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("MatchByTag on an unknown tag: want ErrRecordNotFound, got %v", err)
	}
	if _, err := intents.MatchByEmail(ctx, "nobody@example.com", "pro_monthly"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("MatchByEmail on an unknown email: want ErrRecordNotFound, got %v", err)
	}
	// The empty-key guards, tested against rows that would ACTUALLY match if the
	// guard were removed. An earlier version of this test seeded only a row with a
	// non-empty tag, so disabling the guard still returned not-found and a mutant
	// that deleted it survived: the assertion was decoration.
	//
	// CustomerEmail is optional on CheckoutRequest ("empty lets the provider collect
	// it"), so StartCheckout genuinely records intents with email = '' — this is a
	// row production creates, not a contrived one. An order whose contributor email
	// we cannot read must not match it.
	if err := intents.Record(ctx, CheckoutIntent{
		TeamID: "victim-team", PlanCode: "pro_monthly", Tag: "", Email: "",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got, err := intents.MatchByTag(ctx, "", "pro_monthly"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("MatchByTag on an EMPTY tag matched team %q: an untagged order must attribute to nobody, got err %v", got.TeamID, err)
	}
	if got, err := intents.MatchByEmail(ctx, "", "pro_monthly"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("MatchByEmail on an EMPTY email matched team %q: an order with no readable email must attribute to nobody, got err %v", got.TeamID, err)
	}
}

// TestStartCheckoutSurvivesIntentWriteFailure keeps the bookkeeping subordinate to
// the payment: a customer must never be blocked from paying because our intent
// table refused a write.
func TestStartCheckoutSurvivesIntentWriteFailure(t *testing.T) {
	svc, _, _, _, teamID := newTestEnv(t)
	oc := newOpenCollectiveProvider(Config{
		PriceByPlanCode:          map[string]string{"pro_monthly": testOCMonthlyURL},
		OpenCollectiveProjectURL: testOCProjectURL,
	})
	svc.checkout, svc.webhook, svc.portal = oc, oc, oc
	svc.intents = failingIntentRecorder{}

	url, err := svc.StartCheckout(context.Background(), CheckoutRequest{
		TeamID: teamID, PlanCode: "pro_monthly", CustomerEmail: "a@b.co",
		SuccessURL: "https://app.example/ok",
	})
	if err != nil {
		t.Fatalf("StartCheckout must not fail when intent recording fails: %v", err)
	}
	if url == "" {
		t.Fatal("StartCheckout returned an empty URL")
	}
}

// failingIntentRecorder always refuses, standing in for a full disk or a locked
// table.
type failingIntentRecorder struct{}

func (failingIntentRecorder) Record(context.Context, CheckoutIntent) error {
	return errors.New("intent store unavailable")
}
