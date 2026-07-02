package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
)

// stripeProvider is the Stripe implementation of the billing seams: it opens
// hosted Checkout Sessions and verifies + normalizes Stripe webhooks. It carries
// only the webhook signing secret; the API secret key is set process-wide on
// stripe.Key at construction (one Stripe account per process), matching the SDK's
// global-client convention.
type stripeProvider struct {
	webhookSecret string
}

// newStripeProvider builds the provider when Stripe is configured, returning nil
// when the secret key is unset so Service reports Enabled()==false and the
// dashboard shows no upgrade button. Setting stripe.Key configures the SDK's
// process-wide client.
func newStripeProvider(cfg Config) *stripeProvider {
	if cfg.StripeSecretKey == "" {
		return nil
	}
	stripe.Key = cfg.StripeSecretKey
	return &stripeProvider{webhookSecret: cfg.StripeWebhookSecret}
}

// createCheckout creates a subscription-mode Checkout Session and returns its
// hosted URL. client_reference_id and metadata carry the workspace and plan
// through Stripe so the completion webhook can attribute the payment without
// trusting anything the browser sends back on the success redirect.
func (stripeProvider) createCheckout(_ context.Context, in checkoutInput) (string, error) {
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

// parseWebhook verifies a Stripe webhook's signature and normalizes it into a
// providerEvent. Verification comes first and always: an unsigned or mis-signed
// payload is rejected before a single byte is trusted. Recognised events map to
// activated/canceled; every other event type is an ignored no-op.
func (p stripeProvider) parseWebhook(payload []byte, headers http.Header) (providerEvent, error) {
	// Fail CLOSED on a missing signing secret. ConstructEvent with an empty secret
	// would verify an empty-key HMAC, which an attacker who knows the payload shape
	// could forge — so an unconfigured secret must reject every event, never accept
	// one. (main.go also warns at startup when the key is set but this is not.)
	if p.webhookSecret == "" {
		return providerEvent{}, fmt.Errorf("billing: stripe webhook secret not configured")
	}
	event, err := webhook.ConstructEvent(payload, headers.Get("Stripe-Signature"), p.webhookSecret)
	if err != nil {
		return providerEvent{}, fmt.Errorf("billing: stripe webhook signature: %w", err)
	}
	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted:
		var sess stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
			return providerEvent{}, fmt.Errorf("billing: decode checkout session: %w", err)
		}
		// Attribution comes from the session WE created (Stripe echoes it back inside
		// the signed event). Service validates the fields are present before acting.
		evt := providerEvent{
			kind:     eventActivated,
			teamID:   sess.ClientReferenceID,
			planCode: sess.Metadata["plan_code"],
		}
		if sess.Customer != nil {
			evt.customerID = sess.Customer.ID
		}
		if sess.Subscription != nil {
			evt.subscriptionID = sess.Subscription.ID
		}
		return evt, nil
	case stripe.EventTypeCustomerSubscriptionDeleted:
		var ssub stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &ssub); err != nil {
			return providerEvent{}, fmt.Errorf("billing: decode subscription: %w", err)
		}
		return providerEvent{kind: eventCanceled, subscriptionID: ssub.ID}, nil
	default:
		return providerEvent{}, nil
	}
}
