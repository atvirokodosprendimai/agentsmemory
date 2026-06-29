package billing

import (
	"context"
	"errors"
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
// write the webhook makes against billing state, so it must be idempotent: a
// re-delivered "completed" event simply rewrites the same row with the same
// values. The caller supplies all fields except id/timestamps, which Upsert
// manages — preserving the original CreatedAt across updates.
func (r *Repo) Upsert(ctx context.Context, sub Subscription) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var existing Subscription
	err := r.db.WithContext(ctx).Where("team_id = ?", sub.TeamID).First(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		sub.ID = uuid.NewString()
		sub.CreatedAt = now
		sub.UpdatedAt = now
		return r.db.WithContext(ctx).Create(&sub).Error
	case err != nil:
		return err
	default:
		// Keep the row's identity and creation time; refresh the mutable fields.
		sub.ID = existing.ID
		sub.CreatedAt = existing.CreatedAt
		sub.UpdatedAt = now
		return r.db.WithContext(ctx).Save(&sub).Error
	}
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
