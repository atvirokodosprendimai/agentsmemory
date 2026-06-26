// Package usage meters and enforces per-workspace request quotas. A free
// project allows a fixed number of metered MCP requests per calendar month
// (10,000 by default); this package counts them and decides when a workspace
// has hit its ceiling. It is a separate bounded context so the cap policy lives
// in one place and both the MCP tools (enforcement) and the dashboard
// (display) read from the same counters.
package usage

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Counter is the per-workspace, per-month request tally.
type Counter struct {
	TeamID    string `gorm:"primaryKey"`
	Period    string `gorm:"primaryKey"` // 'YYYY-MM' UTC
	Count     int
	UpdatedAt string
}

// TableName pins the gorm model to the goose-managed table.
func (Counter) TableName() string { return "usage_counters" }

// CurrentPeriod returns the current calendar-month key in UTC ('YYYY-MM'). The
// month boundary is UTC so usage windows are unambiguous across time zones.
func CurrentPeriod() string { return time.Now().UTC().Format("2006-01") }

// CapLookup yields a workspace's monthly request cap. *tenant.Repo implements
// it; defining it here keeps usage decoupled from tenant's models. A cap <= 0
// is treated as "no limit".
type CapLookup interface {
	MonthlyCap(ctx context.Context, teamID string) (int, error)
}

// Repo persists the counters.
type Repo struct{ db *gorm.DB }

// NewRepo constructs a Repo over an open gorm connection.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// get returns the current count for a workspace's period (0 if none yet).
func (r *Repo) get(ctx context.Context, teamID, period string) (int, error) {
	var c Counter
	err := r.db.WithContext(ctx).
		Where("team_id = ? AND period = ?", teamID, period).First(&c).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	return c.Count, err
}

// increment atomically bumps the counter for a workspace's period and returns
// the new value. The upsert keeps it to a single round-trip.
func (r *Repo) increment(ctx context.Context, teamID, period string) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var newCount int
	err := r.db.WithContext(ctx).Raw(`
		INSERT INTO usage_counters (team_id, period, count, updated_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(team_id, period) DO UPDATE SET
			count = count + 1,
			updated_at = excluded.updated_at
		RETURNING count`, teamID, period, now).Scan(&newCount).Error
	return newCount, err
}

// Status reports a workspace's standing for the current month.
type Status struct {
	Used    int  // requests consumed this month
	Cap     int  // monthly cap (0 = unlimited)
	Allowed bool // whether the request that produced this status is permitted
}

// Remaining returns how many requests are left this month (0 if unlimited cap).
func (s Status) Remaining() int {
	if s.Cap <= 0 {
		return 0
	}
	if s.Used >= s.Cap {
		return 0
	}
	return s.Cap - s.Used
}

// Service is the metering use-case layer.
type Service struct {
	repo *Repo
	caps CapLookup
}

// NewService wires a Service over the counter repo and a cap lookup.
func NewService(repo *Repo, caps CapLookup) *Service {
	return &Service{repo: repo, caps: caps}
}

// Allow records one metered request against a workspace and reports whether it
// is within the monthly cap. A request at or above the cap is refused WITHOUT
// being counted, so a blocked caller cannot inflate the tally. With no cap
// (unlimited) it always allows and still counts for analytics.
func (s *Service) Allow(ctx context.Context, teamID string) (Status, error) {
	period := CurrentPeriod()
	limit, err := s.caps.MonthlyCap(ctx, teamID)
	if err != nil {
		return Status{}, err
	}
	if limit > 0 {
		current, err := s.repo.get(ctx, teamID, period)
		if err != nil {
			return Status{}, err
		}
		if current >= limit {
			return Status{Used: current, Cap: limit, Allowed: false}, nil
		}
	}
	used, err := s.repo.increment(ctx, teamID, period)
	if err != nil {
		return Status{}, err
	}
	return Status{Used: used, Cap: limit, Allowed: true}, nil
}

// Snapshot reports current usage WITHOUT counting a request — for the dashboard.
func (s *Service) Snapshot(ctx context.Context, teamID string) (Status, error) {
	limit, err := s.caps.MonthlyCap(ctx, teamID)
	if err != nil {
		return Status{}, err
	}
	used, err := s.repo.get(ctx, teamID, CurrentPeriod())
	if err != nil {
		return Status{}, err
	}
	return Status{Used: used, Cap: limit, Allowed: limit <= 0 || used < limit}, nil
}
