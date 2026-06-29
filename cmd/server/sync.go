package main

import (
	"context"
	"fmt"
	"log"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/urfave/cli/v3"
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
		Flags: dataFlags(def),
		Action: func(ctx context.Context, c *cli.Command) error {
			return syncIndex(ctx, configFromCmd(c, def))
		},
	}
}

// syncIndex performs the source-of-truth -> index replay for every namespace.
func syncIndex(ctx context.Context, cfg config.Config) error {
	if cfg.VectorBackend != config.VectorBackendQdrant {
		return fmt.Errorf("sync needs --vector-backend qdrant: with the sqlite backend the " +
			"source of truth IS the search index, so there is nothing to sync")
	}

	gdb, err := openDB(cfg.DBPath, cfg.Debug)
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

	log.Printf("sync: replaying %d namespace(s) from SQLite into Qdrant (%s)", len(namespaces), cfg.QdrantURL)
	var failed int
	for _, ns := range namespaces {
		// Stop promptly on Ctrl-C; already-synced namespaces stay synced (the replay
		// is idempotent), so a re-run resumes cleanly.
		if err := ctx.Err(); err != nil {
			return err
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
