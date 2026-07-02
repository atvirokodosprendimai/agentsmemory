// Package billing is the payments bounded context: it turns a workspace's
// "upgrade to Pro" click into a hosted-checkout session, and turns the resulting
// signed webhook back into a plan change on that workspace.
//
// It is deliberately thin and isolated. The relational source of truth for "what
// plan is a workspace on" stays teams.plan_id (owned by tenant); billing only
// flips that value and records the durable payment relationship in its own
// subscriptions table. Card data never touches this server — hosted checkout
// keeps the whole PCI surface on the provider — so billing's only untrusted input
// is the webhook, which is verified by signature before anything is acted on.
//
// The payment provider is pluggable: Stripe or Polar, selected at construction by
// Config.Provider (decision 2026-07-02: run both, pick one per deployment with
// BILLING_PROVIDER). Provider-specific wire handling lives behind the checkoutAPI
// and webhookParser seams (see provider.go); everything in this file — resolving
// plans, flipping the effective plan, recording the subscription — is
// provider-agnostic.
package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"gorm.io/gorm"
)

// Config carries the process-level billing wiring, resolved from the environment.
// Provider selects which payment backend is live; only that backend's fields need
// to be set. PriceByPlanCode maps our sellable plan codes (e.g. "pro_monthly") to
// the *active provider's* price/product ids that back them; the ids are
// environment-specific (test vs live, Stripe price vs Polar product) so they live
// in config, not in the seeded plan catalog.
type Config struct {
	Provider        string            // "stripe" (default) | "polar"
	PriceByPlanCode map[string]string // active provider's price/product id by plan code

	// Stripe wiring (used when Provider == "stripe").
	StripeSecretKey     string
	StripeWebhookSecret string

	// Polar wiring (used when Provider == "polar").
	PolarAccessToken   string
	PolarWebhookSecret string
	PolarServer        string // "sandbox" | "production"
}

// PlanStore is the slice of tenant state billing needs: resolve a sellable plan
// by code, and set a workspace's effective plan. *tenant.Repo satisfies it. The
// interface is declared here, at the consumer, so billing depends on the two
// methods it uses rather than on tenant's whole repo.
type PlanStore interface {
	PlanByCode(ctx context.Context, code string) (tenant.Plan, error)
	SetTeamPlan(ctx context.Context, teamID, planID string) error
}

// Service is the billing use-case layer: start a checkout, handle a webhook. It
// depends only on the two provider seams (checkout, webhook) plus the plan store
// and subscription repo, so its logic is identical whichever provider is live.
type Service struct {
	priceByPlanCode map[string]string
	plans           PlanStore
	subs            *Repo
	checkout        checkoutAPI   // nil when the active provider is unconfigured
	webhook         webhookParser // nil when the active provider is unconfigured
	portal          portalAPI     // nil when the active provider is unconfigured
}

// NewService wires a Service around the provider named by cfg.Provider. The chosen
// provider is constructed only when its credentials are present (nil otherwise),
// so main.go can always build the Service and the dashboard degrades gracefully:
// with no provider configured, Enabled() is false and no upgrade button is shown.
func NewService(cfg Config, plans PlanStore, subs *Repo) *Service {
	s := &Service{priceByPlanCode: cfg.PriceByPlanCode, plans: plans, subs: subs}
	// One concrete provider implements both seams; assigning the same value to both
	// fields keeps checkout and webhook handling on the same backend.
	switch cfg.Provider {
	case ProviderPolar:
		if p := newPolarProvider(cfg); p != nil {
			s.checkout, s.webhook, s.portal = p, p, p
		}
	default: // ProviderStripe or unset — Stripe is the back-compatible default.
		if p := newStripeProvider(cfg); p != nil {
			s.checkout, s.webhook, s.portal = p, p, p
		}
	}
	return s
}

// Enabled reports whether checkout can actually run — a configured provider plus
// at least one priced plan. main.go always constructs the Service (so the webhook
// route and dashboard wiring stay simple), so this is the runtime gate the
// dashboard checks before offering an upgrade control. A nil Service is treated as
// disabled so callers needn't nil-check.
func (s *Service) Enabled() bool {
	return s != nil && s.checkout != nil && len(s.priceByPlanCode) > 0
}

// CheckoutRequest is the input to StartCheckout: which workspace is buying which
// plan, plus where the provider should return the user afterwards.
type CheckoutRequest struct {
	TeamID        string
	PlanCode      string
	CustomerEmail string
	SuccessURL    string
	CancelURL     string
}

// ErrUnknownPlan is returned when a checkout is requested for a plan code that has
// no configured provider price (or no catalog row) — a client asking to buy
// something we don't sell, treated as a bad request by the handler.
var ErrUnknownPlan = errors.New("billing: unknown or unpriced plan")

// StartCheckout creates a hosted checkout session for a workspace and returns the
// URL to redirect the user to. It refuses a plan code that has no provider price
// configured or no catalog row, so a tampered signal can only ever buy a real,
// sellable plan.
func (s *Service) StartCheckout(ctx context.Context, req CheckoutRequest) (string, error) {
	if s.checkout == nil {
		return "", fmt.Errorf("billing: no payment provider configured")
	}
	priceID := s.priceByPlanCode[req.PlanCode]
	if priceID == "" {
		return "", ErrUnknownPlan
	}
	// Confirm the plan exists in the catalog before charging for it.
	if _, err := s.plans.PlanByCode(ctx, req.PlanCode); err != nil {
		return "", fmt.Errorf("%w: %q", ErrUnknownPlan, req.PlanCode)
	}
	return s.checkout.createCheckout(ctx, checkoutInput{
		PriceID:       priceID,
		TeamID:        req.TeamID,
		PlanCode:      req.PlanCode,
		CustomerEmail: req.CustomerEmail,
		SuccessURL:    req.SuccessURL,
		CancelURL:     req.CancelURL,
	})
}

// ErrNoSubscription is returned by ManageURL when a workspace has no recorded
// provider customer to open a portal for — it never subscribed, or subscribed
// before we captured a customer id. The handler treats it as "nothing to manage".
var ErrNoSubscription = errors.New("billing: no subscription to manage")

// ManageURL returns a provider-hosted customer-portal URL where the workspace's
// admin can update payment, download invoices, or cancel. It resolves the
// workspace's provider customer id from its recorded subscription; any cancel made
// in the portal comes back as a webhook, so this only hands the user off — it never
// changes the plan itself. returnURL is where providers that support it send the
// user back.
func (s *Service) ManageURL(ctx context.Context, teamID, returnURL string) (string, error) {
	if s.portal == nil {
		return "", fmt.Errorf("billing: no payment provider configured")
	}
	sub, err := s.subs.ByTeam(ctx, teamID)
	if err != nil || sub.StripeCustomerID == "" {
		return "", ErrNoSubscription
	}
	return s.portal.createPortalSession(ctx, sub.StripeCustomerID, returnURL)
}

// HandleWebhook verifies a provider webhook and applies its effect. Verification
// happens inside the provider's parseWebhook and always comes first: an unsigned
// or mis-signed payload is rejected before a single byte is trusted (this is the
// package's only untrusted input). The normalized event drives an idempotent plan
// change; a verified-but-irrelevant event is a no-op success, so the provider's
// broad event stream doesn't error the endpoint. All handling is idempotent —
// providers re-deliver, so the same event arriving twice must converge to the
// same state, not double-apply.
func (s *Service) HandleWebhook(ctx context.Context, payload []byte, headers http.Header) error {
	if s.webhook == nil {
		// Fail closed: with no provider configured we cannot verify anything, so we
		// must reject rather than accept an unverifiable event.
		return fmt.Errorf("billing: no payment provider configured")
	}
	evt, err := s.webhook.parseWebhook(payload, headers)
	if err != nil {
		return err
	}
	switch evt.kind {
	case eventActivated:
		return s.applyActivated(ctx, evt)
	case eventCanceled:
		return s.applyCanceled(ctx, evt)
	default:
		return nil
	}
}

// applyActivated upgrades the workspace named in the event to the plan named in
// it. Both come from the checkout WE created (the provider echoes our metadata
// back inside the signed event), so they are trustworthy here in a way the
// browser's success redirect is not.
func (s *Service) applyActivated(ctx context.Context, evt providerEvent) error {
	if evt.teamID == "" || evt.planCode == "" {
		return fmt.Errorf("billing: activated event missing team_id/plan_code")
	}
	// Guard against a stale or out-of-order re-delivery: if this exact provider
	// subscription is already recorded as canceled for the team, a late "activated"
	// event must NOT resurrect it to Pro. A genuinely new subscription has a
	// different id and still proceeds. (A processed-event-id ledger would generalise
	// this; the same-sub-canceled check covers the lifecycle race re-delivery
	// actually produces.)
	if evt.subscriptionID != "" {
		if existing, err := s.subs.ByTeam(ctx, evt.teamID); err == nil &&
			existing.Status == "canceled" && existing.StripeSubscriptionID == evt.subscriptionID {
			return nil
		}
	}
	plan, err := s.plans.PlanByCode(ctx, evt.planCode)
	if err != nil {
		return fmt.Errorf("billing: resolve plan %q: %w", evt.planCode, err)
	}
	// Flip the effective plan (idempotent: same plan id on re-delivery).
	if err := s.plans.SetTeamPlan(ctx, evt.teamID, plan.ID); err != nil {
		return fmt.Errorf("billing: set team plan: %w", err)
	}
	return s.subs.Upsert(ctx, Subscription{
		TeamID:               evt.teamID,
		PlanID:               plan.ID,
		Status:               "active",
		StripeCustomerID:     evt.customerID,
		StripeSubscriptionID: evt.subscriptionID,
	})
}

// applyCanceled downgrades a workspace back to the free plan when its subscription
// ends. The subscription id is the stable key, so we look up which workspace it
// belongs to; an unknown id (we never recorded it) is a no-op.
func (s *Service) applyCanceled(ctx context.Context, evt providerEvent) error {
	// A cancellation with no subscription id has no stable key to attribute it. Never
	// query on the empty string: the subscriptions table's pre-provider rows default
	// stripe_subscription_id to '' and would match, downgrading the wrong workspace.
	if evt.subscriptionID == "" {
		return nil
	}
	existing, err := s.subs.ByStripeSubID(ctx, evt.subscriptionID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // nothing of ours to downgrade
	}
	if err != nil {
		return err
	}
	if err := s.plans.SetTeamPlan(ctx, existing.TeamID, tenant.FreePlanID); err != nil {
		return fmt.Errorf("billing: downgrade team plan: %w", err)
	}
	existing.PlanID = tenant.FreePlanID
	existing.Status = "canceled"
	return s.subs.Upsert(ctx, existing)
}
