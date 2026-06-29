package billing

import (
	"context"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
)

// checkoutInput is everything needed to open one hosted Checkout Session,
// expressed in billing's own terms so the Stripe SDK stays behind the interface.
type checkoutInput struct {
	PriceID       string // the Stripe Price the customer is subscribing to
	TeamID        string // workspace to upgrade; echoed back on the webhook
	PlanCode      string // our plan code, carried in metadata for the webhook
	CustomerEmail string // prefills Checkout; empty lets Stripe collect it
	SuccessURL    string
	CancelURL     string
}

// checkoutAPI is the one Stripe operation the upgrade flow performs: turn a price
// + workspace into a hosted Checkout URL to redirect the user to. It is an
// interface so Service can be unit-tested with a fake — no network, no keys.
type checkoutAPI interface {
	createCheckout(ctx context.Context, in checkoutInput) (redirectURL string, err error)
}

// stripeClient is the live checkoutAPI backed by stripe-go. It carries no state:
// the secret key is set process-wide on stripe.Key at service construction (one
// Stripe account per process), matching the SDK's global-client convention.
type stripeClient struct{}

// createCheckout creates a subscription-mode Checkout Session and returns its
// hosted URL. client_reference_id and metadata carry the workspace and plan
// through Stripe so the completion webhook can attribute the payment without
// trusting anything the browser sends back on the success redirect.
func (stripeClient) createCheckout(_ context.Context, in checkoutInput) (string, error) {
	params := &stripe.CheckoutSessionParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL:        stripe.String(in.SuccessURL),
		CancelURL:         stripe.String(in.CancelURL),
		ClientReferenceID: stripe.String(in.TeamID),
		LineItems: []*stripe.CheckoutSessionLineItemParams{{
			Price:    stripe.String(in.PriceID),
			Quantity: stripe.Int64(1),
		}},
	}
	if in.CustomerEmail != "" {
		params.CustomerEmail = stripe.String(in.CustomerEmail)
	}
	// Metadata is the trusted channel for the webhook: the success redirect is
	// attacker-controllable, but a signed webhook carrying these fields is not.
	params.AddMetadata("team_id", in.TeamID)
	params.AddMetadata("plan_code", in.PlanCode)

	sess, err := session.New(params)
	if err != nil {
		return "", err
	}
	return sess.URL, nil
}
