package mergejob

import (
	"context"
	"errors"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// --- fakes ------------------------------------------------------------------

type fakeRoles struct{ roles map[string]tenant.Role }

func (f fakeRoles) MembershipRole(_ context.Context, userID, teamID string) (tenant.Role, error) {
	r, ok := f.roles[userID+"@"+teamID]
	if !ok {
		return "", tenant.ErrNotMember
	}
	return r, nil
}

type fakeWings struct{ wings map[string][]palace.WingStat }

func (f fakeWings) Wings(_ context.Context, teamID string) ([]palace.WingStat, error) {
	return f.wings[teamID], nil
}

// fakeRepo implements both jobStore (service) and claimer (worker).
type fakeRepo struct {
	created   []Job
	queue     []Job
	doneID    string
	doneDraw  int64
	doneClose int64
	failID    string
	failMsg   string
	released  int64
}

func (r *fakeRepo) Create(_ context.Context, job *Job) error {
	r.created = append(r.created, *job)
	return nil
}
func (r *fakeRepo) ListForTeam(_ context.Context, teamID string, _ int) ([]Job, error) {
	var out []Job
	for _, j := range r.created {
		if j.TeamID == teamID {
			out = append(out, j)
		}
	}
	return out, nil
}
func (r *fakeRepo) ReleaseRunning(_ context.Context) (int64, error) {
	var n int64
	for i := range r.queue {
		if r.queue[i].Status == string(StatusRunning) {
			r.queue[i].Status = string(StatusQueued)
			n++
		}
	}
	r.released = n
	return n, nil
}
func (r *fakeRepo) ClaimNext(_ context.Context) (Job, bool, error) {
	if len(r.queue) == 0 {
		return Job{}, false, nil
	}
	j := r.queue[0]
	r.queue = r.queue[1:]
	return j, true, nil
}
func (r *fakeRepo) MarkDone(_ context.Context, id string, drawers, closets int64) error {
	r.doneID, r.doneDraw, r.doneClose = id, drawers, closets
	return nil
}
func (r *fakeRepo) MarkFailed(_ context.Context, id, msg string) error {
	r.failID, r.failMsg = id, msg
	return nil
}

func ctx() context.Context { return context.Background() }

func svcHarness() (*Service, *fakeRepo, fakeRoles, fakeWings) {
	repo := &fakeRepo{}
	roles := fakeRoles{roles: map[string]tenant.Role{}}
	wings := fakeWings{wings: map[string][]palace.WingStat{
		"t1": {{Wing: "wing_research", Drawers: 5, Rooms: 2}, {Wing: "research", Drawers: 3, Rooms: 1}},
	}}
	return NewService(repo, roles, wings), repo, roles, wings
}

// --- Enqueue ----------------------------------------------------------------

func TestEnqueueRejectsReadOnlyMember(t *testing.T) {
	svc, repo, roles, _ := svcHarness()
	roles.roles["u1@t1"] = tenant.RoleMember
	_, err := svc.Enqueue(ctx(), "u1", "t1", "wing_research", "research")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("no job should be queued")
	}
}

func TestEnqueueRejectsNonMember(t *testing.T) {
	svc, _, _, _ := svcHarness()
	_, err := svc.Enqueue(ctx(), "u1", "t1", "wing_research", "research")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestEnqueueRejectsSameWing(t *testing.T) {
	svc, _, roles, _ := svcHarness()
	roles.roles["u1@t1"] = tenant.RoleWriter
	_, err := svc.Enqueue(ctx(), "u1", "t1", "research", "research")
	if !errors.Is(err, ErrSameWing) {
		t.Fatalf("want ErrSameWing, got %v", err)
	}
}

func TestEnqueueRejectsMissingSourceWing(t *testing.T) {
	svc, _, roles, _ := svcHarness()
	roles.roles["u1@t1"] = tenant.RoleAdmin
	_, err := svc.Enqueue(ctx(), "u1", "t1", "ghost", "research")
	if !errors.Is(err, ErrWingNotFound) {
		t.Fatalf("want ErrWingNotFound, got %v", err)
	}
}

func TestEnqueueQueuesJob(t *testing.T) {
	svc, repo, roles, _ := svcHarness()
	roles.roles["u1@t1"] = tenant.RoleWriter
	job, err := svc.Enqueue(ctx(), "u1", "t1", "wing_research", "research")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.Status != string(StatusQueued) || job.Target != "research" || len(job.Sources) != 1 || job.Sources[0] != "wing_research" {
		t.Fatalf("bad job: %+v", job)
	}
	if len(repo.created) != 1 {
		t.Fatalf("want 1 queued job, got %d", len(repo.created))
	}
}

// Enqueue allows a target that does not exist yet (folding wing_X into a fresh X
// is a rename); only the source must exist.
func TestEnqueueAllowsNewTarget(t *testing.T) {
	svc, _, roles, _ := svcHarness()
	roles.roles["u1@t1"] = tenant.RoleWriter
	_, err := svc.Enqueue(ctx(), "u1", "t1", "wing_research", "brandnew")
	if err != nil {
		t.Fatalf("merging into a fresh target should be allowed, got %v", err)
	}
}

// --- Duplicates -------------------------------------------------------------

func TestDuplicatesDetectsPrefixPairs(t *testing.T) {
	svc, _, _, _ := svcHarness()
	pairs, err := svc.Duplicates(ctx(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("want 1 duplicate pair, got %d: %+v", len(pairs), pairs)
	}
	p := pairs[0]
	if p.Source != "wing_research" || p.Target != "research" || p.SourceDrawers != 5 || p.TargetDrawers != 3 {
		t.Fatalf("bad pair: %+v", p)
	}
}

func TestDuplicatesIgnoresUnpairedPrefix(t *testing.T) {
	repo := &fakeRepo{}
	roles := fakeRoles{roles: map[string]tenant.Role{}}
	// wing_solo has a prefix but no bare "solo" twin -> not a duplicate.
	wings := fakeWings{wings: map[string][]palace.WingStat{
		"t1": {{Wing: "wing_solo", Drawers: 4}},
	}}
	svc := NewService(repo, roles, wings)
	pairs, err := svc.Duplicates(ctx(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("want no pairs, got %+v", pairs)
	}
}
