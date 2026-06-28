package skillset

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeStore is an in-memory Store so the Service can be tested without a database.
// It models the singleton: at most one stored skillset, created then replaced.
type fakeStore struct {
	cur    *Skillset
	setErr error
}

func (f *fakeStore) Get(context.Context) (Skillset, error) {
	if f.cur == nil {
		return Skillset{}, ErrNotSet
	}
	return *f.cur, nil
}

func (f *fakeStore) Set(_ context.Context, content, updatedBy string) (Skillset, error) {
	if f.setErr != nil {
		return Skillset{}, f.setErr
	}
	version := 1
	if f.cur != nil {
		version = f.cur.Version + 1 // mimic the repo's version bump on replace
	}
	s := Skillset{ID: globalID, Content: content, Version: version, UpdatedBy: updatedBy}
	f.cur = &s
	return s, nil
}

// caller is a test SuperHolder; super toggles the platform-superadmin gate.
type caller struct {
	user  string
	super bool
}

func (c caller) User() string       { return c.user }
func (c caller) IsSuperAdmin() bool { return c.super }

// TestGet_Unset confirms that an unwritten skillset reads as found=false, not an
// error — "no playbook yet" is a normal state am_skillset must handle gracefully.
func TestGet_Unset(t *testing.T) {
	svc := NewService(&fakeStore{})
	_, found, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get on unset: unexpected error %v", err)
	}
	if found {
		t.Fatal("Get on unset: want found=false")
	}
}

// TestSet_SuperAdmin_CreatesThenBumps verifies the happy path: a superadmin writes
// the playbook (v1), and a second write replaces it and bumps the version (v2),
// recording the editor as updated_by.
func TestSet_SuperAdmin_CreatesThenBumps(t *testing.T) {
	svc := NewService(&fakeStore{})
	admin := caller{user: "u-admin", super: true}

	first, err := svc.Set(context.Background(), admin, "# v1 playbook")
	if err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if first.Version != 1 || first.UpdatedBy != "u-admin" {
		t.Fatalf("first Set: got version=%d updatedBy=%q, want 1/u-admin", first.Version, first.UpdatedBy)
	}

	second, err := svc.Set(context.Background(), admin, "# v2 playbook")
	if err != nil {
		t.Fatalf("second Set: %v", err)
	}
	if second.Version != 2 {
		t.Fatalf("second Set: got version=%d, want 2", second.Version)
	}

	got, found, err := svc.Get(context.Background())
	if err != nil || !found {
		t.Fatalf("Get after Set: found=%v err=%v", found, err)
	}
	if got.Content != "# v2 playbook" {
		t.Fatalf("Get after Set: content=%q, want the v2 body", got.Content)
	}
}

// TestSet_NonSuperAdmin_Forbidden confirms the gate: a team admin (super=false) is
// refused, and nothing is written.
func TestSet_NonSuperAdmin_Forbidden(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(store)

	_, err := svc.Set(context.Background(), caller{user: "u-team-admin", super: false}, "# sneaky edit")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-superadmin Set: got %v, want ErrForbidden", err)
	}
	if store.cur != nil {
		t.Fatal("non-superadmin Set must not write the playbook")
	}
}

// TestSet_InvalidContent rejects an empty (or whitespace-only) body even from a
// superadmin: the gate passes, the content validation does not.
func TestSet_InvalidContent(t *testing.T) {
	svc := NewService(&fakeStore{})
	admin := caller{user: "u-admin", super: true}

	for _, body := range []string{"", "   \n\t  "} {
		if _, err := svc.Set(context.Background(), admin, body); !errors.Is(err, ErrInvalidContent) {
			t.Fatalf("Set(%q): got %v, want ErrInvalidContent", body, err)
		}
	}
}

// TestSet_OversizedContent rejects a body past the size cap before it is stored.
func TestSet_OversizedContent(t *testing.T) {
	svc := NewService(&fakeStore{})
	admin := caller{user: "u-admin", super: true}

	huge := strings.Repeat("x", maxContentLen+1)
	if _, err := svc.Set(context.Background(), admin, huge); !errors.Is(err, ErrInvalidContent) {
		t.Fatalf("oversized Set: got %v, want ErrInvalidContent", err)
	}
}
