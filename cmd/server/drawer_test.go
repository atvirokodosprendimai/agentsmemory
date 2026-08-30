package main

import (
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// ADR-038 T4. Erasure left the agent surface, so this is the ONLY place a record
// can still be destroyed — and the coverage the removed am_delete_wing scenario
// used to carry has to land here, or the boundary was moved to a path nothing
// exercises.

// eraseTestWorkspace stands up a throwaway local database with one workspace and
// returns the config plus its team id.
func eraseTestWorkspace(t *testing.T) (config.Config, string) {
	t.Helper()
	cfg := config.Default()
	cfg.DBPath = t.TempDir() + "/agentsmemory.db"
	cfg.VectorBackend = config.VectorBackendSQLite

	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	team, err := svc.tenants.EnsureLocalWorkspace(t.Context())
	if err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	sqlDB, err := svc.gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return cfg, team.TeamID
}

func TestDrawerEraseNeedsTheIdSpelledTwice(t *testing.T) {
	cfg, teamID := eraseTestWorkspace(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	added, err := svc.drawers.Add(t.Context(), teamID, palace.AddInput{
		Wing: "wing_alpha", Room: "decisions", Content: "a memory that must survive a mistyped erase",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := added.Drawers[0].ID

	err = eraseDrawer(context.Background(), cfg, "local", id, id+"x")
	if err == nil {
		t.Fatal("a mismatched --confirm erased the memory; nothing about an erase is recoverable and " +
			"nothing records that it happened, so one typo must not be enough")
	}
	if !strings.Contains(err.Error(), "nothing was erased") {
		t.Errorf("the refusal does not say the memory survived: %v", err)
	}

	svc2, _ := buildServices(cfg)
	if _, err := svc2.drawers.Get(t.Context(), teamID, id); err != nil {
		t.Fatalf("the refused erase destroyed the memory anyway: %v", err)
	}
}

func TestDrawerEraseRemovesEveryChunk(t *testing.T) {
	cfg, teamID := eraseTestWorkspace(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	// Multi-chunk on purpose: erasing the parent and leaving the children is the
	// defect that shaped Delete, and an operator path that reintroduces it destroys
	// half a leaked secret.
	long := strings.Repeat("the token is hunter2 and this sentence forces chunking. ", 40)
	added, err := svc.drawers.Add(t.Context(), teamID, palace.AddInput{
		Wing: "wing_alpha", Room: "decisions", Content: long,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("fixture is %d chunk(s); this test needs several", len(added.Drawers))
	}

	if err := eraseDrawer(context.Background(), cfg, "local", added.Drawers[0].ID, added.Drawers[0].ID); err != nil {
		t.Fatalf("erase: %v", err)
	}

	svc2, _ := buildServices(cfg)
	for _, d := range added.Drawers {
		if _, err := svc2.drawers.Get(t.Context(), teamID, d.ID); err == nil {
			t.Errorf("chunk %d outlived the erase; an erase that leaves part of the text is worse "+
				"than none, because the operator believes the secret is gone", d.ChunkIndex)
		}
	}
}
