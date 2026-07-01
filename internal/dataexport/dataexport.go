// Package dataexport builds a per-workspace SQLite archive of a tenant's data so
// a user can download everything held about them and their workspace. This is the
// BDAR/GDPR right of access & data portability made concrete: one click yields a
// standalone, fully-valid SQLite database (openable with any SQLite tool), not an
// opaque dump.
//
// The archive is the live schema replayed verbatim from the source database's
// sqlite_master, populated with ONLY the requesting tenant's rows. Isolation is
// the whole point of this package, so it is enforced two ways: (1) an explicit,
// reviewed manifest — a table is exported only by a deliberate decision, never by
// "copy anything with a team_id"; and (2) every copied row is filtered by the
// requester's team/user, so no other tenant's data can reach the archive.
package dataexport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Exporter builds tenant archives from the live source database. It reads through
// a *gorm.DB (the same SQLite source of truth the app runs on) and writes each
// archive to a throwaway file the caller streams and then deletes.
type Exporter struct {
	src *gorm.DB
}

// New constructs an Exporter over the live source database.
func New(src *gorm.DB) *Exporter { return &Exporter{src: src} }

// scope says how a table is partitioned, and therefore how the export filters it.
// Keeping this explicit (rather than sniffing columns) is the isolation contract:
// each table's filter is a reviewed decision, so the archive can only ever contain
// the requesting tenant's rows.
type scope int

const (
	scopeTeam     scope = iota // WHERE <col> = teamID  (workspace-owned data)
	scopeUser                  // WHERE <col> = userID  (the requester's identity rows)
	scopeTeamUser              // WHERE team_id = teamID AND user_id = userID
	scopeShare                 // WHERE from_team_id = teamID OR to_team_id = teamID
	scopeAll                   // no filter (non-personal reference data, e.g. plan catalog)
)

// tableSpec is one manifest entry: the table, its filter column (for the single
// -column scopes), and how to scope it.
type tableSpec struct {
	table string
	col   string
	scope scope
}

// where renders the SQL predicate + args that confine a table to the requester.
// scopeAll returns an empty predicate (copied wholesale); an unknown scope fails
// closed with a predicate that matches nothing, so a mis-tagged table can never
// leak rows.
func (ts tableSpec) where(teamID, userID string) (string, []any) {
	switch ts.scope {
	case scopeTeam:
		return quoteIdent(ts.col) + " = ?", []any{teamID}
	case scopeUser:
		return quoteIdent(ts.col) + " = ?", []any{userID}
	case scopeTeamUser:
		return "team_id = ? AND user_id = ?", []any{teamID, userID}
	case scopeShare:
		return "from_team_id = ? OR to_team_id = ?", []any{teamID, teamID}
	case scopeAll:
		return "", nil
	default:
		return "1 = 0", nil
	}
}

// manifest is the complete, ordered set of tables an archive contains. Order is
// parent-first (reference + identity before children) so the archive is valid even
// for a strict SQLite tool that enforces foreign keys on open.
//
// Deliberately scoped choices:
//   - Workspace memory (drawers, closets, graph, kg, vectors, skills, usage, subs,
//     merge jobs) is the team's — copied by team, all of it, because that IS what
//     "download this workspace's data" means.
//   - Identity rows (users, memberships, api_keys) are scoped to the REQUESTER, so
//     the archive carries the user's own account/keys/membership, never a
//     co-member's — which also keeps every foreign key referentially consistent.
//   - plans is a non-personal catalog copied wholesale so teams.plan_id resolves.
//   - The global skillset row and goose_db_version are intentionally absent (not
//     the user's data / bookkeeping).
var manifest = []tableSpec{
	{table: "plans", scope: scopeAll},
	{table: "users", col: "id", scope: scopeUser},
	{table: "teams", col: "id", scope: scopeTeam},
	{table: "memberships", scope: scopeTeamUser},
	{table: "api_keys", scope: scopeTeamUser},
	{table: "skills", col: "team_id", scope: scopeTeam},
	{table: "usage_counters", col: "team_id", scope: scopeTeam},
	{table: "subscriptions", col: "team_id", scope: scopeTeam},
	{table: "vectors", col: "namespace", scope: scopeTeam},
	{table: "drawers", col: "team_id", scope: scopeTeam},
	{table: "closets", col: "team_id", scope: scopeTeam},
	{table: "hallways", col: "team_id", scope: scopeTeam},
	{table: "tunnels", col: "team_id", scope: scopeTeam},
	{table: "kg_entities", col: "team_id", scope: scopeTeam},
	{table: "kg_triples", col: "team_id", scope: scopeTeam},
	{table: "share_requests", scope: scopeShare},
	{table: "merge_jobs", col: "team_id", scope: scopeTeam},
}

// redactors blanks credential material that must never travel in an export, even
// to the data subject. The password hash is bcrypt (offline-crackable), so it is
// blanked outright. An API key's token_hash is a one-way SHA-256 (useless as a
// credential) but its column is UNIQUE NOT NULL, so it is replaced with the row's
// own id — valid and unique, yet carrying nothing; token_enc is server-encrypted
// ciphertext the user cannot open, so it is blanked. Keyed table → column →
// transform over the whole row (so a redactor can read another column, e.g. id).
var redactors = map[string]map[string]func(row map[string]any) any{
	"users": {
		"password_hash": func(map[string]any) any { return "" },
	},
	"api_keys": {
		"token_hash": func(row map[string]any) any { return row["id"] },
		"token_enc":  func(map[string]any) any { return "" },
	},
}

// BuildTeamArchive writes an archive of everything the export covers for teamID
// (scoped to userID for identity rows) into a fresh temp file and returns its
// path. The caller streams the file, then invokes cleanup to delete it. On any
// error the temp directory is removed before returning, so a failed build leaves
// nothing behind and the caller can still send a clean HTTP error (nothing has
// been written to the response yet).
func (e *Exporter) BuildTeamArchive(ctx context.Context, teamID, userID string) (path string, cleanup func() error, err error) {
	dir, err := os.MkdirTemp("", "agentsmemory-export-")
	if err != nil {
		return "", nil, fmt.Errorf("temp dir: %w", err)
	}
	cleanup = func() error { return os.RemoveAll(dir) }
	// Declared first so it runs LAST (LIFO): only after the archive DB is closed
	// and flushed do we decide whether the build failed and the dir must go.
	defer func() {
		if err != nil {
			_ = cleanup()
		}
	}()

	dbPath := filepath.Join(dir, "agentsmemory-export.db")
	dst, err := openArchive(dbPath)
	if err != nil {
		return "", nil, err
	}
	sqlDst, derr := dst.DB()
	if derr != nil {
		return "", nil, fmt.Errorf("archive sql handle: %w", derr)
	}
	// Declared after the open so it runs FIRST: close (and flush) the archive file
	// before the caller streams it. A close error surfaces only if nothing failed.
	defer func() {
		if cerr := sqlDst.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close archive: %w", cerr)
		}
	}()

	// Foreign keys off during load so parent/child insert order can never abort a
	// copy; the exported rows are referentially consistent by construction anyway.
	if err = dst.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
		return "", nil, fmt.Errorf("disable foreign keys: %w", err)
	}
	if err = e.replaySchema(ctx, dst); err != nil {
		return "", nil, err
	}
	if err = e.copyRows(ctx, dst, teamID, userID); err != nil {
		return "", nil, err
	}
	return dbPath, cleanup, nil
}

// openArchive opens a fresh, silent SQLite database at path via the same no-cgo
// glebarez driver the app uses, so an archive is byte-compatible with the source.
func openArchive(path string) (*gorm.DB, error) {
	g, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open archive db: %w", err)
	}
	return g, nil
}

// replaySchema recreates the source schema in the archive by replaying the source
// sqlite_master DDL verbatim — every table and index exactly as migrated. This
// deliberately avoids re-running goose: it needs no migrations FS, sidesteps
// goose's global-state (safe under concurrent exports), and guarantees the archive
// schema matches the live one. sqlite_% internals and goose bookkeeping are
// skipped; tables are created before indexes.
func (e *Exporter) replaySchema(ctx context.Context, dst *gorm.DB) error {
	rows, err := e.src.WithContext(ctx).Raw(
		`SELECT sql FROM sqlite_master
		 WHERE sql IS NOT NULL
		   AND name NOT LIKE 'sqlite_%'
		   AND name <> 'goose_db_version'
		 ORDER BY CASE type WHEN 'table' THEN 0 ELSE 1 END, name`).Rows()
	if err != nil {
		return fmt.Errorf("read source schema: %w", err)
	}
	defer rows.Close()

	// Collect first, then execute: don't hold the source cursor open while writing
	// to the archive.
	var stmts []string
	for rows.Next() {
		var stmt string
		if err := rows.Scan(&stmt); err != nil {
			return fmt.Errorf("scan schema row: %w", err)
		}
		stmts = append(stmts, stmt)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema rows: %w", err)
	}
	for _, stmt := range stmts {
		if err := dst.Exec(stmt).Error; err != nil {
			return fmt.Errorf("replay schema: %w", err)
		}
	}
	return nil
}

// copyRows copies every manifest table's scoped rows into the archive inside a
// single archive transaction, so thousands of drawer/vector rows load as one
// commit rather than one commit per row.
func (e *Exporter) copyRows(ctx context.Context, dst *gorm.DB, teamID, userID string) error {
	return dst.Transaction(func(tx *gorm.DB) error {
		for _, spec := range manifest {
			if err := e.copyTable(ctx, tx, spec, teamID, userID); err != nil {
				return fmt.Errorf("copy %s: %w", spec.table, err)
			}
		}
		return nil
	})
}

// copyTable streams one table's scoped rows from the source into the archive,
// applying any per-column redaction. It scans each row generically (SELECT *) so
// it needs no per-table struct and stays correct as migrations add columns; the
// archive's INSERT lists the exact same columns, so ordering always matches.
func (e *Exporter) copyTable(ctx context.Context, dst *gorm.DB, spec tableSpec, teamID, userID string) error {
	where, args := spec.where(teamID, userID)
	query := "SELECT * FROM " + quoteIdent(spec.table)
	if where != "" {
		query += " WHERE " + where
	}
	rows, err := e.src.WithContext(ctx).Raw(query, args...).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	insert := buildInsert(spec.table, cols)
	red := redactors[spec.table]

	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		if red != nil {
			applyRedaction(cols, vals, red)
		}
		if err := dst.WithContext(ctx).Exec(insert, vals...).Error; err != nil {
			return err
		}
	}
	return rows.Err()
}

// applyRedaction rewrites the redacted columns of one scanned row in place. The
// row is snapshotted into a name→value map first so a transform can reference
// other columns (e.g. token_hash reads the row's id) without being sensitive to
// column order.
func applyRedaction(cols []string, vals []any, red map[string]func(row map[string]any) any) {
	row := make(map[string]any, len(cols))
	for i, c := range cols {
		row[c] = vals[i]
	}
	for i, c := range cols {
		if fn, ok := red[c]; ok {
			vals[i] = fn(row)
		}
	}
}

// buildInsert builds a positional INSERT naming exactly the scanned columns, so
// the archive receives values in the same order they were read.
func buildInsert(table string, cols []string) string {
	quoted := make([]string, len(cols))
	placeholders := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(c)
		placeholders[i] = "?"
	}
	return "INSERT INTO " + quoteIdent(table) +
		" (" + strings.Join(quoted, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
}

// quoteIdent double-quotes a SQLite identifier, escaping embedded quotes. Table
// and column names here come from our own migrations (via sqlite_master), never
// user input, but quoting keeps reserved-word columns safe and the intent explicit.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
