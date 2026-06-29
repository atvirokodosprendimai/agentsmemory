package share

import (
	"context"
	"errors"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"gorm.io/gorm"
)

// --- fakes ------------------------------------------------------------------

// fakeTeams is an in-memory teamLookup: slugs resolve to teams, and roles are
// keyed by "userID@teamID". A missing role row yields tenant.ErrNotMember, so the
// fake exercises the same not-a-member path the real repo does.
type fakeTeams struct {
	bySlug map[string]tenant.Team
	roles  map[string]tenant.Role
}

func (f fakeTeams) TeamBySlug(_ context.Context, slug string) (tenant.Team, error) {
	t, ok := f.bySlug[slug]
	if !ok {
		return tenant.Team{}, gorm.ErrRecordNotFound
	}
	return t, nil
}

func (f fakeTeams) MembershipRole(_ context.Context, userID, teamID string) (tenant.Role, error) {
	r, ok := f.roles[userID+"@"+teamID]
	if !ok {
		return "", tenant.ErrNotMember
	}
	return r, nil
}

// fakeProvider is an in-memory wingProvider. It records CopyWing calls so a test
// can assert the copy ran (or, for Decline, that it did not).
type fakeProvider struct {
	wings    map[string][]palace.WingStat // teamID -> wings
	result   palace.CopyResult
	copyErr  error
	copyCall *copyArgs
}

type copyArgs struct{ from, to, wing string }

func (f *fakeProvider) Wings(_ context.Context, teamID string) ([]palace.WingStat, error) {
	return f.wings[teamID], nil
}

func (f *fakeProvider) CopyWing(_ context.Context, from, to, wing string) (palace.CopyResult, error) {
	f.copyCall = &copyArgs{from, to, wing}
	if f.copyErr != nil {
		return palace.CopyResult{}, f.copyErr
	}
	return f.result, nil
}

// fakeStore is an in-memory requestStore.
type fakeStore struct {
	rows map[string]*Request
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]*Request{}} }

func (s *fakeStore) Create(_ context.Context, req *Request) error {
	cp := *req
	s.rows[req.ID] = &cp
	return nil
}

func (s *fakeStore) Get(_ context.Context, id string) (Request, error) {
	r, ok := s.rows[id]
	if !ok {
		return Request{}, gorm.ErrRecordNotFound
	}
	return *r, nil
}

func (s *fakeStore) PendingByPair(_ context.Context, from, to, wing string) (Request, bool, error) {
	for _, r := range s.rows {
		if r.FromTeamID == from && r.ToTeamID == to && r.Wing == wing && r.Status == string(StatusPending) {
			return *r, true, nil
		}
	}
	return Request{}, false, nil
}

func (s *fakeStore) IncomingPending(_ context.Context, to string) ([]Request, error) {
	var out []Request
	for _, r := range s.rows {
		if r.ToTeamID == to && r.Status == string(StatusPending) {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (s *fakeStore) Claim(_ context.Context, id, toTeam string, status Status, by string) (bool, error) {
	r, ok := s.rows[id]
	if !ok || r.ToTeamID != toTeam || r.Status != string(StatusPending) {
		return false, nil // not ours / already resolved => claim lost
	}
	r.Status = string(status)
	r.ResolvedBy = &by
	stamp := "resolved"
	r.ResolvedAt = &stamp
	return true, nil
}

func (s *fakeStore) Reopen(_ context.Context, id string) error {
	r, ok := s.rows[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	r.Status = string(StatusPending)
	r.ResolvedBy = nil
	r.ResolvedAt = nil
	return nil
}

// harness builds a service over the three fakes with a common fixture: source
// team "src" (slug src-slug) holding wing "proj", destination "dst" (slug
// dst-slug). Role wiring is left to each test.
func harness() (*Service, *fakeStore, *fakeProvider, fakeTeams) {
	store := newFakeStore()
	prov := &fakeProvider{
		wings: map[string][]palace.WingStat{
			"src": {{Wing: "proj", Drawers: 10, Rooms: 3}},
		},
		result: palace.CopyResult{Drawers: 10, Closets: 2},
	}
	teams := fakeTeams{
		bySlug: map[string]tenant.Team{
			"src-slug": {ID: "src", Slug: "src-slug", Name: "Source"},
			"dst-slug": {ID: "dst", Slug: "dst-slug", Name: "Dest"},
		},
		roles: map[string]tenant.Role{},
	}
	return NewService(store, teams, prov), store, prov, teams
}

const ctxUser = "u1"

func ctx() context.Context { return context.Background() }

// --- Request ----------------------------------------------------------------

func TestRequestRejectsReadOnlyMember(t *testing.T) {
	svc, store, _, teams := harness()
	teams.roles[ctxUser+"@src"] = tenant.RoleMember // read-only cannot export

	_, err := svc.Request(ctx(), ctxUser, "src", "dst-slug", "proj")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if len(store.rows) != 0 {
		t.Fatalf("no request should be created, got %d", len(store.rows))
	}
}

func TestRequestRejectsNonMember(t *testing.T) {
	svc, _, _, _ := harness() // no role row for the user => not a member
	_, err := svc.Request(ctx(), ctxUser, "src", "dst-slug", "proj")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestRequestUnknownSlug(t *testing.T) {
	svc, _, _, teams := harness()
	teams.roles[ctxUser+"@src"] = tenant.RoleWriter
	_, err := svc.Request(ctx(), ctxUser, "src", "nope", "proj")
	if !errors.Is(err, ErrSlugNotFound) {
		t.Fatalf("want ErrSlugNotFound, got %v", err)
	}
}

func TestRequestSameWorkspace(t *testing.T) {
	svc, _, _, teams := harness()
	teams.roles[ctxUser+"@src"] = tenant.RoleAdmin
	// Resolve a slug that points back at the source team.
	teams.bySlug["self"] = tenant.Team{ID: "src", Slug: "self"}
	_, err := svc.Request(ctx(), ctxUser, "src", "self", "proj")
	if !errors.Is(err, ErrSameWorkspace) {
		t.Fatalf("want ErrSameWorkspace, got %v", err)
	}
}

func TestRequestUnknownWing(t *testing.T) {
	svc, _, _, teams := harness()
	teams.roles[ctxUser+"@src"] = tenant.RoleWriter
	_, err := svc.Request(ctx(), ctxUser, "src", "dst-slug", "ghost")
	if !errors.Is(err, ErrWingNotFound) {
		t.Fatalf("want ErrWingNotFound, got %v", err)
	}
}

func TestRequestHappyPath(t *testing.T) {
	svc, store, _, teams := harness()
	teams.roles[ctxUser+"@src"] = tenant.RoleWriter

	req, err := svc.Request(ctx(), ctxUser, "src", "dst-slug", "proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.ToTeamID != "dst" || req.FromTeamID != "src" || req.Wing != "proj" {
		t.Fatalf("request fields wrong: %+v", req)
	}
	if req.Status != string(StatusPending) {
		t.Fatalf("want pending, got %q", req.Status)
	}
	if len(store.rows) != 1 {
		t.Fatalf("want 1 stored request, got %d", len(store.rows))
	}
}

func TestRequestDedupesPending(t *testing.T) {
	svc, store, _, teams := harness()
	teams.roles[ctxUser+"@src"] = tenant.RoleWriter

	first, err := svc.Request(ctx(), ctxUser, "src", "dst-slug", "proj")
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	second, err := svc.Request(ctx(), ctxUser, "src", "dst-slug", "proj")
	if !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("want ErrAlreadyPending, got %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("dedupe should return the existing request id")
	}
	if len(store.rows) != 1 {
		t.Fatalf("dedupe must not create a second row, got %d", len(store.rows))
	}
}

// --- Accept -----------------------------------------------------------------

// seedPending files a pending request directly so Accept/Decline tests don't
// depend on Request's gates.
func seedPending(store *fakeStore) Request {
	r := Request{
		ID: "req1", FromTeamID: "src", ToTeamID: "dst", Wing: "proj",
		RequestedBy: ctxUser, Status: string(StatusPending), CreatedAt: "t0",
	}
	store.rows[r.ID] = &r
	return r
}

func TestAcceptRequiresDestAdmin(t *testing.T) {
	svc, store, prov, teams := harness()
	seedPending(store)
	teams.roles["admin@dst"] = tenant.RoleWriter // writer is not enough to accept

	_, _, err := svc.Accept(ctx(), "admin", "dst", "req1")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if prov.copyCall != nil {
		t.Fatalf("copy must not run when authorization fails")
	}
}

func TestAcceptRejectsTeamMismatch(t *testing.T) {
	svc, store, _, teams := harness()
	seedPending(store) // addressed to "dst"
	teams.roles["admin@other"] = tenant.RoleAdmin

	// Admin of a different team tries to accept via their own team id.
	_, _, err := svc.Accept(ctx(), "admin", "other", "req1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for team mismatch, got %v", err)
	}
}

func TestAcceptHappyPath(t *testing.T) {
	svc, store, prov, teams := harness()
	seedPending(store)
	teams.roles["admin@dst"] = tenant.RoleAdmin

	res, req, err := svc.Accept(ctx(), "admin", "dst", "req1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.copyCall == nil || prov.copyCall.from != "src" || prov.copyCall.to != "dst" || prov.copyCall.wing != "proj" {
		t.Fatalf("copy called with wrong args: %+v", prov.copyCall)
	}
	if res.Drawers != 10 {
		t.Fatalf("want 10 drawers copied, got %d", res.Drawers)
	}
	if req.Status != string(StatusAccepted) {
		t.Fatalf("want accepted, got %q", req.Status)
	}
	if store.rows["req1"].Status != string(StatusAccepted) {
		t.Fatalf("stored row not marked accepted")
	}
}

func TestAcceptRejectsAlreadyResolved(t *testing.T) {
	svc, store, prov, teams := harness()
	r := seedPending(store)
	r.Status = string(StatusDeclined)
	store.rows[r.ID] = &r
	teams.roles["admin@dst"] = tenant.RoleAdmin

	_, _, err := svc.Accept(ctx(), "admin", "dst", "req1")
	if !errors.Is(err, ErrNotPending) {
		t.Fatalf("want ErrNotPending, got %v", err)
	}
	if prov.copyCall != nil {
		t.Fatalf("copy must not run for a resolved request")
	}
}

// --- Decline ----------------------------------------------------------------

func TestDeclineHappyPath(t *testing.T) {
	svc, store, prov, teams := harness()
	seedPending(store)
	teams.roles["admin@dst"] = tenant.RoleAdmin

	req, err := svc.Decline(ctx(), "admin", "dst", "req1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Status != string(StatusDeclined) {
		t.Fatalf("want declined, got %q", req.Status)
	}
	if prov.copyCall != nil {
		t.Fatalf("decline must not copy anything")
	}
}

func TestDeclineRequiresAdmin(t *testing.T) {
	svc, store, _, teams := harness()
	seedPending(store)
	teams.roles["admin@dst"] = tenant.RoleWriter

	_, err := svc.Decline(ctx(), "admin", "dst", "req1")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if store.rows["req1"].Status != string(StatusPending) {
		t.Fatalf("request should stay pending after a failed decline")
	}
}

// TestAcceptReopensOnCopyFailure asserts the claim-then-copy ordering undoes
// itself: if the copy fails after the request was claimed, the row returns to
// pending so it can be retried rather than sticking as accepted-but-empty.
func TestAcceptReopensOnCopyFailure(t *testing.T) {
	svc, store, prov, teams := harness()
	seedPending(store)
	teams.roles["admin@dst"] = tenant.RoleAdmin
	prov.copyErr = errors.New("store down")

	_, _, err := svc.Accept(ctx(), "admin", "dst", "req1")
	if err == nil {
		t.Fatalf("want a copy error, got nil")
	}
	if store.rows["req1"].Status != string(StatusPending) {
		t.Fatalf("request should be reopened to pending after a copy failure, got %q", store.rows["req1"].Status)
	}
}

// TestDeclineLosesAfterAccept is the mutual-exclusion guarantee: once a request
// is accepted, a decline cannot override it (the claim is lost), so a late
// decline can never undo a completed copy and vice versa.
func TestDeclineLosesAfterAccept(t *testing.T) {
	svc, store, _, teams := harness()
	seedPending(store)
	teams.roles["admin@dst"] = tenant.RoleAdmin

	if _, _, err := svc.Accept(ctx(), "admin", "dst", "req1"); err != nil {
		t.Fatalf("accept failed: %v", err)
	}
	_, err := svc.Decline(ctx(), "admin", "dst", "req1")
	if !errors.Is(err, ErrNotPending) {
		t.Fatalf("want ErrNotPending after accept, got %v", err)
	}
	if store.rows["req1"].Status != string(StatusAccepted) {
		t.Fatalf("status must remain accepted, got %q", store.rows["req1"].Status)
	}
}
