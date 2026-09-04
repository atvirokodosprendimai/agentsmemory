package db

// WriterPragmas are the connection pragmas appended to the SQLite DSN of every
// handle that serves the live database — the server's writer, its readers, and
// the test harness in internal/mcptest.
//
// It lives here rather than beside the openers in cmd/server because cmd/server
// is package main and cannot be imported, and ADR-052 T3 requires exactly ONE
// spelling of this string in the tree: the harness used to open with no
// pragmas at all, so every scenario there measured a rollback-journal database
// with foreign keys off while the server ran WAL with them on. A second copy
// kept in step by hand is the drift that test found; a shared constant is the
// only form that cannot diverge. TestTheHarnessNamesOneDSNSource reads the
// harness's open call out of the AST and sweeps the module for another literal.
//
// journal_mode(WAL): readers no longer block the writer, so `inspect` or a data
// export can run against a live server instead of colliding with it. WAL is a
// persistent property of the database file, so the first connection converts it
// and every later one simply observes it.
//
// busy_timeout(5000): SQLite's default is 0 — a contended write fails
// *instantly* with "database is locked" rather than waiting. Five seconds turns
// the normal case (a reader and the writer overlapping for microseconds) into a
// brief wait instead of an error. It does NOT cover a deferred transaction's
// lock upgrade, which is why the writer is one connection (see openWriterDB).
//
// foreign_keys(1): SQLite enforces declared foreign keys per connection and
// off by default; ADR-052 T2 turned it on for serving connections so the schema
// the migrations declare is the schema the data obeys.
//
// Deliberately NOT applied to the data-export archive (internal/dataexport):
// that file is handed to the user as a single download, and WAL would leave
// committed rows in a -wal sidecar that does not travel with it.
//
// This is a concurrency *performance* setting and nothing more. It is not a
// substitute for the single-instance lock in cmd/server/lock.go — if anything
// it raises the stakes, because with WAL two servers on one database write
// happily and silently instead of announcing themselves with lock errors.
const WriterPragmas = "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
