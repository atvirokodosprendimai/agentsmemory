// Package mergejob is the background wing-merge: the dashboard enqueues a job to
// fold one wing into another, and a worker runs the merge + derived-graph rebuild
// off the request path. The merge itself (relabel the `wing` of drawers + closets)
// is fast, but the graph rebuild that must follow can be slow on a large
// workspace, so it is deferred to a durable queue rather than blocking an HTTP
// request. This package owns the queue (Service for the web side, Worker for the
// background side); the actual relabel + rebuild live in palace and are reached
// through a narrow interface.
package mergejob

import (
	"context"
	"errors"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/google/uuid"
)

// Sentinel errors mapped to user-facing banners by the web layer.
var (
	ErrForbidden    = errors.New("mergejob: not authorized")
	ErrSameWing     = errors.New("mergejob: source and target are the same wing")
	ErrWingNotFound = errors.New("mergejob: that source wing does not exist")
)

// roleLookup is the slice of the tenant repo the merge flow needs: a user's role
// in a workspace. *tenant.Repo satisfies it.
type roleLookup interface {
	MembershipRole(ctx context.Context, userID, teamID string) (tenant.Role, error)
}

// wingLister lists a team's wings (to validate the source and detect duplicates).
// *palace.Service satisfies it.
type wingLister interface {
	Wings(ctx context.Context, teamID string) ([]palace.WingStat, error)
}

// jobStore is the persistence the service needs. *Repo satisfies it; tests use a
// fake. The worker uses a narrower slice of the same repo (see worker.go).
type jobStore interface {
	Create(ctx context.Context, job *Job) error
	ListForTeam(ctx context.Context, teamID string, limit int) ([]Job, error)
}

// Service is the web-facing half: enqueue a merge, list a team's jobs, and detect
// the wing_X/X duplicate pairs the dashboard offers as one-click merges.
type Service struct {
	jobs  jobStore
	teams roleLookup
	wings wingLister
}

// NewService wires the enqueue/list/detect side of the merge flow.
func NewService(jobs jobStore, teams roleLookup, wings wingLister) *Service {
	return &Service{jobs: jobs, teams: teams, wings: wings}
}

// DuplicatePair is a detected wing_X/X collision: Source (the prefixed wing) folds
// into Target (the clean one), with each side's drawer count so the user can see
// the weights before merging.
type DuplicatePair struct {
	Source        string
	Target        string
	SourceDrawers int
	TargetDrawers int
}

// canManage reports whether a role may reorganize a workspace's wings. Merging is
// a structural change to shared memory, so it takes the same writer/admin bar as
// editing a shared skill — a read-only member cannot restructure the palace.
func canManage(r tenant.Role) bool {
	return r == tenant.RoleWriter || r == tenant.RoleAdmin
}

// Enqueue validates and queues a merge of one source wing into a target. It does
// NOT merge — the worker does — so this returns as soon as the job is durably
// queued. The target need not exist yet (folding wing_X into a fresh X is also a
// rename); only the source must exist, else there is nothing to move.
func (s *Service) Enqueue(ctx context.Context, requesterID, teamID, source, target string) (Job, error) {
	if err := s.requireManager(ctx, requesterID, teamID); err != nil {
		return Job{}, err
	}
	// Reuse palace's name sanitiser so a queued wing name obeys the same rules the
	// merge itself enforces (non-empty, length, no path/NUL/unsafe chars).
	src, err := palace.SanitizeName(source, "source")
	if err != nil {
		return Job{}, err
	}
	tgt, err := palace.SanitizeName(target, "target")
	if err != nil {
		return Job{}, err
	}
	if src == tgt {
		return Job{}, ErrSameWing
	}
	exists, err := s.wingExists(ctx, teamID, src)
	if err != nil {
		return Job{}, err
	}
	if !exists {
		return Job{}, ErrWingNotFound
	}

	job := Job{
		ID:          uuid.NewString(),
		TeamID:      teamID,
		Sources:     []string{src},
		Target:      tgt,
		Status:      string(StatusQueued),
		RequestedBy: requesterID,
		CreatedAt:   nowRFC3339(),
	}
	if err := s.jobs.Create(ctx, &job); err != nil {
		return Job{}, err
	}
	return job, nil
}

// ListForTeam returns a team's recent merge jobs (newest first), capped. The
// caller has already confirmed the viewer manages the team.
func (s *Service) ListForTeam(ctx context.Context, teamID string, limit int) ([]Job, error) {
	return s.jobs.ListForTeam(ctx, teamID, limit)
}

// Duplicates finds wing_X/X collisions in a team: any wing named "wing_<X>" whose
// bare "<X>" is also a wing. These are exactly the duplicates a prefixed import
// leaves behind, surfaced so the user can fold each prefixed wing into its clean
// twin in one click.
func (s *Service) Duplicates(ctx context.Context, teamID string) ([]DuplicatePair, error) {
	wings, err := s.wings.Wings(ctx, teamID)
	if err != nil {
		return nil, err
	}
	drawersByWing := make(map[string]int, len(wings))
	for _, w := range wings {
		drawersByWing[w.Wing] = w.Drawers
	}
	var pairs []DuplicatePair
	for _, w := range wings {
		base := strings.TrimPrefix(w.Wing, "wing_")
		if base == w.Wing || base == "" {
			continue // no "wing_" prefix to strip
		}
		if _, ok := drawersByWing[base]; ok {
			pairs = append(pairs, DuplicatePair{
				Source:        w.Wing,
				Target:        base,
				SourceDrawers: drawersByWing[w.Wing],
				TargetDrawers: drawersByWing[base],
			})
		}
	}
	return pairs, nil
}

// requireManager admits only a writer/admin of the workspace.
func (s *Service) requireManager(ctx context.Context, userID, teamID string) error {
	role, err := s.teams.MembershipRole(ctx, userID, teamID)
	if err != nil {
		if errors.Is(err, tenant.ErrNotMember) {
			return ErrForbidden
		}
		return err
	}
	if !canManage(role) {
		return ErrForbidden
	}
	return nil
}

// wingExists reports whether a wing currently holds drawers in the team.
func (s *Service) wingExists(ctx context.Context, teamID, wing string) (bool, error) {
	wings, err := s.wings.Wings(ctx, teamID)
	if err != nil {
		return false, err
	}
	for _, w := range wings {
		if w.Wing == wing {
			return true, nil
		}
	}
	return false, nil
}
