package billing

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Subscription is the durable record of a workspace's Stripe relationship: which
// plan it is paying for, on what Stripe objects, and the current billing window.
// It is the billing context's own model; teams.plan_id (owned by tenant) remains
// the *effective* plan the metering path reads. There is at most one row per
// team (team_id is unique) — a new checkout updates the same row rather than
// stacking subscriptions.
type Subscription struct {
	ID                   string `gorm:"primaryKey"`
	TeamID               string `gorm:"uniqueIndex"`
	PlanID               string
	Status               string // active | past_due | canceled | incomplete
	StripeCustomerID     string
	StripeSubscriptionID string
	CurrentPeriodEnd     string // RFC3339; end of the paid window (best-effort)
	CreatedAt            string
	UpdatedAt            string
}

// TableName pins the gorm model to the goose-managed table.
func (Subscription) TableName() string { return "subscriptions" }

// Repo persists subscriptions over a gorm connection. Consumers depend on its
// methods, not on gorm directly.
type Repo struct{ db *gorm.DB }

// NewRepo constructs a Repo over an open gorm connection.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Upsert records a subscription for a team, creating it on first sight and
// updating the existing row otherwise (team_id is unique). It is the single
// write the webhook makes against billing state, so it must be idempotent AND
// safe under Stripe's concurrent re-delivery: two duplicate deliveries can race,
// so the create-or-update is one atomic INSERT … ON CONFLICT(team_id) rather than
// a read-then-create (which could have both deliveries miss the row and collide
// on Create). created_at is written only on insert — left untouched on conflict —
// so the original creation time survives updates.
func (r *Repo) Upsert(ctx context.Context, sub Subscription) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if sub.ID == "" {
		sub.ID = uuid.NewString() // used only if this is an insert
	}
	return r.db.WithContext(ctx).Exec(`
		INSERT INTO subscriptions
			(id, team_id, plan_id, status, stripe_customer_id, stripe_subscription_id, current_period_end, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(team_id) DO UPDATE SET
			plan_id                = excluded.plan_id,
			status                 = excluded.status,
			stripe_customer_id     = excluded.stripe_customer_id,
			stripe_subscription_id = excluded.stripe_subscription_id,
			current_period_end     = excluded.current_period_end,
			updated_at             = excluded.updated_at`,
		sub.ID, sub.TeamID, sub.PlanID, sub.Status,
		sub.StripeCustomerID, sub.StripeSubscriptionID, sub.CurrentPeriodEnd,
		now, now).Error
}

// ByTeam returns a workspace's subscription, or gorm.ErrRecordNotFound if it has
// never subscribed. Used by the dashboard to show the current paid status.
func (r *Repo) ByTeam(ctx context.Context, teamID string) (Subscription, error) {
	var sub Subscription
	return sub, r.db.WithContext(ctx).Where("team_id = ?", teamID).First(&sub).Error
}

// ByStripeSubID finds a subscription by its Stripe subscription id — the stable
// key carried on every lifecycle event — so a cancellation webhook can locate the
// workspace to downgrade. Returns gorm.ErrRecordNotFound when unknown.
func (r *Repo) ByStripeSubID(ctx context.Context, stripeSubID string) (Subscription, error) {
	var sub Subscription
	return sub, r.db.WithContext(ctx).
		Where("stripe_subscription_id = ?", stripeSubID).First(&sub).Error
}
