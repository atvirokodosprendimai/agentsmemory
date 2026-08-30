package views

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestUpgradeCardDoesNotNameAProviderItMayNotUse pins ADR-042-T1's user-facing
// half. The hint under the Upgrade button read "Secure checkout via Stripe —
// you'll confirm payment on Stripe, then land back here", which is false on every
// count under BILLING_PROVIDER=opencollective: the provider is not Stripe, and
// the OpenCollective contribution checkout has no success redirect (its
// createCheckout ignores SuccessURL), so the contributor does not land back here.
//
// The card is rendered from one template for both providers, so the copy must be
// true under either. This test is what stops a provider name being reintroduced —
// the previous "de-Stripe the billing copy" pass (c326817) edited the handler
// comments and missed this string, because nothing asserted on it.
func TestUpgradeCardDoesNotNameAProviderItMayNotUse(t *testing.T) {
	var buf bytes.Buffer
	if err := UpgradeCard(ProjectVM{TeamID: "t42"}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	for _, banned := range []string{"Stripe", "stripe", "OpenCollective", "Open Collective"} {
		if strings.Contains(html, banned) {
			t.Errorf("UpgradeCard names the payment provider %q; the card is shared by both providers, so naming one is false under the other\n---\n%s", banned, html)
		}
	}
	// "land back here" was the second false clause: only the Stripe flow returns
	// the user to this dashboard.
	for _, banned := range []string{"land back here", "back here"} {
		if strings.Contains(html, banned) {
			t.Errorf("UpgradeCard promises the user returns here (%q); the OpenCollective checkout has no success redirect\n---\n%s", banned, html)
		}
	}
}

// TestUpgradeCardStillExplainsTheHandoff guards the obvious wrong fix: deleting
// the hint entirely would pass the test above while leaving the user with no idea
// what the button does. The hint must still say a payment happens elsewhere.
func TestUpgradeCardStillExplainsTheHandoff(t *testing.T) {
	var buf bytes.Buffer
	if err := UpgradeCard(ProjectVM{TeamID: "t42"}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), `class="hint"`) {
		t.Fatal("UpgradeCard has no hint text: the checkout hand-off must still be explained, not deleted")
	}
}
