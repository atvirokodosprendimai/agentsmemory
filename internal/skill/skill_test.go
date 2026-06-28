package skill

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeStore is an in-memory Store so the service is tested without a database.
type fakeStore struct {
	skills map[string]Skill // keyed by team|name
}

func newFakeStore() *fakeStore { return &fakeStore{skills: map[string]Skill{}} }

func (f *fakeStore) GetByName(_ context.Context, teamID, name string) (Skill, error) {
	s, ok := f.skills[teamID+"|"+name]
	if !ok {
		return Skill{}, ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) Upsert(_ context.Context, teamID, name, desc, content, by string) (Skill, error) {
	key := teamID + "|" + name
	s := f.skills[key]
	s.TeamID, s.Name, s.Description, s.Content, s.UpdatedBy = teamID, name, desc, content, by
	s.Version++ // 0->1 on create, then increments
	f.skills[key] = s
	return s, nil
}

func (f *fakeStore) List(_ context.Context, teamID string) ([]Skill, error) {
	var out []Skill
	for _, s := range f.skills {
		if s.TeamID == teamID {
			out = append(out, s)
		}
	}
	return out, nil
}

// fakeCaller is a RoleHolder for the role-gate test.
type fakeCaller struct {
	team, user string
	write      bool
}

func (c fakeCaller) Team() string   { return c.team }
func (c fakeCaller) User() string   { return c.user }
func (c fakeCaller) CanWrite() bool { return c.write }

// TestLoadReturnsTeamScopedSkill confirms the load path returns the right body
// and is scoped to the caller's team.
func TestLoadReturnsTeamScopedSkill(t *testing.T) {
	store := newFakeStore()
	store.skills["team1|effective-go"] = Skill{
		ID: "s1", TeamID: "team1", Name: "effective-go",
		Content: "BODY", Version: 3,
	}
	svc := NewService(store)

	got, err := svc.Load(context.Background(), "team1", "effective-go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Content != "BODY" || got.Version != 3 {
		t.Fatalf("wrong skill loaded: %+v", got)
	}

	// A different team must not see team1's skill.
	if _, err := svc.Load(context.Background(), "team2", "effective-go"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for foreign team, got %v", err)
	}
}

// TestListSkillsScopedAndSummary confirms list returns only the team's skills as
// metadata summaries (the Summary type structurally cannot carry the body).
func TestListSkillsScopedAndSummary(t *testing.T) {
	store := newFakeStore()
	store.skills["team1|alpha"] = Skill{TeamID: "team1", Name: "alpha", Description: "da", Version: 2, Content: "BODY"}
	store.skills["team2|beta"] = Skill{TeamID: "team2", Name: "beta"}
	svc := NewService(store)

	list, err := svc.List(context.Background(), "team1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "alpha" || list[0].Version != 2 {
		t.Fatalf("list should return only team1's alpha v2, got %+v", list)
	}
}

// TestUpdateRequiresWriteRole confirms the role gate: a read-only member is
// refused, a writer succeeds and bumps the version.
func TestUpdateRequiresWriteRole(t *testing.T) {
	svc := NewService(newFakeStore())

	reader := fakeCaller{team: "team1", user: "u1", write: false}
	if _, err := svc.Update(context.Background(), reader, "x", "d", "c"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for reader, got %v", err)
	}

	writer := fakeCaller{team: "team1", user: "u2", write: true}
	s, err := svc.Update(context.Background(), writer, "x", "d", "c")
	if err != nil {
		t.Fatalf("writer update failed: %v", err)
	}
	if s.Version != 1 || s.UpdatedBy != "u2" {
		t.Fatalf("unexpected skill after update: %+v", s)
	}
}

// TestUpdateValidatesPayload confirms the untrusted write payload is bounded:
// a blank name, a blank body, and an over-long description are each rejected
// before storage, while an empty description (it is optional) is accepted.
func TestUpdateValidatesPayload(t *testing.T) {
	svc := NewService(newFakeStore())
	w := fakeCaller{team: "team1", user: "u1", write: true}
	ctx := context.Background()

	if _, err := svc.Update(ctx, w, "  ", "d", "c"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("blank name: got %v, want ErrInvalidName", err)
	}
	if _, err := svc.Update(ctx, w, "ok", "d", "   "); !errors.Is(err, ErrInvalidContent) {
		t.Fatalf("blank content: got %v, want ErrInvalidContent", err)
	}
	long := strings.Repeat("x", maxSkillDescriptionLen+1)
	if _, err := svc.Update(ctx, w, "ok", long, "c"); !errors.Is(err, ErrInvalidDescription) {
		t.Fatalf("over-long description: got %v, want ErrInvalidDescription", err)
	}
	// Empty description is fine — it is optional metadata, not a second body.
	if _, err := svc.Update(ctx, w, "ok", "", "c"); err != nil {
		t.Fatalf("empty description should be accepted, got %v", err)
	}
}
