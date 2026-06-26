package skill

import (
	"context"
	"errors"
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
