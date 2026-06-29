// Package billing is the payments bounded context: it turns a workspace's
// "upgrade to Pro" click into a Stripe hosted-Checkout session, and turns the
// resulting signed Stripe webhook back into a plan change on that workspace.
//
// It is deliberately thin and isolated. The relational source of truth for "what
// plan is a workspace on" stays teams.plan_id (owned by tenant); billing only
// flips that value and records the durable Stripe relationship in its own
// subscriptions table. Card data never touches this server — hosted Checkout
// keeps the whole PCI surface on Stripe — so billing's only untrusted input is
// the webhook, which is verified by signature before anything is acted on.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
	"gorm.io/gorm"
)

// Config carries the process-level Stripe wiring, resolved from the environment.
// PriceByPlanCode maps our sellable plan codes (e.g. "pro_monthly") to the Stripe
// Price ids that back them; the ids are environment-specific (test vs live) so
// they live in config, not in the seeded plan catalog.
type Config struct {
	SecretKey       string
	WebhookSecret   string
	PriceByPlanCode map[string]string
}

// PlanStore is the slice of tenant state billing needs: resolve a sellable plan
// by code, and set a workspace's effective plan. *tenant.Repo satisfies it. The
// interface is declared here, at the consumer, so billing depends on the two
// methods it uses rather than on tenant's whole repo.
type PlanStore interface {
	PlanByCode(ctx context.Context, code string) (tenant.Plan, error)
	SetTeamPlan(ctx context.Context, teamID, planID string) error
}

// Service is the billing use-case layer: start a checkout, handle a webhook.
type Service struct {
	cfg      Config
	plans    PlanStore
	subs     *Repo
	checkout checkoutAPI
}

// NewService wires a Service. Setting the secret key configures the SDK's
// process-wide client (one Stripe account per process). A Service is usable for
// checkout only when PriceByPlanCode is populated and for webhooks only when
// WebhookSecret is set; main.go constructs it only when Stripe is configured, so
// the dashboard degrades gracefully (no upgrade button) when it is not.
func NewService(cfg Config, plans PlanStore, subs *Repo) *Service {
	if cfg.SecretKey != "" {
		stripe.Key = cfg.SecretKey
	}
	return &Service{cfg: cfg, plans: plans, subs: subs, checkout: stripeClient{}}
}

// Enabled reports whether checkout can actually run — a Stripe secret key plus
// at least one priced plan. main.go always constructs the Service (so the webhook
// route and dashboard wiring are simple), so this is the runtime gate the
// dashboard checks before offering an upgrade control. A nil Service is treated
// as disabled so callers needn't nil-check.
func (s *Service) Enabled() bool {
	return s != nil && s.cfg.SecretKey != "" && len(s.cfg.PriceByPlanCode) > 0
}

// CheckoutRequest is the input to StartCheckout: which workspace is buying which
// plan, plus where Stripe should return the user afterwards.
type CheckoutRequest struct {
	TeamID        string
	PlanCode      string
	CustomerEmail string
	SuccessURL    string
	CancelURL     string
}

// ErrUnknownPlan is returned when a checkout is requested for a plan code that
// has no configured Stripe price (or no catalog row) — a client asking to buy
// something we don't sell, treated as a bad request by the handler.
var ErrUnknownPlan = errors.New("billing: unknown or unpriced plan")

// StartCheckout creates a hosted Checkout Session for a workspace and returns the
// URL to redirect the user to. It refuses a plan code that has no Stripe price
// configured or no catalog row, so a tampered signal can only ever buy a real,
// sellable plan.
func (s *Service) StartCheckout(ctx context.Context, req CheckoutRequest) (string, error) {
	priceID := s.cfg.PriceByPlanCode[req.PlanCode]
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

// HandleWebhook verifies a Stripe webhook's signature and applies its effect.
// Verification comes first and always: an unsigned or mis-signed payload is
// rejected before a single byte is trusted (this is the package's only untrusted
// input). Recognised events flip the workspace's plan; unrecognised event types
// are a no-op success, so Stripe's broad event stream doesn't error the endpoint.
// All handling is idempotent — Stripe re-delivers, so the same event arriving
// twice must converge to the same state, not double-apply.
func (s *Service) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error {
	// Fail CLOSED on a missing signing secret. ConstructEvent with an empty secret
	// would verify an empty-key HMAC, which an attacker who knows the payload shape
	// could forge — so an unconfigured secret must reject every event, never accept
	// one. (main.go also warns at startup when the key is set but this is not.)
	if s.cfg.WebhookSecret == "" {
		return fmt.Errorf("billing: webhook secret not configured")
	}
	event, err := webhook.ConstructEvent(payload, sigHeader, s.cfg.WebhookSecret)
	if err != nil {
		return fmt.Errorf("billing: webhook signature: %w", err)
	}
	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted:
		return s.applyCheckoutCompleted(ctx, event.Data.Raw)
	case stripe.EventTypeCustomerSubscriptionDeleted:
		return s.applySubscriptionCanceled(ctx, event.Data.Raw)
	default:
		return nil
	}
}

// applyCheckoutCompleted upgrades the workspace named by the session's
// client_reference_id to the plan named in its metadata. Both come from the
// session WE created (Stripe echoes them back inside the signed event), so they
// are trustworthy here in a way the browser's success redirect is not. Missing
// attribution is an error, not a silent skip — it means a session we can't place.
func (s *Service) applyCheckoutCompleted(ctx context.Context, raw json.RawMessage) error {
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(raw, &sess); err != nil {
		return fmt.Errorf("billing: decode checkout session: %w", err)
	}
	teamID := sess.ClientReferenceID
	planCode := sess.Metadata["plan_code"]
	if teamID == "" || planCode == "" {
		return fmt.Errorf("billing: completed session missing team_id/plan_code")
	}
	// Guard against a stale or out-of-order re-delivery: if this exact Stripe
	// subscription is already recorded as canceled for the team, a late "completed"
	// event must NOT resurrect it to Pro. A genuinely new subscription has a
	// different id and still proceeds. (A processed-event-id ledger would generalise
	// this; the same-sub-canceled check covers the lifecycle race Stripe re-delivery
	// actually produces.)
	if sess.Subscription != nil {
		if existing, err := s.subs.ByTeam(ctx, teamID); err == nil &&
			existing.Status == "canceled" && existing.StripeSubscriptionID == sess.Subscription.ID {
			return nil
		}
	}
	plan, err := s.plans.PlanByCode(ctx, planCode)
	if err != nil {
		return fmt.Errorf("billing: resolve plan %q: %w", planCode, err)
	}
	// Flip the effective plan (idempotent: same plan id on re-delivery).
	if err := s.plans.SetTeamPlan(ctx, teamID, plan.ID); err != nil {
		return fmt.Errorf("billing: set team plan: %w", err)
	}
	sub := Subscription{
		TeamID: teamID,
		PlanID: plan.ID,
		Status: "active",
	}
	if sess.Customer != nil {
		sub.StripeCustomerID = sess.Customer.ID
	}
	if sess.Subscription != nil {
		sub.StripeSubscriptionID = sess.Subscription.ID
	}
	return s.subs.Upsert(ctx, sub)
}

// applySubscriptionCanceled downgrades a workspace back to the free plan when its
// Stripe subscription ends. The subscription id is the stable key, so we look up
// which workspace it belongs to; an unknown id (we never recorded it) is a no-op.
func (s *Service) applySubscriptionCanceled(ctx context.Context, raw json.RawMessage) error {
	var ssub stripe.Subscription
	if err := json.Unmarshal(raw, &ssub); err != nil {
		return fmt.Errorf("billing: decode subscription: %w", err)
	}
	existing, err := s.subs.ByStripeSubID(ctx, ssub.ID)
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
