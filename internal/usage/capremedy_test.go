package usage

import (
	"strings"
	"testing"
)

// TestACappedCallerIsToldARemedyThatCanWork covers the user-visible rejection,
// which is the surface that actually reaches an agent, and /import's 429.
//
// The message said "upgrade the project's plan" unconditionally. Since
// capLookupFor returns usage.FixedCap for every nonzero --monthly-request-cap,
// under an override changing teams.plan_id moves nothing — so the one sentence a
// blocked caller gets named an action that cannot succeed. On a self-hosted
// install there is no plan to buy at all, which is the deployment the override
// exists for.
//
// It asserts on the two branches rather than on exact wording: the property is
// that the remedy DIFFERS with the cap's source and that neither branch sends a
// caller to the other's action.
func TestACappedCallerIsToldARemedyThatCanWork(t *testing.T) {
	plan := Status{Used: 10, Cap: 10}.CapRejection()
	fixed := Status{Used: 10, Cap: 10, CapFixed: true}.CapRejection()

	if !strings.Contains(plan, "plan") {
		t.Errorf("a plan-derived cap must point at the plan; got %q", plan)
	}
	if strings.Contains(fixed, "upgrade the project") {
		t.Errorf("a deployment-fixed cap still tells the caller to upgrade the plan, which cannot "+
			"move the enforced cap: %q", fixed)
	}
	if !strings.Contains(fixed, "monthly-request-cap") {
		t.Errorf("a deployment-fixed cap must name the knob that actually raises it; got %q", fixed)
	}
	if plan == fixed {
		t.Error("both cap sources produce the same advice, so the source is not reaching the caller")
	}
	for _, msg := range []string{plan, fixed} {
		if !strings.Contains(msg, "10/10") {
			t.Errorf("the rejection no longer reports usage against the cap: %q", msg)
		}
	}
}
