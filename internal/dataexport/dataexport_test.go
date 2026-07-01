package dataexport

import (
	"context"
	"path/filepath"
	"testing"

	appdb "github.com/atvirokodosprendimai/agentsmemory/db"

	"github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newMigratedSource opens a fresh no-cgo SQLite database in a temp dir and applies
// the embedded goose migrations, so the test exercises the real production schema
// rather than a hand-rolled subset.
func newMigratedSource(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.db")
	g, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	goose.SetBaseFS(appdb.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return g
}

// exec runs a raw statement and fails the test on error, keeping the seed compact.
func exec(t *testing.T, g *gorm.DB, sql string, args ...any) {
	t.Helper()
	if err := g.Exec(sql, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// TestBuildTeamArchive_ScopesAndRedacts seeds two isolated teams and asserts an
// archive of team A contains only A's rows (never B's) and blanks credential
// material — the two guarantees BDAR compliance rests on.
func TestBuildTeamArchive_ScopesAndRedacts(t *testing.T) {
	ctx := context.Background()
	src := newMigratedSource(t)

	const (
		teamA, teamB = "team-a", "team-b"
		userA, userB = "user-a", "user-b"
		keyA         = "key-a"
		secretHash   = "ORIGINAL_SECRET_HASH"
		secretEnc    = "ORIGINAL_SEALED_TOKEN"
		pwHash       = "$2a$10$originalbcryptpasswordhashvalue"
	)

	// Team A: user, membership, an API key with sealed secrets, and two drawers.
	exec(t, src, `INSERT INTO teams (id,name,slug,created_at,kind) VALUES (?,?,?,?,?)`, teamA, "Alpha", "alpha", "2026-01-01T00:00:00Z", "personal")
	exec(t, src, `INSERT INTO users (id,email,password_hash,display_name,created_at) VALUES (?,?,?,?,?)`, userA, "a@example.com", pwHash, "A", "2026-01-01T00:00:00Z")
	exec(t, src, `INSERT INTO memberships (id,team_id,user_id,role,created_at) VALUES (?,?,?,?,?)`, "m-a", teamA, userA, "admin", "2026-01-01T00:00:00Z")
	exec(t, src, `INSERT INTO api_keys (id,team_id,user_id,name,prefix,token_hash,created_at,token_enc) VALUES (?,?,?,?,?,?,?,?)`, keyA, teamA, userA, "default", "abc", secretHash, "2026-01-01T00:00:00Z", secretEnc)
	exec(t, src, `INSERT INTO drawers (team_id,id,wing,room,content,filed_at) VALUES (?,?,?,?,?,?)`, teamA, "d-a1", "w", "r", "alpha memory one", "2026-01-01T00:00:00Z")
	exec(t, src, `INSERT INTO drawers (team_id,id,wing,room,content,filed_at) VALUES (?,?,?,?,?,?)`, teamA, "d-a2", "w", "r", "alpha memory two", "2026-01-01T00:00:00Z")
	// A drawer embedding (base namespace) and a closet embedding (sub-namespace
	// teamA::closets) — the export must include BOTH namespaces for the team.
	blob := []byte{0, 0, 0, 0}
	exec(t, src, `INSERT INTO vectors (namespace,id,dim,vector) VALUES (?,?,?,?)`, teamA, "d-a1", 1, blob)
	exec(t, src, `INSERT INTO vectors (namespace,id,dim,vector) VALUES (?,?,?,?)`, teamA+"::closets", "c-a1", 1, blob)

	// Team B: a separate tenant that must NEVER appear in A's archive.
	exec(t, src, `INSERT INTO teams (id,name,slug,created_at,kind) VALUES (?,?,?,?,?)`, teamB, "Beta", "beta", "2026-01-01T00:00:00Z", "personal")
	exec(t, src, `INSERT INTO users (id,email,password_hash,display_name,created_at) VALUES (?,?,?,?,?)`, userB, "b@example.com", pwHash, "B", "2026-01-01T00:00:00Z")
	exec(t, src, `INSERT INTO memberships (id,team_id,user_id,role,created_at) VALUES (?,?,?,?,?)`, "m-b", teamB, userB, "admin", "2026-01-01T00:00:00Z")
	exec(t, src, `INSERT INTO api_keys (id,team_id,user_id,name,prefix,token_hash,created_at,token_enc) VALUES (?,?,?,?,?,?,?,?)`, "key-b", teamB, userB, "default", "xyz", "B_HASH", "2026-01-01T00:00:00Z", "B_ENC")
	exec(t, src, `INSERT INTO drawers (team_id,id,wing,room,content,filed_at) VALUES (?,?,?,?,?,?)`, teamB, "d-b1", "w", "r", "beta memory", "2026-01-01T00:00:00Z")
	exec(t, src, `INSERT INTO vectors (namespace,id,dim,vector) VALUES (?,?,?,?)`, teamB, "d-b1", 1, []byte{0, 0, 0, 0})
	exec(t, src, `INSERT INTO vectors (namespace,id,dim,vector) VALUES (?,?,?,?)`, teamB+"::closets", "c-b1", 1, []byte{0, 0, 0, 0})

	path, cleanup, err := New(src).BuildTeamArchive(ctx, teamA, userA)
	if err != nil {
		t.Fatalf("BuildTeamArchive: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })

	arc, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}

	count := func(sql string, args ...any) int64 {
		var n int64
		if err := arc.Raw(sql, args...).Scan(&n).Error; err != nil {
			t.Fatalf("count %q: %v", sql, err)
		}
		return n
	}

	// Scoping: only team A's rows are present; team B is entirely absent.
	if got := count(`SELECT COUNT(*) FROM drawers WHERE team_id = ?`, teamA); got != 2 {
		t.Errorf("team A drawers = %d, want 2", got)
	}
	if got := count(`SELECT COUNT(*) FROM drawers WHERE team_id = ?`, teamB); got != 0 {
		t.Errorf("team B drawers leaked into archive: %d, want 0", got)
	}
	// Vectors: both the base and the ::closets sub-namespace for team A are present;
	// no team B vector (base or closet) leaks in.
	if got := count(`SELECT COUNT(*) FROM vectors WHERE namespace = ? OR namespace LIKE ?`, teamA, teamA+"::%"); got != 2 {
		t.Errorf("team A vectors (base + closets) = %d, want 2", got)
	}
	if got := count(`SELECT COUNT(*) FROM vectors WHERE namespace LIKE ?`, teamB+"%"); got != 0 {
		t.Errorf("team B vectors leaked into archive: %d, want 0", got)
	}
	if got := count(`SELECT COUNT(*) FROM teams`); got != 1 {
		t.Errorf("teams in archive = %d, want 1 (only team A)", got)
	}
	if got := count(`SELECT COUNT(*) FROM teams WHERE id = ?`, teamB); got != 0 {
		t.Errorf("team B row leaked into archive")
	}
	if got := count(`SELECT COUNT(*) FROM users`); got != 1 {
		t.Errorf("users in archive = %d, want 1 (only the requester)", got)
	}
	if got := count(`SELECT COUNT(*) FROM users WHERE id = ?`, userB); got != 0 {
		t.Errorf("co-tenant user B leaked into archive")
	}
	if got := count(`SELECT COUNT(*) FROM memberships WHERE user_id = ?`, userB); got != 0 {
		t.Errorf("co-tenant membership leaked into archive")
	}

	// Redaction: no crackable password hash, no usable/API secret material.
	var gotPwHash, gotTokenHash, gotTokenEnc string
	if err := arc.Raw(`SELECT password_hash FROM users WHERE id = ?`, userA).Scan(&gotPwHash).Error; err != nil {
		t.Fatalf("read password_hash: %v", err)
	}
	if gotPwHash != "" {
		t.Errorf("password_hash not redacted: %q", gotPwHash)
	}
	if err := arc.Raw(`SELECT token_hash, token_enc FROM api_keys WHERE id = ?`, keyA).Row().Scan(&gotTokenHash, &gotTokenEnc); err != nil {
		t.Fatalf("read api key secrets: %v", err)
	}
	if gotTokenHash == secretHash {
		t.Errorf("token_hash not redacted (still original)")
	}
	if gotTokenHash != keyA {
		t.Errorf("token_hash = %q, want redacted to row id %q", gotTokenHash, keyA)
	}
	if gotTokenEnc != "" {
		t.Errorf("token_enc not blanked: %q", gotTokenEnc)
	}
}
