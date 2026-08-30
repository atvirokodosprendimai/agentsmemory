package billing

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CheckoutIntent records that a workspace asked to buy a plan. It is the bridge
// across a payment we cannot instrument: OpenCollective's hosted contribution page
// carries no workspace id of its own and sends no signed webhook, so when an order
// is later read back from the API this table is what says whose it was (ADR-042).
//
// An intent is not a purchase. It may never be paid, may be abandoned, or may be
// repeated; the durable payment relationship stays in Subscription, written only by
// the plan-flip path.
type CheckoutIntent struct {
	ID        string `gorm:"primaryKey"`
	TeamID    string
	PlanCode  string
	Tag       string // the value placed in the checkout URL's `tags` parameter
	Email     string // prefilled on the checkout, and the fallback match key
	CreatedAt string
}

// TableName pins the gorm model to the goose-managed table.
func (CheckoutIntent) TableName() string { return "billing_checkout_intents" }

// intentRecorder is the write half of the intent store, declared at the consumer
// so Service depends on the one method it calls and can be handed a stub that
// fails. Recording must never be able to block a payment, and a test needs to be
// able to prove that.
type intentRecorder interface {
	Record(ctx context.Context, in CheckoutIntent) error
}

// IntentRepo persists checkout intents over a gorm connection.
type IntentRepo struct{ db *gorm.DB }

// NewIntentRepo constructs an IntentRepo over an open gorm connection.
func NewIntentRepo(db *gorm.DB) *IntentRepo { return &IntentRepo{db: db} }

// intentTag derives the value carried in the checkout URL's `tags` parameter for a
// workspace. It is a truncated base32 SHA-256 of the workspace id: stable, so a
// contribution made today still matches the intent recorded when the button was
// clicked; URL-safe without escaping, because it goes into a query string; and
// preimage-resistant, so the raw workspace id is not recoverable from a tag that is
// world-readable on a public contribution record.
//
// ⚠ It is a HASH, NOT A MAC. There is no secret, so anyone holding a workspace id
// can compute that workspace's tag — do not read "preimage-resistant" as
// "unforgeable" (PR #96 review, A3). It does not need to be unforgeable: the tag
// only selects WHICH intent to look for, and an intent this server recorded is what
// makes the attribution credible. The worst a forged tag achieves is paying for
// somebody else's workspace.
//
// ⚠ It is an ATTRIBUTION HINT, never an authorization. The tag travels in a URL the
// user controls, so anyone can present any tag. Reconciliation must therefore also
// require a matching CheckoutIntent row before acting on one — the tag says which
// workspace to look up, and the recorded intent is what makes the answer credible.
func intentTag(teamID string) string {
	sum := sha256.Sum256([]byte("agentsmemory-checkout-intent:" + teamID))
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return "am-" + strings.ToLower(enc[:16])
}

// Record stores an intent. Every click is a new row: a workspace may start checkout
// more than once, and the reconciler matches the most recent intent for the pair.
func (r *IntentRepo) Record(ctx context.Context, in CheckoutIntent) error {
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	if in.CreatedAt == "" {
		in.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return r.db.WithContext(ctx).Create(&in).Error
}

// MatchByTag finds the most recent intent for a tag and plan. An empty tag matches
// NOTHING and is refused before the query runs: an order that arrives with no tag
// would otherwise match every row whose column holds the empty string, and
// attribute a stranger's payment to an arbitrary workspace. This is the same
// empty-key trap applyCanceled already guards against for subscription ids.
func (r *IntentRepo) MatchByTag(ctx context.Context, tag, planCode string) (CheckoutIntent, error) {
	if tag == "" {
		return CheckoutIntent{}, gorm.ErrRecordNotFound
	}
	return r.first(ctx, "tag = ? AND plan_code = ?", tag, planCode)
}

// MatchByEmail is the fallback channel, used when a contribution carries no usable
// tag. It is weaker than the tag — a contributor may pay under a different address —
// so a miss here is a genuine "cannot attribute", not a reason to guess. An empty
// email is refused for the same reason as an empty tag.
//
// ⚠ It REFUSES AN AMBIGUOUS MATCH, and that is the point of it. Nothing ties an
// intent to the account that actually paid, so this lookup ranges over every
// workspace. Returning the newest of several meant one email with two workspaces
// sent the payment to whichever clicked Upgrade last — an ordinary support ticket
// for anyone with a personal and a team workspace, and worse than that given
// registration performs no email verification, so an address can be claimed by
// someone who does not own it (PR #96 review, B2).
//
// Refusing costs nothing when there is no ambiguity, and when there is, "we do not
// know" is the honest answer — the same answer this package already gives when
// neither channel resolves. The order then lands in the unattributed bucket for an
// operator to settle with `set-plan`.
func (r *IntentRepo) MatchByEmail(ctx context.Context, email, planCode string) (CheckoutIntent, error) {
	if email == "" {
		return CheckoutIntent{}, gorm.ErrRecordNotFound
	}
	var rows []CheckoutIntent
	if err := r.db.WithContext(ctx).
		Where("email = ? AND plan_code = ?", email, planCode).
		Distinct("team_id").
		Find(&rows).Error; err != nil {
		return CheckoutIntent{}, err
	}
	teams := map[string]struct{}{}
	for _, row := range rows {
		teams[row.TeamID] = struct{}{}
	}
	switch len(teams) {
	case 0:
		return CheckoutIntent{}, gorm.ErrRecordNotFound
	case 1:
		// Exactly one workspace ever asked to buy this plan with this address, so the
		// match is unambiguous. Re-read the newest row for that workspace so callers
		// get a fully populated record.
		return r.first(ctx, "email = ? AND plan_code = ?", email, planCode)
	default:
		return CheckoutIntent{}, fmt.Errorf("%w: %d workspaces have an open %s intent for that email; attributing none",
			gorm.ErrRecordNotFound, len(teams), planCode)
	}
}

// first returns the newest matching intent, or gorm.ErrRecordNotFound. Newest wins
// because a workspace that clicked twice and paid once should resolve to its latest
// stated intention.
func (r *IntentRepo) first(ctx context.Context, where string, args ...any) (CheckoutIntent, error) {
	var out CheckoutIntent
	err := r.db.WithContext(ctx).
		Where(where, args...).
		Order("created_at DESC").
		First(&out).Error
	return out, err
}
