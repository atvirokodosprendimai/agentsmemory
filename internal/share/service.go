// Package share is the cross-workspace wing-share handshake. The CLI
// `agentsmemory share` copies a wing across tenants outright, but the dashboard
// must not: a slug box that copied on submit would let any caller push memory
// into — or pull it out of — a workspace just by knowing its slug. So the GUI
// path is a two-step consent flow. A writer/admin of the SOURCE files a pending
// request naming the destination (by slug) and the wing; an ADMIN of the
// DESTINATION must accept before palace.CopyWing runs. This package owns that
// handshake, bridging the tenant context (slugs, roles) and the palace context
// (listing + copying wings) without either depending on the other.
package share

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Sentinel errors. Handlers map each to a user-facing banner and the right HTTP
// behavior; the wording here stays internal. ErrForbidden is deliberately broad
// (not-a-member and wrong-role both collapse to it) so the surface never reveals
// which gate a caller failed.
var (
	ErrInvalidInput   = errors.New("share: requester, source, destination slug and wing are required")
	ErrSlugNotFound   = errors.New("share: no workspace found with that slug")
	ErrSameWorkspace  = errors.New("share: source and destination are the same workspace")
	ErrWingNotFound   = errors.New("share: that wing does not exist in the source workspace")
	ErrForbidden      = errors.New("share: not authorized")
	ErrNotFound       = errors.New("share: request not found")
	ErrNotPending     = errors.New("share: request has already been resolved")
	ErrAlreadyPending = errors.New("share: a pending request for this wing already exists")
)

// teamLookup is the slice of the tenant repo the share flow needs: resolve a
// human slug to a workspace, and read a user's role within one. *tenant.Repo
// satisfies it. Defined here (the consumer) so share depends on a narrow seam,
// not the whole repo surface.
type teamLookup interface {
	TeamBySlug(ctx context.Context, slug string) (tenant.Team, error)
	MembershipRole(ctx context.Context, userID, teamID string) (tenant.Role, error)
}

// wingProvider is the slice of the palace service the share flow needs: list a
// team's wings (to validate + offer them) and copy one across tenants.
// *palace.Service satisfies it.
type wingProvider interface {
	Wings(ctx context.Context, teamID string) ([]palace.WingStat, error)
	CopyWing(ctx context.Context, fromTeam, toTeam, wing string) (palace.CopyResult, error)
}

// requestStore is the persistence the service needs. *Repo satisfies it; tests
// use an in-memory fake so the service can be exercised without a database.
type requestStore interface {
	Create(ctx context.Context, req *Request) error
	Get(ctx context.Context, id string) (Request, error)
	PendingByPair(ctx context.Context, fromTeam, toTeam, wing string) (Request, bool, error)
	IncomingPending(ctx context.Context, toTeam string) ([]Request, error)
	Claim(ctx context.Context, id, toTeam string, status Status, resolvedBy string) (bool, error)
	Reopen(ctx context.Context, id string) error
}

// Service runs the wing-share handshake.
type Service struct {
	repo   requestStore
	teams  teamLookup
	palace wingProvider
}

// NewService wires the handshake to its collaborators.
func NewService(repo requestStore, teams teamLookup, palace wingProvider) *Service {
	return &Service{repo: repo, teams: teams, palace: palace}
}

// canExport reports whether a role may push a wing out of its workspace. Exporting
// a workspace's memory to another tenant is a write-class governance action, so it
// takes the same writer/admin bar as editing a shared skill — a read-only member
// cannot ship the workspace's memory elsewhere.
func canExport(r tenant.Role) bool {
	return r == tenant.RoleWriter || r == tenant.RoleAdmin
}

// Request files a pending share: copy `wing` from `fromTeamID` into the workspace
// addressed by `toSlug`. It validates the source-side authority and the inputs but
// copies NOTHING — the destination admin's Accept does that. Returns the existing
// row with ErrAlreadyPending when an identical request is already open, so a
// double-click is harmless.
func (s *Service) Request(ctx context.Context, requesterID, fromTeamID, toSlug, wing string) (Request, error) {
	wing = strings.TrimSpace(wing)
	toSlug = strings.TrimSpace(toSlug)
	if requesterID == "" || fromTeamID == "" || toSlug == "" || wing == "" {
		return Request{}, ErrInvalidInput
	}

	// Source-side authority: only a writer/admin of the source may export its wing.
	if err := s.requireExporter(ctx, requesterID, fromTeamID); err != nil {
		return Request{}, err
	}

	// Resolve the typed slug to a destination workspace. An unknown slug is its own
	// error so the UI can say "no workspace with that slug" rather than failing mute.
	//
	// This does disclose to a source writer/admin whether a slug exists — but slugs
	// carry a 24-bit random suffix (slugify(name)+"-"+uuid[:6]), so they cannot be
	// enumerated, only confirmed when already known; and resolving one grants
	// nothing on its own — the destination admin's Accept is the only thing that
	// moves memory. An accepted, documented tradeoff for a usable slug field.
	to, err := s.teams.TeamBySlug(ctx, toSlug)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Request{}, ErrSlugNotFound
		}
		return Request{}, err
	}
	if to.ID == fromTeamID {
		return Request{}, ErrSameWorkspace
	}

	// The wing must actually exist in the source, else there is nothing to copy and
	// the destination admin would be asked to accept an empty share.
	exists, err := s.wingExists(ctx, fromTeamID, wing)
	if err != nil {
		return Request{}, err
	}
	if !exists {
		return Request{}, ErrWingNotFound
	}

	// Reuse an open request for the same triple instead of stacking duplicates (the
	// partial unique index enforces this at the DB too; this is the friendly path).
	if existing, found, err := s.repo.PendingByPair(ctx, fromTeamID, to.ID, wing); err != nil {
		return Request{}, err
	} else if found {
		return existing, ErrAlreadyPending
	}

	req := Request{
		ID:          uuid.NewString(),
		FromTeamID:  fromTeamID,
		ToTeamID:    to.ID,
		Wing:        wing,
		RequestedBy: requesterID,
		Status:      string(StatusPending),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.repo.Create(ctx, &req); err != nil {
		// A concurrent identical submit may have inserted between our PendingByPair
		// check and here; the partial unique index then rejects this one. Treat that
		// as already-pending (reload the winner) rather than surfacing a raw DB error.
		if existing, found, perr := s.repo.PendingByPair(ctx, fromTeamID, to.ID, wing); perr == nil && found {
			return existing, ErrAlreadyPending
		}
		return Request{}, err
	}
	return req, nil
}

// Incoming returns the pending requests addressed to a destination team. The
// caller (a project-scoped handler) has already confirmed the actor administers
// the team, so this is a plain read.
func (s *Service) Incoming(ctx context.Context, toTeamID string) ([]Request, error) {
	return s.repo.IncomingPending(ctx, toTeamID)
}

// SourceWings lists the wings a requester may share out of a source workspace. It
// re-checks the exporter gate independently of the page render, so the picker can
// never offer wings the caller has no right to export.
func (s *Service) SourceWings(ctx context.Context, requesterID, fromTeamID string) ([]palace.WingStat, error) {
	if err := s.requireExporter(ctx, requesterID, fromTeamID); err != nil {
		return nil, err
	}
	return s.palace.Wings(ctx, fromTeamID)
}

// Accept is the destination's consent: it runs the copy and marks the request
// accepted. teamID is the destination from the URL — binding the request to it
// stops an admin of one workspace accepting a request aimed at another. The actor
// must be an ADMIN of the destination, the strictest bar, because accepting mixes
// another tenant's memory into this workspace.
func (s *Service) Accept(ctx context.Context, accepterID, teamID, reqID string) (palace.CopyResult, Request, error) {
	req, err := s.load(ctx, teamID, reqID)
	if err != nil {
		return palace.CopyResult{}, Request{}, err
	}
	if err := s.requireAdmin(ctx, accepterID, req.ToTeamID); err != nil {
		return palace.CopyResult{}, Request{}, err
	}

	// Claim the request (pending -> accepted) BEFORE copying. Claiming first is what
	// makes the copy safe under concurrency: if a second admin declines in parallel,
	// exactly one of the two claims wins, so a late copy can never overwrite a
	// decline that already landed. A lost claim means it was already resolved.
	won, err := s.repo.Claim(ctx, req.ID, req.ToTeamID, StatusAccepted, accepterID)
	if err != nil {
		return palace.CopyResult{}, req, err
	}
	if !won {
		return palace.CopyResult{}, req, ErrNotPending
	}

	res, err := s.palace.CopyWing(ctx, req.FromTeamID, req.ToTeamID, req.Wing)
	if err != nil {
		// The copy failed after we claimed the request — reopen it so this isn't left
		// stuck as accepted-but-empty; the admin can retry. Best-effort: a failed
		// reopen still surfaces the original copy error, which is the actionable one.
		_ = s.repo.Reopen(ctx, req.ID)
		return palace.CopyResult{}, req, fmt.Errorf("copy wing: %w", err)
	}
	req.Status = string(StatusAccepted)
	return res, req, nil
}

// Decline rejects a pending request without copying anything. Same destination
// admin gate and team-binding as Accept.
func (s *Service) Decline(ctx context.Context, accepterID, teamID, reqID string) (Request, error) {
	req, err := s.load(ctx, teamID, reqID)
	if err != nil {
		return Request{}, err
	}
	if err := s.requireAdmin(ctx, accepterID, req.ToTeamID); err != nil {
		return Request{}, err
	}
	// Same atomic claim as Accept (the other side of the mutual exclusion): decline
	// wins only if the request is still pending.
	won, err := s.repo.Claim(ctx, req.ID, req.ToTeamID, StatusDeclined, accepterID)
	if err != nil {
		return req, err
	}
	if !won {
		return req, ErrNotPending
	}
	req.Status = string(StatusDeclined)
	return req, nil
}

// load fetches a request and binds it to the destination team from the URL. A
// missing request and one aimed at a different team both return ErrNotFound, so
// the surface never confirms a request exists to a team it is not addressed to.
func (s *Service) load(ctx context.Context, teamID, reqID string) (Request, error) {
	req, err := s.repo.Get(ctx, reqID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Request{}, ErrNotFound
		}
		return Request{}, err
	}
	if req.ToTeamID != teamID {
		return Request{}, ErrNotFound
	}
	return req, nil
}

// requireExporter admits only a writer/admin of the source workspace.
func (s *Service) requireExporter(ctx context.Context, userID, teamID string) error {
	role, err := s.role(ctx, userID, teamID)
	if err != nil {
		return err
	}
	if !canExport(role) {
		return ErrForbidden
	}
	return nil
}

// requireAdmin admits only an admin of the given workspace.
func (s *Service) requireAdmin(ctx context.Context, userID, teamID string) error {
	role, err := s.role(ctx, userID, teamID)
	if err != nil {
		return err
	}
	if role != tenant.RoleAdmin {
		return ErrForbidden
	}
	return nil
}

// role resolves a user's role in a team, collapsing "not a member" into the same
// ErrForbidden as a too-low role (fail closed, reveal nothing).
func (s *Service) role(ctx context.Context, userID, teamID string) (tenant.Role, error) {
	role, err := s.teams.MembershipRole(ctx, userID, teamID)
	if err != nil {
		if errors.Is(err, tenant.ErrNotMember) {
			return "", ErrForbidden
		}
		return "", err
	}
	return role, nil
}

// wingExists reports whether a wing is present in a team's drawers.
func (s *Service) wingExists(ctx context.Context, teamID, wing string) (bool, error) {
	wings, err := s.palace.Wings(ctx, teamID)
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
