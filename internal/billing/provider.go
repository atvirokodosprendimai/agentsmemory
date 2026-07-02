package billing

import (
	"context"
	"net/http"
)

// This file defines the payment-provider seam. billing.Service is written once,
// against these two small interfaces, and a concrete provider (Stripe or Polar)
// is plugged in at construction. Keeping the seam this thin — one method to open
// a checkout, one to turn a signed webhook into a normalized event — is what lets
// the plan-flip use-case logic in Service stay provider-agnostic, so adding Polar
// alongside Stripe touched no business rule (decision 2026-07-02: dual provider,
// selected by BILLING_PROVIDER at boot).

// Provider identifiers for Config.Provider. Stripe is the default so an existing
// deployment with no BILLING_PROVIDER set keeps its current behavior.
const (
	ProviderStripe = "stripe"
	ProviderPolar  = "polar"
)

// checkoutInput is everything needed to open one hosted checkout, expressed in
// billing's own terms so each payment SDK/API stays behind the seam. CancelURL is
// honored by providers that support a cancel redirect (Stripe); Polar's hosted
// checkout has no cancel URL, so its provider ignores it.
type checkoutInput struct {
	PriceID       string // the provider's price/product id the customer subscribes to
	TeamID        string // workspace to upgrade; echoed back on the webhook
	PlanCode      string // our plan code, carried in metadata for the webhook
	CustomerEmail string // prefills checkout; empty lets the provider collect it
	SuccessURL    string
	CancelURL     string
}

// checkoutAPI is the one write the upgrade flow performs: turn a price + workspace
// into a hosted checkout URL to redirect the user to. It is an interface so
// Service can be unit-tested with a fake — no network, no keys.
type checkoutAPI interface {
	createCheckout(ctx context.Context, in checkoutInput) (redirectURL string, err error)
}

// eventKind classifies a verified webhook after the provider has normalized it,
// so Service can dispatch without knowing Stripe's or Polar's event vocabulary.
type eventKind int

const (
	// eventIgnored is a verified-but-irrelevant event (e.g. an interim status or an
	// event type we don't act on). It is a success no-op, never an error, so a
	// provider's broad event stream doesn't 400 the webhook endpoint.
	eventIgnored eventKind = iota
	// eventActivated means a workspace completed checkout and should be on its Pro
	// plan. TeamID + PlanCode attribute it; CustomerID + SubscriptionID record the
	// durable provider relationship.
	eventActivated
	// eventCanceled means a workspace's subscription has actually ended and it must
	// return to Free. SubscriptionID is the stable key used to find the workspace.
	eventCanceled
)

// providerEvent is the provider-neutral shape Service acts on: the outcome of a
// verified webhook, with the provider's own object model already decoded away.
// The zero value is an ignored event, so a provider that returns providerEvent{}
// is safely a no-op.
type providerEvent struct {
	kind           eventKind
	teamID         string // our workspace id (from the checkout metadata we set)
	planCode       string // our plan code (from the checkout metadata we set)
	customerID     string // the provider's customer id
	subscriptionID string // the provider's subscription id — the stable lifecycle key
}

// webhookParser verifies a raw webhook request and returns the normalized event.
// Verification always comes first: an unsigned or mis-signed payload is a non-nil
// error and nothing downstream runs. Headers is passed whole because providers
// differ — Stripe carries one Stripe-Signature header, Polar carries the three
// Standard-Webhooks headers — so the seam stays provider-agnostic.
type webhookParser interface {
	parseWebhook(payload []byte, headers http.Header) (providerEvent, error)
}
