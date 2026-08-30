// Package usage meters and enforces per-workspace request quotas. A free
// project allows a fixed number of metered MCP requests per calendar month
// (10,000 by default); this package counts them and decides when a workspace
// has hit its ceiling. It is a separate bounded context so the cap policy lives
// in one place and both the MCP tools (enforcement) and the dashboard
// (display) read from the same counters.
package usage

import (
	"context"
	"fmt"
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

// fixedCapLookup is the optional half of CapLookup that says a cap is a
// deployment policy. A CapLookup that does not implement it is plan-derived,
// which is the pre-existing behaviour and the right default for a lookup written
// elsewhere.
type fixedCapLookup interface {
	capIsFixed() bool
}

// FixedCap is a CapLookup that answers the same cap for every workspace,
// ignoring plans entirely.
//
// It is what makes the cap configurable for a self-hosted install. The plan-derived
// cap prices a hosted service; a server running on someone's own machine has no
// billing relationship to price, and before this the only way to lift the seeded
// Free plan's 10,000/month was the `set-plan` superadmin CLI, per workspace.
//
// Deliberately a decorator rather than a branch inside Service: enforcement then
// has exactly one shape — ask the CapLookup — and the choice of policy is made
// once, at wiring time, where an operator's configuration is already being read.
// A branch inside Allow would have to be repeated in Snapshot, and the two
// disagreeing is how a dashboard comes to show a limit that is not enforced.
//
// A negative value means unlimited, matching what Service already does with any
// cap <= 0 and what the Unlimited plan row carries.
type FixedCap int

// MonthlyCap returns the fixed cap. The context and workspace are ignored by
// construction: this is a process-wide deployment policy, and consulting the
// workspace here would make it something else.
func (c FixedCap) MonthlyCap(context.Context, string) (int, error) { return int(c), nil }

// capIsFixed marks this cap as deployment policy so Status can carry the source
// to the surfaces that advise a capped caller what to do next.
func (c FixedCap) capIsFixed() bool { return true }

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

	// CapFixed says the cap came from deployment configuration rather than from
	// the workspace's plan, so nothing the workspace can BUY will move it.
	//
	// It exists because every surface that tells a capped caller what to do next
	// was written when the plan was the only source: the MCP rejection says
	// "upgrade the project's plan" and the dashboard offers a checkout button.
	// Under a fixed cap both are advice that cannot work, and the dashboard's
	// version is worse than useless — a user can pay, teams.plan_id can flip
	// successfully, and the enforced cap does not move. The value alone cannot
	// tell you that; only its source can.
	CapFixed bool
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

// CapRejection is the one sentence a caller gets when the monthly cap refuses
// the request, and it branches on WHERE the cap came from because only the
// source decides which remedy can work.
//
// It lives on Status rather than beside a caller because there are two callers —
// the MCP tool rejection and the /import handler — and one copy per caller is
// how this went wrong the first time. The MCP branch learned about
// deployment-fixed caps while /import went on telling a self-hosted operator to
// upgrade a plan that cannot move their cap and, with billing switched off, does
// not exist to buy. The sentence is a property of the Status, so the Status owns
// it and a third surface cannot drift from the other two.
func (s Status) CapRejection() string {
	remedy := "upgrade the project's plan"
	if s.CapFixed {
		remedy = "this cap is set by the deployment, not by the plan — raise --monthly-request-cap (or AGENTSMEMORY_MONTHLY_REQUEST_CAP) on the server"
	}
	return fmt.Sprintf("monthly request cap reached (%d/%d) — %s", s.Used, s.Cap, remedy)
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
	fixed := s.capIsFixed()
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
			return Status{Used: current, Cap: limit, Allowed: false, CapFixed: fixed}, nil
		}
	}
	used, err := s.repo.increment(ctx, teamID, period)
	if err != nil {
		return Status{}, err
	}
	return Status{Used: used, Cap: limit, Allowed: true, CapFixed: fixed}, nil
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
	return Status{Used: used, Cap: limit, Allowed: limit <= 0 || used < limit, CapFixed: s.capIsFixed()}, nil
}

// capIsFixed reports whether this Service's cap is deployment policy. Asked once
// per call rather than cached so a Service built with a different lookup can
// never report a stale source.
func (s *Service) capIsFixed() bool {
	f, ok := s.caps.(fixedCapLookup)
	return ok && f.capIsFixed()
}
