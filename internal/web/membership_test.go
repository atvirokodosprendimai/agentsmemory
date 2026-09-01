package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// seedForeignWorkspace creates a second workspace the test's user is NOT a member
// of, and returns its id. It is what a real attack looks like from the server's
// side: a perfectly ordinary authenticated session, holding a teamID that is
// somebody else's.
func seedForeignWorkspace(t *testing.T, gdb *gorm.DB) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	teamID := uuid.NewString()
	ownerID := uuid.NewString()
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{"INSERT INTO teams (id, name, slug, kind, created_at) VALUES (?,?,?,?,?)",
			[]any{teamID, "Someone Else", "else-" + teamID[:6], "personal", now}},
		{"INSERT INTO users (id, email, password_hash, display_name, created_at) VALUES (?,?,?,?,?)",
			[]any{ownerID, "owner@other.test", "x", "O", now}},
		{"INSERT INTO memberships (id, team_id, user_id, role, created_at) VALUES (?,?,?,?,?)",
			[]any{uuid.NewString(), teamID, ownerID, string(tenant.RoleAdmin), now}},
	} {
		if err := gdb.Exec(stmt.q, stmt.args...).Error; err != nil {
			t.Fatalf("seed foreign workspace: %v", err)
		}
	}
	return teamID
}

// TestEveryWorkspaceRouteRefusesANonMember pins the cross-workspace boundary of
// the whole dashboard.
//
// ⚠ FOUND BY MUTATION (round 6), and it is the widest hole this audit found.
// Neutering the two branches of s.membership — the ErrNotMember arm and the error
// arm — left the entire repository green, and there is nothing else between an
// authenticated user and another workspace's data: membership() is the ONLY check
// every /projects/{teamID} route shares. With it gone, a logged-in user who types
// somebody else's workspace id reads their memories (getWingExport), downloads
// their whole workspace (getExport) and writes into it (postWingImport), because
// the handler is handed ok=true and an empty role.
//
// The role gates the rest of this package's tests assert sit BEHIND this one and
// are worth nothing without it: an empty role fails CanWrite, so the six handlers
// round 5 covered still refuse — which is exactly why the hole survived. What it
// does not refuse is every READ, and the reads are the whole corpus.
//
// It runs over the routes rather than over membership() directly, because the
// binding that matters is "this handler consults it", and a test of the helper
// alone stays green if a handler stops calling it.
func TestEveryWorkspaceRouteRefusesANonMember(t *testing.T) {
	srv, gdb, userID, ownTeamID := newRoleEnv(t, tenant.RoleAdmin)
	foreignTeamID := seedForeignWorkspace(t, gdb)

	// One handler per shape of workspace access: a read of the workspace's
	// memories, a read of its identity, and a write into it. Each takes only the
	// dependencies membership() needs, because a non-member must be refused
	// BEFORE any of the rest is touched — a handler that reaches its service and
	// then fails is not a refusal, it is a crash.
	for _, route := range []struct {
		name    string
		path    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"getWingExport", "/projects/{teamID}/wings/export?wing=wing_x", srv.getWingExport},
		{"postWingImport", "/projects/{teamID}/wings/import", srv.postWingImport},
		{"getExport", "/projects/{teamID}/export", srv.getExport},
	} {
		t.Run(route.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			route.handler(rec, asRole(foreignTeamID, userID, route.path))

			if rec.Code != http.StatusNotFound {
				t.Errorf("a member of one workspace reached %s in ANOTHER workspace and got %d, want %d. "+
					"s.membership is the only cross-workspace check these routes have; without its refusal "+
					"an authenticated user reads and writes any workspace whose id they can type. Body: %s",
					route.name, rec.Code, http.StatusNotFound, rec.Body.String())
			}
		})
	}

	// The control: the same user, the same handler, their OWN workspace. Without
	// it a refusal that refuses everybody would pass the loop above, and a
	// boundary that never lets anyone in is a broken dashboard rather than a
	// secure one. getWingExport with no ?wing is the cheapest way to see the
	// membership check PASS — it stops at a 400 for the missing parameter, one
	// step past the gate and before any service the fixture does not wire.
	t.Run("their own workspace is not refused", func(t *testing.T) {
		rec := httptest.NewRecorder()
		srv.getWingExport(rec, asRole(ownTeamID, userID, "/projects/{teamID}/wings/export"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("an admin asking to export from their OWN workspace got %d, want %d (the missing ?wing). "+
				"Anything else means the membership check is refusing on something other than membership, and "+
				"the refusals asserted above would prove nothing. Body: %s",
				rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})
}
