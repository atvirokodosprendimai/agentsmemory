package mergejob

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Status is the lifecycle of a merge job: queued by the dashboard, claimed by the
// worker (running), then done or failed.
type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Job is one durable background wing-merge: fold the source wings into target for
// a team, then rebuild the derived graph. It carries its own outcome (counts /
// error) so the dashboard can show progress without re-deriving anything.
type Job struct {
	ID          string   `gorm:"primaryKey"`
	TeamID      string   // workspace the merge runs in
	Sources     []string `gorm:"serializer:json"` // source wing names folded into Target
	Target      string   // wing they are folded into
	Status      string   // one of Status
	RequestedBy string   // user id who queued it
	Drawers     int64    // drawers relabeled (set on done)
	Closets     int64    // closets relabeled (set on done)
	Error       string   // failure message when Status == failed
	CreatedAt   string
	StartedAt   *string // when the worker claimed it
	FinishedAt  *string // when it reached done/failed
}

// TableName pins the gorm model to the goose-managed table.
func (Job) TableName() string { return "merge_jobs" }

// Repo persists merge jobs against the shared SQLite database.
type Repo struct{ db *gorm.DB }

// NewRepo wires a Repo to the open gorm handle.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Create inserts a queued job.
func (r *Repo) Create(ctx context.Context, job *Job) error {
	return r.db.WithContext(ctx).Create(job).Error
}

// ListForTeam returns a team's jobs newest first, capped at limit (0 = no cap) —
// the dashboard's recent-jobs panel.
func (r *Repo) ListForTeam(ctx context.Context, teamID string, limit int) ([]Job, error) {
	var jobs []Job
	q := r.db.WithContext(ctx).Where("team_id = ?", teamID).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	return jobs, q.Find(&jobs).Error
}

// ClaimNext atomically claims the oldest queued job for the worker, transitioning
// it queued -> running. It returns ok=false (nil error) when there is nothing to
// claim, or when another worker won the row first (the conditional UPDATE matched
// no row) — both mean "try again next cycle". The status='queued' predicate is the
// optimistic lock, so two workers can never run the same job.
func (r *Repo) ClaimNext(ctx context.Context) (Job, bool, error) {
	var job Job
	err := r.db.WithContext(ctx).
		Where("status = ?", string(StatusQueued)).
		Order("created_at").
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res := r.db.WithContext(ctx).Model(&Job{}).
		Where("id = ? AND status = ?", job.ID, string(StatusQueued)).
		Updates(map[string]any{"status": string(StatusRunning), "started_at": now})
	if res.Error != nil {
		return Job{}, false, res.Error
	}
	if res.RowsAffected != 1 {
		return Job{}, false, nil // lost the claim race; retry next cycle
	}
	job.Status = string(StatusRunning)
	job.StartedAt = &now
	return job, true, nil
}

// MarkDone records a successful merge: counts, finished time, status done. The
// status='running' guard means only the job the worker is actually finalizing is
// updated — a job reclaimed back to queued (see ReleaseRunning) is never silently
// marked done.
func (r *Repo) MarkDone(ctx context.Context, id string, drawers, closets int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return r.db.WithContext(ctx).Model(&Job{}).
		Where("id = ? AND status = ?", id, string(StatusRunning)).
		Updates(map[string]any{
			"status":      string(StatusDone),
			"drawers":     drawers,
			"closets":     closets,
			"finished_at": now,
			"error":       "",
		}).Error
}

// MarkFailed records a failure message and finished time, status failed (guarded
// to the running job, as MarkDone).
func (r *Repo) MarkFailed(ctx context.Context, id, msg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return r.db.WithContext(ctx).Model(&Job{}).
		Where("id = ? AND status = ?", id, string(StatusRunning)).
		Updates(map[string]any{
			"status":      string(StatusFailed),
			"error":       msg,
			"finished_at": now,
		}).Error
}

// ReleaseRunning resets any job stuck in 'running' back to 'queued', clearing its
// started_at. The worker calls it once at startup: with a single worker, any row
// found 'running' at boot is necessarily orphaned by a previous process that died
// mid-job (claimed but never finalized), so reclaiming it is what lets the durable
// queue actually resume mid-flight work rather than stranding it forever. Returns
// how many it released.
func (r *Repo) ReleaseRunning(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Model(&Job{}).
		Where("status = ?", string(StatusRunning)).
		Updates(map[string]any{"status": string(StatusQueued), "started_at": nil})
	return res.RowsAffected, res.Error
}
