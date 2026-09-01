package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/glebarez/sqlite"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newRoleEnv builds a Server over a migrated in-memory database with one user
// holding the given role, so the handler's authorization runs through the real
// membership lookup rather than a stub that could agree with anything.
func newRoleEnv(t *testing.T, role tenant.Role) (*Server, string, string) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	teamID, userID := uuid.NewString(), uuid.NewString()
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{"INSERT INTO teams (id, name, slug, kind, created_at) VALUES (?,?,?,?,?)",
			[]any{teamID, "Acme", "acme-" + teamID[:6], "personal", now}},
		{"INSERT INTO users (id, email, password_hash, display_name, created_at) VALUES (?,?,?,?,?)",
			[]any{userID, "member@example.test", "x", "R", now}},
		{"INSERT INTO memberships (id, team_id, user_id, role, created_at) VALUES (?,?,?,?,?)",
			[]any{uuid.NewString(), teamID, userID, string(role), now}},
	} {
		if err := gdb.Exec(stmt.q, stmt.args...).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return &Server{tenants: tenant.NewRepo(gdb)}, userID, teamID
}

// TestWingImportRefusesAReadOnlyMember is the authorization gate for the one web
// handler that writes a whole wing.
//
// ⚠ FOUND BY MUTATION, and it is the shape this audit exists for: deleting
// `if !tenant.CanWrite(role)` from postWingImport left internal/web green. The
// check was correct — nothing said so, and an authorization check that no test
// exercises is indistinguishable from one that was never there.
//
// It matters more than an ordinary write path: import replays a whole wing into
// the workspace, so a reader who could reach it could add memories every other
// member then recalls as the team's own. The refusal is asserted through the real
// membership lookup, because a stubbed role would agree with whatever the handler
// asked it.
func TestWingImportRefusesAReadOnlyMember(t *testing.T) {
	srv, userID, teamID := newRoleEnv(t, tenant.RoleMember)

	req := httptest.NewRequest(http.MethodPost, "/app/teams/"+teamID+"/wings/import", strings.NewReader(""))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("teamID", teamID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	req = req.WithContext(context.WithValue(ctx, userCtxKey, tenant.User{ID: userID, Email: "member@example.test"}))

	rec := httptest.NewRecorder()
	srv.postWingImport(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("a read-only member importing a wing got %d, want %d. Import replays a whole wing into the "+
			"workspace, so a role check that does not refuse lets a read-only member file memories "+
			"every other member recalls as the team's own. Body: %s",
			rec.Code, http.StatusForbidden, rec.Body.String())
	}
}
