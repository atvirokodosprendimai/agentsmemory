package main

import (
	"strings"
	"testing"
)

// TestACapOverrideAndConfiguredBillingAreRefused pins the combination whose two
// halves cannot both be honoured.
//
// Under a nonzero override capLookupFor returns usage.FixedCap for every
// workspace, so teams.plan_id no longer decides the enforced cap — and billing
// exists to change teams.plan_id in exchange for money. Serving both means a paid
// upgrade is a no-op an operator cannot see: the checkout succeeds, the plan row
// flips, and the cap does not move.
//
// Both signs are covered because they fail differently and only one is obvious. A
// positive override sells a lift that never lands; a NEGATIVE override sells a
// lift from a cap that is already unlimited.
func TestACapOverrideAndConfiguredBillingAreRefused(t *testing.T) {
	for _, cap := range []int{50, -1} {
		err := refuseCapOverrideWithBilling(cap, true)
		if err == nil {
			t.Errorf("an override of %d starts happily beside configured billing: a user can pay, "+
				"the plan flips, and the enforced cap stays exactly where it was", cap)
			continue
		}
		if !strings.Contains(err.Error(), "monthly-request-cap") {
			t.Errorf("the refusal does not name the flag an operator has to change: %v", err)
		}
	}

	// And neither half alone is refused, or the override would be unusable on the
	// self-hosted install it exists for.
	if err := refuseCapOverrideWithBilling(50, false); err != nil {
		t.Errorf("an override refused with billing OFF — that is the deployment this flag is for: %v", err)
	}
	if err := refuseCapOverrideWithBilling(0, true); err != nil {
		t.Errorf("configured billing refused with no override, which breaks every hosted install: %v", err)
	}
}
