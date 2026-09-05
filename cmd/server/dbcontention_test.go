package main

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"gorm.io/gorm"
)

// contentionRow is the throwaway model this file's transactions contend over.
// Two columns: one to address the row by, one to write.
type contentionRow struct {
	ID    uint `gorm:"primaryKey"`
	Value int
}

// TableName keeps the throwaway model out of the namespace the migrations own,
// so a future table called "contention_rows" cannot collide with it.
func (contentionRow) TableName() string { return "adr052_contention" }

// bumpThroughAHelper reads the row and then writes it, with the read hidden
// inside this function rather than visible at the call site.
//
// The shape is deliberate and it is the shape of `internal/palace/service.go`'s
// ADR-045 relocation: the read that turns a deferred transaction into a lock
// UPGRADE lives inside `Repo.Update`, so a reviewer scanning the transaction
// body sees an update and no select. Of the six read-then-write sites ADR-052
// names, this is the one a future change reintroduces, because it is the one
// nothing at the call site shows.
func bumpThroughAHelper(tx *gorm.DB, id uint) error {
	var row contentionRow
	if err := tx.First(&row, id).Error; err != nil {
		return err
	}
	return tx.Model(&contentionRow{}).Where("id = ?", id).
		Update("value", row.Value+1).Error
}

// TestAReadThenWriteTransactionSurvivesConcurrentWriters asserts that a
// transaction which reads before it writes completes under concurrency.
//
// ⚠ IT IS RED AT THE TREE THAT INTRODUCED IT, AND THAT IS THE POINT. ADR-052
// measured 273-281 failures of 320 on the shipped DSN with an uncapped pool,
// five runs, every one "database is locked". The mechanism is a deferred
// transaction's lock UPGRADE: SQLite grants the write lock on the first write
// statement, and on an upgrade conflict it returns SQLITE_BUSY IMMEDIATELY
// rather than invoking the busy handler — so `busy_timeout(5000)` in db.WriterPragmas
// does not cover this case however large it is set. In WAL that covers two
// distinct returns, a held writer lock and a stale snapshot (BUSY_SNAPSHOT),
// and no amount of waiting fixes the second.
//
// ⚠ THE MUTANT THIS TEST MUST DIE TO IS `SetMaxOpenConns(1)` DELETED FROM THE
// WRITER OPENER — not a DSN flag. Measured through ONE handle: uncapped 280 of
// 320, capped 0 of 320, and capped scores 0 with or without `_txlock=immediate`.
// If this test ever passes against an uncapped handle, the measurement did not
// reproduce on the executing machine and ADR-052 T1's Stop Condition applies:
// stop and re-take the measurement rather than assuming the defect is gone.
//
// The behaviour is pinned to github.com/glebarez/sqlite v1.11.0 over
// modernc.org/sqlite v1.49.1 (see go.mod). A driver bump is a reason to re-read
// this test, never a reason to delete it.
//
// The assertion is zero failures rather than "fewer than N": it states the
// invariant ADR-052 decides, not the number today's tree happens to produce.
func TestAReadThenWriteTransactionSurvivesConcurrentWriters(t *testing.T) {
	t.Parallel()

	// Opened through openWriterDB rather than a hand-assembled DSN, so this
	// measures whatever the shipped constant says rather than a copy of it that
	// can drift.
	// whatever the shipped constant says rather than a copy of it that can drift.
	path := filepath.Join(t.TempDir(), "contention.db")
	db, err := openWriterDB(path, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := db.AutoMigrate(&contentionRow{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&contentionRow{ID: 1, Value: 0}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	const (
		writers = 8
		perGo   = 40
	)

	var (
		mu     sync.Mutex
		errs   []error
		wg     sync.WaitGroup
		lockRe = "database is locked"
	)

	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perGo {
				err := db.Transaction(func(tx *gorm.DB) error {
					return bumpThroughAHelper(tx, 1)
				})
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if len(errs) == 0 {
		return
	}

	// Name the count and the first error verbatim. The count is what the ADR's
	// table is comparable against; the string is what distinguishes this defect
	// from a filesystem without working locking, which fails for an unrelated
	// reason on the same code path.
	first := errs[0].Error()
	locked := strings.Contains(first, lockRe)
	t.Errorf("%d of %d read-then-write transactions failed; first error: %v (contains %q: %v)",
		len(errs), writers*perGo, errs[0], lockRe, locked)
	if !locked {
		t.Errorf("the first failure is not the documented symptom — this may be an environment "+
			"problem rather than the lock upgrade ADR-052 measured: %v", errors.Unwrap(errs[0]))
	}
}

// TestServingConnectionsCarryTheirPragmas reads the pragmas back off a serving
// writer connection, rather than trusting that the DSN string was honoured.
//
// A DSN is a claim about a connection; this asserts the connection. The three
// values are load-bearing for different reasons and each fails silently on its
// own: journal_mode(WAL) is what lets `inspect` and an export read a live
// server, busy_timeout(5000) turns a microsecond overlap into a wait instead of
// an error, and foreign_keys(1) is what makes the five ON DELETE CASCADE
// clauses in 00001_init.sql mean anything — before ADR-052 it read 0 under the
// shipped DSN, so those clauses were decoration.
//
// ⚠ IT ALSO PINS THE WRITER COUNT, which is why it is here rather than beside
// a pragma helper: the same handle must report exactly one open connection,
// and that is the claim ADR-052 makes. Raising the cap turns this red before
// anything else in the tree notices.
func TestServingConnectionsCarryTheirPragmas(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pragmas.db")
	db, err := openWriterDB(path, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	for _, tc := range []struct {
		pragma string
		want   string
	}{
		{"foreign_keys", "1"},
		{"journal_mode", "wal"},
		{"busy_timeout", "5000"},
	} {
		var got string
		if err := db.Raw("PRAGMA " + tc.pragma).Scan(&got).Error; err != nil {
			t.Fatalf("read PRAGMA %s: %v", tc.pragma, err)
		}
		if !strings.EqualFold(got, tc.want) {
			t.Errorf("PRAGMA %s = %q, want %q — the DSN says otherwise, so the "+
				"connection is not honouring it", tc.pragma, got, tc.want)
		}
	}

	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("writer handle MaxOpenConnections = %d, want 1 — this cap IS the "+
			"write serialisation (ADR-052); with it gone, read-then-write "+
			"transactions fail about 280 times in 320", got)
	}
}

// TestTheReaderPragmasAreTheWritersPlusQueryOnly pins the relationship rather
// than the string, so a pragma added to the writer cannot be silently missing
// from the reader.
//
// ADR-052 T4 builds the reader handle; this task only defines its constant, and
// the property that matters is that the two DSNs cannot drift apart — a reader
// that quietly lost foreign_keys would enforce a different schema than the
// writer on the same file.
func TestTheReaderPragmasAreTheWritersPlusQueryOnly(t *testing.T) {
	t.Parallel()

	if !strings.HasPrefix(readerDBPragmas, db.WriterPragmas) {
		t.Fatalf("readerDBPragmas %q does not start with db.WriterPragmas %q", readerDBPragmas, db.WriterPragmas)
	}
	if got := strings.TrimPrefix(readerDBPragmas, db.WriterPragmas); got != "&_pragma=query_only(1)" {
		t.Errorf("readerDBPragmas adds %q; want only query_only(1) — anything else is "+
			"a second decision hiding in a constant", got)
	}
	if strings.Contains(db.WriterPragmas, "_txlock") || strings.Contains(readerDBPragmas, "_txlock") {
		t.Error("a _txlock pragma appeared: ADR-052 removes the second writer rather than " +
			"serialising in front of one, so its presence means the writer count stopped being one")
	}
}
