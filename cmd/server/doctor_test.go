package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/pressly/goose/v3"
	"github.com/urfave/cli/v3"
)

// latestEmbeddedMigration is the highest version in db.Migrations, read from the
// embedded set rather than written as a literal.
//
// The assertion that uses it means "ordinary preparation applied EVERY
// migration". A literal restates the count instead, so it fails the next time
// anyone adds one — for a reason that has nothing to do with what the test is
// checking. It fatals on an empty set, because a derived expectation of zero
// would make the comparison pass against a database that migrated nothing.
func latestEmbeddedMigration(t *testing.T) int {
	t.Helper()
	entries, err := fs.ReadDir(db.Migrations, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	highest := 0
	for _, e := range entries {
		var version int
		if _, err := fmt.Sscanf(e.Name(), "%05d_", &version); err != nil {
			continue
		}
		if version > highest {
			highest = version
		}
	}
	if highest == 0 {
		t.Fatal("no versioned migrations found in the embedded set — this check would " +
			"then pass against a database that applied nothing")
	}
	return highest
}

// TestDoctorIsRegistered: a command nothing registers is a command nobody can
// run, and this repository has shipped that shape four times. The check reads
// the CLI's own command list rather than the source, so it fails for the reason
// a user would notice — `agentsmemory doctor` not existing.
func TestDoctorIsRegistered(t *testing.T) {
	root := rootCommand(config.Default())
	var names []string
	for _, c := range root.Commands {
		names = append(names, c.Name)
	}
	found := false
	for _, n := range names {
		if n == "doctor" {
			found = true
		}
	}
	if !found {
		t.Errorf("the CLI registers %v and not \"doctor\" — the check exists and cannot be run", names)
	}
}

// TestDoctorRefusesWithNoCheckSelected: `doctor` with no flag must not report a
// clean palace. A check that ran nothing and exited 0 is indistinguishable from
// one that passed, which is the failure mode the whole command exists to remove.
func TestDoctorRefusesWithNoCheckSelected(t *testing.T) {
	cmd := doctorCommand(config.Default())
	err := cmd.Run(context.Background(), []string{"doctor"})
	if err == nil {
		t.Error("doctor with no check selected exited 0, which reads as a healthy palace")
	}
	if err != nil && !strings.Contains(err.Error(), "--index") {
		t.Errorf("the refusal does not name the flag that would run a check: %v", err)
	}
}

// TestDoctorHelpExplainsWhyItExists pins the operator contract, not merely the
// command name. The modes came from different incidents and experiments, so a
// list of flags does not explain which ones are integrity gates, which ones are
// measurements, or whether running them changes the evidence being inspected.
func TestDoctorHelpExplainsWhyItExists(t *testing.T) {
	cmd := doctorCommand(config.Default())
	description := cmd.Description
	for _, want := range []string{
		"silent failures",
		"does not migrate",
		"integrity checks",
		"diagnostic reports",
		"--index",
		"--schema",
		"--roles",
		"--graph",
		"--windows",
	} {
		if !strings.Contains(description, want) {
			t.Errorf("doctor --help does not explain %q:\n%s", want, description)
		}
	}

	var rolesUsage string
	for _, flag := range cmd.Flags {
		if f, ok := flag.(*cli.BoolFlag); ok && f.Name == "roles" {
			rolesUsage = f.Usage
		}
	}
	for _, want := range []string{"member roles", "missing membership rows", "empty roles"} {
		if !strings.Contains(rolesUsage, want) {
			t.Errorf("doctor --roles help does not explain %q: %q", want, rolesUsage)
		}
	}
}

// TestDoctorModesDoNotMigrateBeforeChecking proves the advertised read-only
// boundary through every mode that builds the service stack. Version 22 with a
// missing search_events table is deliberate: migration 23 would repair it, so
// any mode that quietly runs goose destroys the evidence and fails this test.
func TestDoctorModesDoNotMigrateBeforeChecking(t *testing.T) {
	tests := map[string]func(context.Context, config.Config) error{
		"index": func(ctx context.Context, cfg config.Config) error {
			return doctorIndex(ctx, cfg, "local", io.Discard)
		},
		"graph": func(ctx context.Context, cfg config.Config) error {
			return doctorGraph(ctx, cfg, "local", io.Discard)
		},
		"roles": func(ctx context.Context, cfg config.Config) error {
			return doctorRoles(ctx, cfg, io.Discard)
		},
		"schema": func(ctx context.Context, cfg config.Config) error {
			return doctorSchema(ctx, cfg, io.Discard)
		},
		"windows": func(ctx context.Context, cfg config.Config) error {
			return doctorWindows(ctx, cfg, "local", "query", "missing-drawer", io.Discard)
		},
	}

	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := config.Default()
			cfg.DBPath = doctorDriftedDB(t)
			journalMode := doctorJournalMode(t, cfg.DBPath)

			_ = run(context.Background(), cfg)
			assertDoctorDidNotMigrate(t, cfg.DBPath)
			if got := doctorJournalMode(t, cfg.DBPath); got != journalMode {
				t.Errorf("doctor changed journal mode from %q to %q", journalMode, got)
			}
		})
	}
}

// TestBuildServicesStillPreparesThePalace pins the other side of the inspection
// split through the production composition seam. Doctor must preserve drift;
// serving and ordinary CLI paths must still migrate and rebuild an empty index.
func TestBuildServicesStillPreparesThePalace(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.DBPath = doctorDriftedDB(t)
	cfg.VectorBackend = config.VectorBackendChromem

	gdb, err := openWriterDB(cfg.DBPath, false)
	if err != nil {
		t.Fatalf("open drifted database: %v", err)
	}
	if err := sqlitevec.New(gdb).Upsert(ctx, "team-local", []store.Point{{
		ID: "point-a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"wing": "wing_one"},
	}}); err != nil {
		t.Fatalf("seed source-of-truth vector: %v", err)
	}
	if sqlDB, err := gdb.DB(); err != nil {
		t.Fatalf("sql handle: %v", err)
	} else if err := sqlDB.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	if sqlDB, err := svc.gdb.DB(); err == nil {
		defer sqlDB.Close()
	}

	var version int
	if err := svc.gdb.Raw(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version).Error; err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	if want := latestEmbeddedMigration(t); version != want {
		t.Errorf("ordinary preparation left goose at %d, want %d", version, want)
	}
	var searchEvents int
	if err := svc.gdb.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'search_events'`).Scan(&searchEvents).Error; err != nil {
		t.Fatalf("find repaired table: %v", err)
	}
	if searchEvents != 1 {
		t.Error("ordinary preparation did not repair search_events")
	}

	hybrid, ok := svc.vectors.(*store.Hybrid)
	if !ok {
		t.Fatalf("prepared vector store = %T, want *store.Hybrid", svc.vectors)
	}
	_, index := hybrid.Halves()
	counter, ok := index.(interface {
		Count(context.Context, string) (int, error)
	})
	if !ok {
		t.Fatalf("prepared index = %T, want a countable Chromem index", index)
	}
	if n, err := counter.Count(ctx, "team-local"); err != nil || n != 1 {
		t.Errorf("ordinary preparation rebuilt %d point(s), want 1 (err=%v)", n, err)
	}
}

// TestDoctorIndexDoesNotReplaceAStaleChromemLayout covers the second write the
// normal serving constructor performs: it discards an older derived-index
// layout before replaying SQLite. That is correct at boot and destructive in a
// diagnostic, where the stale layout is the evidence being inspected.
func TestDoctorIndexDoesNotReplaceAStaleChromemLayout(t *testing.T) {
	cfg := config.Default()
	cfg.DBPath = doctorDriftedDB(t)
	cfg.VectorBackend = config.VectorBackendChromem

	stale := filepath.Join(config.ChromemPath(cfg.DBPath), "team1", "old-index.gob")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatalf("seed stale chromem directory: %v", err)
	}
	if err := os.WriteFile(stale, []byte("evidence"), 0o644); err != nil {
		t.Fatalf("seed stale chromem file: %v", err)
	}

	_ = doctorIndex(context.Background(), cfg, "local", io.Discard)
	if got, err := os.ReadFile(stale); err != nil || string(got) != "evidence" {
		t.Errorf("doctor replaced the stale chromem evidence: content=%q err=%v", got, err)
	}
}

// TestDatabaseDoctorModesIgnoreABrokenChromemIndex proves one failed subsystem
// cannot hide answers about another. These modes read SQLite only, so a missing
// or stale derived index is irrelevant evidence rather than a startup blocker.
func TestDatabaseDoctorModesIgnoreABrokenChromemIndex(t *testing.T) {
	cfg := config.Default()
	cfg.DBPath = doctorDriftedDB(t)
	cfg.VectorBackend = config.VectorBackendChromem

	sqlDB, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO teams (id, name, slug, created_at) VALUES ('team-local', 'Local', 'local', '2026-08-23T00:00:00Z')`); err != nil {
		sqlDB.Close()
		t.Fatalf("seed local workspace: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	stale := filepath.Join(config.ChromemPath(cfg.DBPath), "team-local", "old-index.gob")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatalf("seed stale chromem directory: %v", err)
	}
	if err := os.WriteFile(stale, []byte("evidence"), 0o644); err != nil {
		t.Fatalf("seed stale chromem file: %v", err)
	}

	var graph bytes.Buffer
	if err := doctorGraph(context.Background(), cfg, "local", &graph); err != nil {
		t.Errorf("graph was blocked by an unrelated index fault: %v", err)
	} else if !strings.Contains(graph.String(), "graph:") {
		t.Errorf("graph did not reach its report: %q", graph.String())
	}

	var roles bytes.Buffer
	if err := doctorRoles(context.Background(), cfg, &roles); err != nil {
		t.Errorf("roles was blocked by an unrelated index fault: %v", err)
	} else if !strings.Contains(roles.String(), "roles:") {
		t.Errorf("roles did not reach its report: %q", roles.String())
	}

	var schema bytes.Buffer
	err = doctorSchema(context.Background(), cfg, &schema)
	if err == nil || !strings.Contains(schema.String(), "MISSING") {
		t.Errorf("schema did not report the deliberately missing table: output=%q err=%v", schema.String(), err)
	}

	err = doctorWindows(context.Background(), cfg, "local", "query", "missing-drawer", io.Discard)
	if !errors.Is(err, palace.ErrNotFound) {
		t.Errorf("windows did not reach the drawer lookup: %v", err)
	}
	if got, err := os.ReadFile(stale); err != nil || string(got) != "evidence" {
		t.Errorf("database diagnostics changed the unrelated index: content=%q err=%v", got, err)
	}
}

// TestInspectServicesOpensSQLiteQueryOnly makes the read-only boundary survive
// future diagnostic code. Avoiding today's migrations is insufficient if a new
// report can write through the repositories it receives tomorrow.
func TestInspectServicesOpensSQLiteQueryOnly(t *testing.T) {
	cfg := config.Default()
	cfg.DBPath = doctorDriftedDB(t)

	svc, err := inspectServices(cfg)
	if err != nil {
		t.Fatalf("inspect services: %v", err)
	}
	if err := svc.gdb.Exec(`UPDATE plans SET name = 'changed' WHERE code = 'personal'`).Error; err == nil {
		t.Fatal("inspection service accepted a database write")
	}
}

func doctorDriftedDB(t *testing.T) string {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "drifted.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.UpTo(sqlDB, "migrations", 22); err != nil {
		t.Fatalf("up to 22: %v", err)
	}
	if _, err := sqlDB.Exec(`DROP TABLE search_events`); err != nil {
		t.Fatalf("drop search_events: %v", err)
	}
	return dbPath
}

func assertDoctorDidNotMigrate(t *testing.T, dbPath string) {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	defer sqlDB.Close()

	var version int
	if err := sqlDB.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	if version != 22 {
		t.Errorf("doctor advanced goose from 22 to %d", version)
	}

	var tables int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'search_events'`).Scan(&tables); err != nil {
		t.Fatalf("find search_events: %v", err)
	}
	if tables != 0 {
		t.Error("doctor repaired search_events before reporting on the database")
	}
}

func doctorJournalMode(t *testing.T, dbPath string) string {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	var mode string
	if err := sqlDB.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	return mode
}

// TestDoctorIndexExitsNonZeroOnDrift pins that the VERDICT is the exit code.
//
// The report is prose and prose is not a gate. A drift that printed a warning
// and exited 0 would sit green in every pipeline that runs this.
func TestDoctorIndexExitsNonZeroOnDrift(t *testing.T) {
	clean := palace.DriftReport{Checked: palace.NamespaceSplit{Drawers: 5}}
	drifted := palace.DriftReport{Checked: palace.NamespaceSplit{Drawers: 5}, Total: 1, Drifted: []palace.DriftedPoint{
		{Store: "index", DrawerID: "d1", Indexed: "wing_acme-legacy", Actual: "wing_acme"},
	}}

	var buf bytes.Buffer
	if err := reportDrift(&buf, clean); err != nil {
		t.Errorf("a clean palace exited non-zero: %v", err)
	}
	if !strings.Contains(buf.String(), "agrees") {
		t.Errorf("a clean report does not say so: %q", buf.String())
	}

	buf.Reset()
	err := reportDrift(&buf, drifted)
	if err == nil {
		t.Error("drift was reported and the command exited 0 — the verdict has to be the exit code")
	}
	out := buf.String()
	for _, want := range []string{"d1", "wing_acme-legacy", "wing_acme", "index"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not name %q, so a reader cannot act on it:\n%s", want, out)
		}
	}
}

var _ = cli.Command{}

// TestDoctorDistinguishesAnAbsentPointFromAMislabelledOne: they need different
// actions, so they must not read the same.
//
// A mislabelled memory answers the wrong wing; an ABSENT one answers nothing at
// all and is fixed by a sync rather than by a merge. Reporting them identically
// sends an operator to the wrong repair.
func TestDoctorDistinguishesAnAbsentPointFromAMislabelledOne(t *testing.T) {
	var buf bytes.Buffer
	err := reportDrift(&buf, palace.DriftReport{Checked: palace.NamespaceSplit{Drawers: 2}, Total: 2, Drifted: []palace.DriftedPoint{
		{Store: "index", DrawerID: "d1", Indexed: "wing_acme-legacy", Actual: "wing_acme"},
		{Store: "index", DrawerID: "d2", Actual: "wing_acme", Missing: true},
	}})
	if err == nil {
		t.Error("drift reported and the command exited 0")
	}
	out := buf.String()
	if !strings.Contains(out, "ABSENT") {
		t.Errorf("a drawer with no point at all is not marked absent:\n%s", out)
	}
	if !strings.Contains(out, "sync") {
		t.Errorf("the report does not name the repair for an absent point:\n%s", out)
	}
}

// TestDoctorBoundsItsListingAndKeepsTheCountExact: a fully drifted palace must
// produce a report a human can read, and a count they can trust.
func TestDoctorBoundsItsListingAndKeepsTheCountExact(t *testing.T) {
	var buf bytes.Buffer
	_ = reportDrift(&buf, palace.DriftReport{Checked: palace.NamespaceSplit{Drawers: 5000}, Total: 5000, Drifted: []palace.DriftedPoint{
		{Store: "index", DrawerID: "d1", Indexed: "wing_acme-legacy", Actual: "wing_acme"},
	}})
	out := buf.String()
	if !strings.Contains(out, "5000 stored point(s) disagree") {
		t.Errorf("the exact count is not reported:\n%s", out)
	}
	if !strings.Contains(out, "4999 more, not listed") {
		t.Errorf("the listing was truncated without saying so — silent truncation reads as "+
			"'that was all of them':\n%s", out)
	}
}

// TestDoctorSaysPendingEmbeddingIsNotAFault: a drawer awaiting its first
// embedding has no point yet, and a busy palace must not look broken.
func TestDoctorSaysPendingEmbeddingIsNotAFault(t *testing.T) {
	var buf bytes.Buffer
	if err := reportDrift(&buf, palace.DriftReport{Checked: palace.NamespaceSplit{Drawers: 10}, Pending: palace.NamespaceSplit{Drawers: 3}}); err != nil {
		t.Errorf("a clean palace with a queue exited non-zero: %v", err)
	}
	if !strings.Contains(buf.String(), "queue and not a fault") {
		t.Errorf("pending embeddings are not explained:\n%s", buf.String())
	}
}

// TestDoctorRefusesAMissingDatabase: openDB CREATES a missing file and the
// migrations fill it, so a mistyped --db built an empty palace and reported it
// clean. "The path was wrong" and "the palace is healthy" must not be the same
// output — and the check must not leave a database behind either.
func TestDoctorRefusesAMissingDatabase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-palace.db")
	err := doctorIndex(context.Background(), config.Config{DBPath: missing}, "local", io.Discard)
	if err == nil {
		t.Fatal("doctor inspected a database that does not exist")
	}
	if !strings.Contains(err.Error(), "no database") {
		t.Errorf("the refusal does not name the cause: %v", err)
	}
	if _, statErr := os.Stat(missing); statErr == nil {
		t.Error("doctor created the database it was asked to inspect")
	}
}

// TestDoctorDoesNotReconcileBeforeChecking protects the two inspection seams.
// Only --index may open the selected vector backend; the other three modes ask
// SQLite-only questions and must remain independent of a broken search index.
func TestDoctorDoesNotReconcileBeforeChecking(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "server", "doctor.go"))
	if err != nil {
		t.Fatalf("read doctor.go: %v", err)
	}
	if regexp.MustCompile(`buildServices\(`).Match(src) {
		t.Error("doctor.go calls buildServices, which migrates and reconciles before the check can look")
	}
	if got := len(regexp.MustCompile(`inspectServices\(cfg\)`).FindAll(src, -1)); got != 1 {
		t.Errorf("doctor.go routes %d modes through vector inspection, want only --index", got)
	}
	if got := len(regexp.MustCompile(`inspectDatabaseServices\(cfg\)`).FindAll(src, -1)); got != 3 {
		t.Errorf("doctor.go routes %d SQLite-only modes through database inspection, want 3", got)
	}
}

// TestDoctorTakesTheLocalFlag: --local is what switches the search index to
// chromem, so a doctor without it inspects a bare SQLite store while chromem
// serves every query — and exits 0 on a broken palace.
func TestDoctorTakesTheLocalFlag(t *testing.T) {
	cmd := doctorCommand(config.Default())
	var found bool
	for _, f := range cmd.Flags {
		for _, n := range f.Names() {
			if n == "local" {
				found = true
			}
		}
	}
	if !found {
		t.Error("doctor has no --local flag, so on a self-hosted install it checks a backend nobody runs")
	}
}

// readRepoFile reads a file from the repository root, for the checks that can
// only be made against the source because their subject needs a live service.
func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{repoRoot(t)}, parts...)...))
	if err != nil {
		t.Fatalf("read %v: %v", parts, err)
	}
	return string(b)
}

// TestDoctorRolesIsSelectable: the role check is reachable from the command
// line. A check with no flag to select it is the exact shape this repository has
// shipped repeatedly — finished, tested, and selected by nothing — so this reads
// the command's own flag list rather than the source.
func TestDoctorRolesIsSelectable(t *testing.T) {
	var names []string
	for _, f := range doctorCommand(config.Default()).Flags {
		names = append(names, f.Names()...)
	}
	found := false
	for _, n := range names {
		if n == "roles" {
			found = true
		}
	}
	if !found {
		t.Errorf("doctor offers %v and not \"roles\" — the check exists and cannot be run", names)
	}
}

// TestDoctorRolesRunsWhenSelected: declaring the flag is half of it. A flag the
// Action never consults leaves `doctor --roles` exiting 0 on a database it never
// opened, which reads as a clean palace.
func TestDoctorRolesRunsWhenSelected(t *testing.T) {
	cmd := doctorCommand(config.Default())
	err := cmd.Run(context.Background(), []string{"doctor", "--roles", "--db", filepath.Join(t.TempDir(), "absent.db")})
	if err == nil {
		t.Fatal("doctor --roles exited 0 against a database that does not exist")
	}
	if !strings.Contains(err.Error(), "no database at") {
		t.Errorf("--roles did not reach the check: %v", err)
	}
}

// TestDoctorRolesExitsNonZeroOnRefusals pins that the VERDICT is the exit code,
// and that a clean report is not an error. Prose is not a gate.
func TestDoctorRolesExitsNonZeroOnRefusals(t *testing.T) {
	if err := reportRefusedWrites(io.Discard, nil); err != nil {
		t.Errorf("a palace where every key may write reported an error: %v", err)
	}
	// A DELIBERATE member role alone must still fail: those agents stop writing
	// on upgrade, and a green exit here is exactly the silence being fixed.
	member := []tenant.ReadOnlyKeys{{TeamID: "team-a", Slug: "acme", Member: 3}}
	if err := reportRefusedWrites(io.Discard, member); err == nil {
		t.Error("member-role keys printed a warning and exited 0, which reads as nobody affected")
	}
}

// TestDoctorRolesSeparatesAChoiceFromAFault: promoting a teammate and repairing
// a broken row are different actions, so the report must not blur them.
func TestDoctorRolesSeparatesAChoiceFromAFault(t *testing.T) {
	out := &bytes.Buffer{}
	_ = reportRefusedWrites(out, []tenant.ReadOnlyKeys{
		{TeamID: "team-a", Slug: "acme", Member: 2, Missing: 1},
	})
	got := out.String()
	for _, want := range []string{"acme", "3 active key(s)", "promote them to writer", "historical data"} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}

	clean := &bytes.Buffer{}
	_ = reportRefusedWrites(clean, []tenant.ReadOnlyKeys{{TeamID: "team-a", Slug: "acme", Member: 2}})
	if strings.Contains(clean.String(), "historical data") {
		t.Errorf("a workspace with no data faults was told to repair one:\n%s", clean.String())
	}
}

// TestDoctorRolesNamesTheWorkspaceNotTheKey: a doctor report is pasted into an
// issue, so it carries slugs and counts and never key material.
func TestDoctorRolesNamesTheWorkspaceNotTheKey(t *testing.T) {
	out := &bytes.Buffer{}
	_ = reportRefusedWrites(out, []tenant.ReadOnlyKeys{{TeamID: "team-a", Slug: "acme", Member: 2, Empty: 1}})
	got := out.String()
	if !strings.Contains(got, "acme") {
		t.Errorf("report does not name the workspace:\n%s", got)
	}
	if strings.Contains(got, "team-a") {
		t.Errorf("report leaks the internal team id where a slug would do:\n%s", got)
	}
}
