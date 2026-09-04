package main

import (
	"context"
	"fmt"
	"log"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

// syncCommand replays the SQLite source of truth into the Qdrant search index: for
// every tenant namespace it creates the collection if missing and upserts all
// stored vectors — WITHOUT re-embedding, since the vectors already live in SQLite.
//
// SQLite is always the source of truth (Hybrid writes it first), so this is the
// one operation needed to (re)populate Qdrant: run it after first pointing the
// server at the Qdrant backend, after a Qdrant data loss, or to reconcile an index
// that fell behind. It is ADDITIVE — it does not prune points that no longer exist
// in the source of truth.
func syncCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "sync",
		Usage: "Replay every tenant's vectors from the SQLite source of truth into Qdrant (creating collections as needed)",
		Flags: append(dataFlags(def),
			&cli.BoolFlag{
				Name:  "recreate",
				Usage: "drop each tenant's Qdrant collection and rebuild it from scratch (prunes points no longer in the source of truth); without it, sync is additive (upsert only)",
			},
			&cli.BoolFlag{
				Name: "repair-payload",
				Usage: "attach wing/room payload to points that lack it, WITHOUT re-embedding — needed once for a palace whose vectors " +
					"were written before scoped search filtered on payload, where every scoped search otherwise returns nothing",
			},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return syncIndex(ctx, configFromCmd(c, def), c.Bool("recreate"), c.Bool("repair-payload"))
		},
	}
}

// syncIndex performs the source-of-truth -> index replay for every namespace.
// When recreate is set, each tenant's Qdrant collection is dropped first so the
// rebuild prunes points that no longer exist in the source of truth; otherwise the
// replay is purely additive (upsert).
func syncIndex(ctx context.Context, cfg config.Config, recreate, repairPayload bool) error {
	if cfg.VectorBackend != config.VectorBackendQdrant {
		return fmt.Errorf("sync needs --vector-backend qdrant: with the sqlite backend the " +
			"source of truth IS the search index, and the chromem backend refills an empty " +
			"index from SQLite when the server boots (delete its .chromem directory to force one)")
	}

	// sync only READS SQLite — it writes to the vector index — but it opens
	// through the writer handle rather than a reader one, because T4 has not
	// built a reader yet and a writable handle is the incumbent behaviour.
	// ADR-052 moves this to the reader handle in T4.
	gdb, err := openWriterDB(cfg.DBPath, cfg.Debug)
	if err != nil {
		return err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("sql handle: %w", err)
	}
	defer sqlDB.Close()
	// Idempotent: ensures the vectors table exists before we read from it.
	if err := migrate(sqlDB); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	sot := sqlitevec.New(gdb)
	index := qdrant.New(cfg.QdrantURL, cfg.QdrantAPIKey, cfg.HTTPTimeout)
	hybrid := store.NewHybrid(sot, index)

	namespaces, err := sot.Namespaces(ctx)
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}
	if len(namespaces) == 0 {
		log.Printf("sync: no vectors in the source of truth — nothing to do")
		return nil
	}

	mode := "upsert"
	if recreate {
		mode = "recreate"
	}
	log.Printf("sync: replaying %d namespace(s) from SQLite into Qdrant (%s, mode=%s)", len(namespaces), cfg.QdrantURL, mode)
	var failed int
	for _, ns := range namespaces {
		// Stop promptly on Ctrl-C; already-synced namespaces stay synced (the replay
		// is idempotent), so a re-run resumes cleanly.
		if err := ctx.Err(); err != nil {
			return err
		}
		// --recreate: drop the collection first so Rebuild's EnsureNamespace makes a
		// fresh one and the upsert leaves only what the source of truth still holds.
		// Drop-then-rebuild per namespace (not all-drops-then-all-rebuilds) keeps each
		// tenant's search-down window to its own rebuild rather than the whole run.
		if recreate {
			if err := index.DeleteCollection(ctx, ns); err != nil {
				failed++
				log.Printf("sync: namespace %q DROP FAILED: %v", ns, err)
				continue
			}
		}
		if repairPayload {
			n, err := repairNamespacePayload(ctx, gdb, hybrid, ns)
			if err != nil {
				return fmt.Errorf("repair payload for %q: %w", ns, err)
			}
			log.Printf("sync: namespace %q — payload repaired on %d point(s)", ns, n)
			continue
		}
		if err := hybrid.Rebuild(ctx, ns); err != nil {
			failed++
			log.Printf("sync: namespace %q FAILED: %v", ns, err)
			continue
		}
		log.Printf("sync: namespace %q ok", ns)
	}
	if failed > 0 {
		return fmt.Errorf("sync finished with %d of %d namespace(s) failed", failed, len(namespaces))
	}
	log.Printf("sync: done — %d namespace(s) in sync", len(namespaces))
	return nil
}

// repairNamespacePayload attaches wing/room payload to a namespace's points from
// the DRAWER ROWS, which are authoritative about where a memory lives.
//
// It is deliberately not a re-embedding: the vectors are fine, only their labels
// are missing, and on a large palace the difference is minutes against days. The
// rows are paged so a corpus of any size costs bounded memory, and points are
// grouped by (wing, room) so one HTTP call labels thousands at a time.
//
// dst is the HYBRID, not the index. It was the raw Qdrant client, which repaired
// the index and left the source of truth stale — so the very next `sync` replayed
// that stale payload back over the repair. Two commands that undo each other, and
// the one an operator reaches for first is the one that loses. A reviewer caught
// this AFTER a commit message claimed it had been fixed; the type of the payload
// map had been changed and the receiver had not.
func repairNamespacePayload(ctx context.Context, gdb *gorm.DB, dst store.VectorStore, namespace string) (int, error) {
	const page = 2000
	repo := palace.NewRepo(gdb)
	repaired := 0
	for offset := 0; ; offset += page {
		drawers, err := repo.List(ctx, namespace, "", "", page, offset)
		if err != nil {
			return repaired, err
		}
		if len(drawers) == 0 {
			return repaired, nil
		}
		byLabel := map[[2]string][]string{}
		for _, d := range drawers {
			key := [2]string{d.Wing, d.Room}
			byLabel[key] = append(byLabel[key], d.ID)
		}
		for label, ids := range byLabel {
			patch := map[string]string{"wing": label[0], "room": label[1]}
			for start := 0; start < len(ids); start += 512 {
				end := start + 512
				if end > len(ids) {
					end = len(ids)
				}
				if err := dst.SetPayload(ctx, namespace, ids[start:end], patch); err != nil {
					return repaired, err
				}
				repaired += end - start
			}
		}
	}
}
