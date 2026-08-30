package billing

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"gorm.io/gorm"
)

// Open Collective's OrderStatus enum, read in full from the live schema on
// 2026-08-28 (all fourteen values). They are grouped here by what they mean for a
// workspace's plan, and the grouping is the judgement in this file — the names are
// someone else's state machine, so each group says why.
//
// ACTIVATING: the money is real and the contribution is live. ACTIVE is a running
// recurring contribution; PAID is a completed one-off or a settled charge.
//
// CANCELLING: the contribution has stopped or been undone, so the workspace must
// return to Free. REJECTED and EXPIRED never became money; REFUNDED and CANCELLED
// stopped being money.
//
// IGNORED: everything in flight, under review, or paused. ⚠ These are deliberately
// NOT cancellations. The failure mode of ignoring a state is a workspace that keeps
// Pro slightly too long; the failure mode of cancelling on one is a paying customer
// downgraded mid-retry, which is strictly worse and much harder to notice.
// ERROR sits here for exactly that reason: an errored order is not a paid one, but
// it is also not evidence the customer left.
var ocStatusKind = map[string]eventKind{
	"ACTIVE":                      eventActivated,
	"PAID":                        eventActivated,
	"CANCELLED":                   eventCanceled,
	"EXPIRED":                     eventCanceled,
	"REFUNDED":                    eventCanceled,
	"REJECTED":                    eventCanceled,
	"NEW":                         eventIgnored,
	"PENDING":                     eventIgnored,
	"PROCESSING":                  eventIgnored,
	"REQUIRE_CLIENT_CONFIRMATION": eventIgnored,
	"DISPUTED":                    eventIgnored,
	"IN_REVIEW":                   eventIgnored,
	"PAUSED":                      eventIgnored,
	"ERROR":                       eventIgnored,
}

// kindForStatus maps a provider status onto our event vocabulary. An UNKNOWN status
// — one Open Collective adds after this was written — is ignored and logged, never
// treated as a cancellation: a new state name must not be able to silently downgrade
// paying workspaces.
func kindForStatus(status string) eventKind {
	if k, ok := ocStatusKind[status]; ok {
		return k
	}
	log.Printf("billing: unknown opencollective order status %q; ignoring (a new status must never be read as a cancellation)", status)
	return eventIgnored
}

// intentMatcher is the read half of the intent store, declared at the consumer so
// the reconciler depends on the two lookups it performs.
type intentMatcher interface {
	MatchByTag(ctx context.Context, tag, planCode string) (CheckoutIntent, error)
	MatchByEmail(ctx context.Context, email, planCode string) (CheckoutIntent, error)
}

// AppliedOrder records that a provider order was acted on in a particular state.
type AppliedOrder struct {
	OrderID   string `gorm:"primaryKey"`
	Status    string
	TeamID    string
	AppliedAt string
}

// TableName pins the gorm model to the goose-managed table.
func (AppliedOrder) TableName() string { return "billing_applied_orders" }

// AppliedOrderRepo is the ledger of orders reconciliation has already acted on.
// It is what makes a POLLING reconciler idempotent over time, as opposed to
// idempotent within one pass.
//
// A webhook fires once per event, so "apply what the event says" is safe. A poll
// sees the same order every interval forever, so without this the only idempotence
// is "the provider's state has not changed" — which re-asserts a past decision over
// any local change made since it. That is not theoretical: it silently reverted an
// operator's `set-plan` downgrade on the next pass (PR #96 review, B1).
type AppliedOrderRepo struct{ db *gorm.DB }

// NewAppliedOrderRepo constructs the ledger over an open gorm connection.
func NewAppliedOrderRepo(db *gorm.DB) *AppliedOrderRepo { return &AppliedOrderRepo{db: db} }

// AlreadyApplied reports whether this exact (order, status) pair has been acted on.
// A genuine transition — ACTIVE then later CANCELLED — is NOT already applied and
// still takes effect; only a repeat of the same state is skipped.
//
// A lookup error answers false: failing open re-applies a decision that is at worst
// redundant, where failing closed would silently stop activating paying customers.
func (r *AppliedOrderRepo) AlreadyApplied(ctx context.Context, orderID, status string) bool {
	if r == nil || orderID == "" {
		return false
	}
	var row AppliedOrder
	if err := r.db.WithContext(ctx).Where("order_id = ?", orderID).First(&row).Error; err != nil {
		return false
	}
	return row.Status == status
}

// MarkApplied records that an order was acted on in this state, replacing any
// earlier state for the same order.
func (r *AppliedOrderRepo) MarkApplied(ctx context.Context, orderID, status, teamID string) error {
	if r == nil || orderID == "" {
		return nil
	}
	return r.db.WithContext(ctx).Exec(`
		INSERT INTO billing_applied_orders (order_id, status, team_id, applied_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(order_id) DO UPDATE SET
			status     = excluded.status,
			team_id    = excluded.team_id,
			applied_at = excluded.applied_at`,
		orderID, status, teamID, time.Now().UTC().Format(time.RFC3339)).Error
}

// ReconcileReport counts what one pass saw. It exists so the caller can log a
// number rather than a silence: "0 orders" and "the call failed" must never produce
// the same log line, which is the same empty-reads-as-an-answer trap the order
// source guards at the transport level.
type ReconcileReport struct {
	Seen         int
	Activated    int
	Canceled     int
	Ignored      int
	Unattributed int
}

// Reconciler turns contributions read from the provider into plan changes. It owns
// no plan-flip logic of its own: it maps each order onto a providerEvent and hands
// it to the same applyActivated / applyCanceled the Stripe webhook uses, so there is
// exactly one implementation of "what a payment does to a workspace" (ADR-042).
type Reconciler struct {
	svc            *Service
	orders         orderSource
	intents        intentMatcher
	applied        *AppliedOrderRepo // what this server has already acted on
	planByTierSlug map[string]string // Open Collective tier slug -> our plan code
}

// WithLedger attaches the applied-order ledger, which is what stops a poll from
// re-applying a decision every interval. Optional so a Reconciler can still be
// constructed in a test without one; production always has it.
func (r *Reconciler) WithLedger(a *AppliedOrderRepo) *Reconciler {
	r.applied = a
	return r
}

// NewReconciler builds a Reconciler. planByTierSlug maps the provider's tier slugs
// onto our sellable plan codes; an order naming a tier that is not in the map is
// ignored, because we cannot say what was bought.
//
// Keyed on the SLUG rather than the numeric tier id: both identify a tier, but only
// the slug is in Open Collective's published schema (`Tier.legacyId` works and is
// absent from introspection on both prod and staging, checked 2026-08-28), and a
// slug is legible in a log line where a bare integer is not.
func NewReconciler(svc *Service, orders orderSource, intents intentMatcher, planByTierSlug map[string]string) *Reconciler {
	return &Reconciler{svc: svc, orders: orders, intents: intents, planByTierSlug: planByTierSlug}
}

// ReconcileOnce reads every incoming contribution and applies the ones it can
// attribute. It is idempotent by construction — it re-reads the same orders every
// pass and converges on the same state, because the plan flip and the subscription
// upsert it delegates to are both idempotent.
//
// A failure to read is returned; a failure to apply ONE order is logged and the pass
// continues, because one unattributable or malformed contribution must not stop
// every other workspace from being activated.
func (r *Reconciler) ReconcileOnce(ctx context.Context) (ReconcileReport, error) {
	var rep ReconcileReport
	orders, err := r.orders.listOrders(ctx)
	if err != nil {
		return rep, err
	}
	rep.Seen = len(orders)
	// Oldest first, so when one workspace has several orders the NEWEST state is the
	// one applied last and the pass converges on it. Without this the outcome depends
	// on the order the API happened to return: an old CANCELLED processed after a new
	// ACTIVE would end the pass with the workspace downgraded (PR #96 review, A2).
	sort.SliceStable(orders, func(i, j int) bool { return orders[i].CreatedAt < orders[j].CreatedAt })
	for _, o := range orders {
		kind := kindForStatus(o.Status)
		if kind == eventIgnored {
			rep.Ignored++
			continue
		}
		planCode, ok := r.planByTierSlug[o.TierSlug]
		if !ok {
			// A contribution outside our sellable tiers — an ordinary donation. Not an
			// error, and not something to act on.
			rep.Ignored++
			continue
		}
		// Already acted on in this exact state: skip. This is what keeps a POLL
		// idempotent over time rather than only within a pass — see AppliedOrderRepo.
		if r.applied.AlreadyApplied(ctx, o.ID, o.Status) {
			rep.Ignored++
			continue
		}
		switch kind {
		case eventActivated:
			// A recurring plan needs a recurring contribution. A ONETIME order to a
			// monthly tier stays PAID upstream forever, so granting Pro from one would
			// grant it for as long as the collective exists — and nothing expires it,
			// because CurrentPeriodEnd is recorded and read by nothing (PR #96 review,
			// B1). Ignored and logged rather than half-honoured.
			//
			// ⚠ An ABSENT frequency is treated as not-recurring, not as permission.
			// `Order.frequency` is nullable in the published schema, so "we cannot tell
			// whether this recurs" is a state the API can really produce — and admitting
			// it would defeat this guard in precisely the case it exists for. The cost of
			// refusing is a log line and one `set-plan`; the cost of admitting is a
			// permanent plan nobody is billed for and nobody notices. An earlier version
			// let an empty value through, which was not a decision — it was nine test
			// fixtures that omitted the field.
			if o.Frequency != "MONTHLY" && o.Frequency != "YEARLY" {
				log.Printf("billing: contribution %s is %s to the recurring tier %q; ignoring — a one-off does not buy a subscription, activate manually with `set-plan` if that is the intent", o.ID, o.Frequency, o.TierSlug)
				rep.Ignored++
				continue
			}
			teamID, attributed := r.attribute(ctx, o, planCode)
			if !attributed {
				rep.Unattributed++
				continue
			}
			applied, err := r.svc.applyActivated(ctx, providerEvent{
				kind: eventActivated, teamID: teamID, planCode: planCode,
				customerID: o.FromAccountSlug, subscriptionID: o.ID,
			})
			if err != nil {
				log.Printf("billing: reconcile activate order %s: %v", o.ID, err)
				continue
			}
			if !applied {
				// The stale-re-delivery guard declined it — a success that changed
				// nothing. Not recorded: the ledger says what this server DID, and
				// writing an entry here would claim a decision it refused to take.
				rep.Ignored++
				continue
			}
			// nextChargeDate is the paid-through date; recording it is what finally
			// populates a column that has existed and been written by nothing.
			if o.NextChargeDate != "" {
				r.recordPeriodEnd(ctx, teamID, o.NextChargeDate)
			}
			r.markApplied(ctx, o, teamID)
			rep.Activated++
		case eventCanceled:
			// A cancellation needs no attribution: the order id is the stable key and
			// applyCanceled looks the workspace up by it. An id we never recorded is a
			// no-op there, which is the correct answer for someone else's contribution.
			downgraded, err := r.svc.applyCanceled(ctx, providerEvent{
				kind: eventCanceled, subscriptionID: o.ID,
			})
			if err != nil {
				log.Printf("billing: reconcile cancel order %s: %v", o.ID, err)
				continue
			}
			if downgraded == "" {
				// A cancellation for an order we never recorded — somebody else's
				// contribution to the same collective. Nothing happened, so nothing is
				// written to the ledger and no empty team id is stored with it.
				rep.Ignored++
				continue
			}
			r.markApplied(ctx, o, downgraded)
			rep.Canceled++
		}
	}
	return rep, nil
}

// attribute decides which workspace a contribution belongs to, and refuses to guess.
//
// The order is deliberate. A `tags` value we put on the checkout URL is the primary
// channel, but a URL is user-controlled, so a tag ALONE is never attribution: it
// must resolve to a CheckoutIntent this server recorded for that plan. Without that
// corroboration anyone could tag a payment with someone else's workspace. The
// contributor's email is the fallback for when the tag does not survive the hosted
// checkout. When neither resolves, the answer is "we do not know" — the order is
// counted as unattributed, logged once, and left entirely alone for an operator to
// settle with set-plan.
func (r *Reconciler) attribute(ctx context.Context, o providerOrder, planCode string) (teamID string, ok bool) {
	for _, tag := range o.Tags {
		intent, err := r.intents.MatchByTag(ctx, tag, planCode)
		if err == nil {
			return intent.TeamID, true
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("billing: reconcile tag lookup for order %s: %v", o.ID, err)
		}
	}
	if o.FromAccountEmail != "" {
		intent, err := r.intents.MatchByEmail(ctx, o.FromAccountEmail, planCode)
		if err == nil {
			return intent.TeamID, true
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("billing: reconcile email lookup for order %s: %v", o.ID, err)
		}
	}
	log.Printf("billing: contribution %s (%s) matches no checkout intent; left for manual attribution with `set-plan`", o.ID, planCode)
	return "", false
}

// Run drives ReconcileOnce until the context is cancelled. It is the only loop in
// this server, so its failure behaviour is stated rather than assumed:
//
//   - A reconcile error is LOGGED and retried at the next tick, never fatal. A
//     payment provider being unreachable must not take the server down.
//   - A panic is recovered at the loop boundary for the same reason: one malformed
//     order must not kill the process.
//   - Every pass logs its counts. "0 orders" and "the call failed" are different
//     lines, because a silent zero is indistinguishable from a working system with
//     no customers — which is exactly the state this project is in today.
//   - The first pass runs immediately, so a restart picks up anything that arrived
//     while the process was down rather than waiting a full interval.
func (r *Reconciler) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		// ⚠ The recover is INSIDE the loop, per pass. At the function boundary it
		// caught the panic and then returned — so the process survived and the loop
		// was gone, with one log line and nothing to restart it. Activation would be
		// dead until someone redeployed (PR #96 review, A1). Losing a pass is
		// recoverable; losing the loop is not, and both look identical in a log.
		r.runOnce(ctx, every)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// runOnce performs a single pass and reports it, converting a panic into a lost
// pass rather than a lost loop.
func (r *Reconciler) runOnce(ctx context.Context, every time.Duration) {
	defer func() {
		if v := recover(); v != nil {
			log.Printf("billing: reconcile pass panicked: %v (the loop continues; this pass applied nothing)", v)
		}
	}()
	rep, err := r.ReconcileOnce(ctx)
	switch {
	case ctx.Err() != nil:
		return
	case err != nil:
		log.Printf("billing: reconcile failed: %v (retrying in %s)", err, every)
	default:
		log.Printf("billing: reconciled %d order(s): %d activated, %d canceled, %d ignored, %d unattributed",
			rep.Seen, rep.Activated, rep.Canceled, rep.Ignored, rep.Unattributed)
	}
}

// markApplied records that an order was acted on, so the next pass skips it. A
// failure is logged and not fatal: the cost is re-applying next interval, which is
// the pre-ledger behaviour, and refusing to continue would stop other workspaces
// being activated over a bookkeeping error.
func (r *Reconciler) markApplied(ctx context.Context, o providerOrder, teamID string) {
	if err := r.applied.MarkApplied(ctx, o.ID, o.Status, teamID); err != nil {
		log.Printf("billing: recording applied order %s: %v (it may be re-applied next pass)", o.ID, err)
	}
}

// recordPeriodEnd stores the paid-through date on the workspace's subscription. It
// is best-effort and deliberately separate from the plan flip: the plan being right
// matters, and a missing period-end is cosmetic until something reads it.
func (r *Reconciler) recordPeriodEnd(ctx context.Context, teamID, until string) {
	sub, err := r.svc.subs.ByTeam(ctx, teamID)
	if err != nil {
		return
	}
	sub.CurrentPeriodEnd = until
	if err := r.svc.subs.Upsert(ctx, sub); err != nil {
		log.Printf("billing: recording period end for team %s: %v", teamID, err)
	}
}
