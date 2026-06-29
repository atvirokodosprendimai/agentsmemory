package share

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Status is the lifecycle state of a share request: it starts pending and an
// admin of the destination resolves it to accepted (the copy ran) or declined.
type Status string

const (
	StatusPending  Status = "pending"
	StatusAccepted Status = "accepted"
	StatusDeclined Status = "declined"
)

// Request is one cross-workspace wing-share handshake row. It records WHO wants
// to copy WHICH wing from WHERE to WHERE, and how it resolved — never the copied
// memory itself (that is duplicated by palace.CopyWing on accept). The row is the
// consent record that keeps the GUI path from being a silent cross-tenant write.
type Request struct {
	ID          string `gorm:"primaryKey"`
	FromTeamID  string // source workspace (the wing's current home)
	ToTeamID    string // destination workspace (resolved from the typed slug)
	Wing        string // the wing to copy
	RequestedBy string // user id who initiated the push
	Status      string // one of Status
	CreatedAt   string
	ResolvedAt  *string // stamped when an admin accepts/declines; nil while pending
	ResolvedBy  *string // user id who resolved it; nil while pending
}

// TableName pins the gorm model to the goose-managed table.
func (Request) TableName() string { return "share_requests" }

// Repo persists share requests against the shared SQLite database.
type Repo struct{ db *gorm.DB }

// NewRepo wires a Repo to the open gorm handle.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Create inserts a new request row.
func (r *Repo) Create(ctx context.Context, req *Request) error {
	return r.db.WithContext(ctx).Create(req).Error
}

// Get loads a request by id. Returns gorm.ErrRecordNotFound when absent so the
// service can map it to ErrNotFound.
func (r *Repo) Get(ctx context.Context, id string) (Request, error) {
	var req Request
	return req, r.db.WithContext(ctx).Where("id = ?", id).First(&req).Error
}

// PendingByPair finds an open request for the same (source, destination, wing)
// triple, so a re-submit reuses it rather than stacking duplicates. found=false
// (with a nil error) means there is no pending request for that triple.
func (r *Repo) PendingByPair(ctx context.Context, fromTeam, toTeam, wing string) (Request, bool, error) {
	var req Request
	err := r.db.WithContext(ctx).
		Where("from_team_id = ? AND to_team_id = ? AND wing = ? AND status = ?",
			fromTeam, toTeam, wing, string(StatusPending)).
		First(&req).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Request{}, false, nil
	}
	if err != nil {
		return Request{}, false, err
	}
	return req, true, nil
}

// IncomingPending lists the pending requests addressed to a destination team,
// newest first — the destination admin's inbox.
func (r *Repo) IncomingPending(ctx context.Context, toTeam string) ([]Request, error) {
	var reqs []Request
	err := r.db.WithContext(ctx).
		Where("to_team_id = ? AND status = ?", toTeam, string(StatusPending)).
		Order("created_at DESC").
		Find(&reqs).Error
	return reqs, err
}

// Claim atomically transitions a STILL-PENDING request to a resolved status
// (accepted or declined), stamping who acted and when. It returns true only when
// THIS call made the transition (one row matched). A false means another actor
// already resolved it — so accept and decline are mutually exclusive under
// concurrency, and a late accept can never overwrite a decision a decline already
// made. The to_team_id predicate keeps the claim bound to the destination even if
// the id were somehow reused. The status='pending' predicate is the optimistic
// lock; SQLite serializes the UPDATE, so no explicit transaction is needed.
func (r *Repo) Claim(ctx context.Context, id, toTeam string, status Status, resolvedBy string) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res := r.db.WithContext(ctx).Model(&Request{}).
		Where("id = ? AND to_team_id = ? AND status = ?", id, toTeam, string(StatusPending)).
		Updates(map[string]any{
			"status":      string(status),
			"resolved_at": now,
			"resolved_by": resolvedBy,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// Reopen returns a request to pending and clears its resolution stamps. Accept
// uses it to undo its claim when the copy itself fails, so a transient copy error
// leaves the request retryable rather than stuck as accepted-but-empty.
func (r *Repo) Reopen(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&Request{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      string(StatusPending),
			"resolved_at": nil,
			"resolved_by": nil,
		}).Error
}
