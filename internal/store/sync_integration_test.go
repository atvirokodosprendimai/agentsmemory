package store_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	dbpkg "github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	glebarez "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestSyncReplaysSQLiteIntoQdrant is the end-to-end of the `sync` command minus
// the CLI wrapper: real SQLite source of truth + the real Qdrant REST client
// pointed at a fake Qdrant. It proves Namespaces -> Rebuild -> Qdrant upsert
// actually pushes the stored points, so a "0 in Qdrant" report can be pinned to
// the environment (empty vectors table / wrong URL) rather than the code.
func TestSyncReplaysSQLiteIntoQdrant(t *testing.T) {
	ctx := context.Background()

	// --- fake Qdrant: record collection creates + point upserts ---
	var mu sync.Mutex
	created := map[string]bool{}
	upserted := map[string]int{} // collection points-path -> total points received
	upsertCalls := 0             // how many PUT /points requests (proves batching)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet: // collectionExists probe -> say "not yet"
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/points"):
			var body struct {
				Points []json.RawMessage `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			upserted[r.URL.Path] += len(body.Points)
			upsertCalls++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut: // create collection
			created[r.URL.Path] = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	// --- real SQLite source of truth: one big namespace (forces batching) + one
	// small closet namespace, mirroring the prod shape that exposed the bug. ---
	sot := newSoT(t)
	const big = 600 // > upsertBatch (256), so the replay must span multiple requests
	bigPoints := make([]store.Point, big)
	for i := range bigPoints {
		bigPoints[i] = store.Point{ID: "d" + strconv.Itoa(i), Vector: []float32{float32(i), 1, 0}}
	}
	if err := sot.Upsert(ctx, "team-a", bigPoints); err != nil {
		t.Fatalf("seed team-a: %v", err)
	}
	if err := sot.Upsert(ctx, "team-a::closets", []store.Point{
		{ID: "c1", Vector: []float32{0, 0, 1}},
	}); err != nil {
		t.Fatalf("seed closets: %v", err)
	}

	// --- the sync core: list namespaces, Rebuild each into Qdrant ---
	index := qdrant.New(srv.URL, "", 5*time.Second)
	hybrid := store.NewHybrid(sot, index)

	nss, err := sot.Namespaces(ctx)
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}
	if len(nss) != 2 {
		t.Fatalf("namespaces = %v, want 2", nss)
	}
	for _, ns := range nss {
		if err := hybrid.Rebuild(ctx, ns); err != nil {
			t.Fatalf("rebuild %q: %v", ns, err)
		}
	}

	// --- assert Qdrant received every point, across MULTIPLE batched requests ---
	total := 0
	for _, n := range upserted {
		total += n
	}
	if total != big+1 {
		t.Errorf("qdrant received %d points across %d collection(s), want %d — upserted=%v",
			total, len(upserted), big+1, upserted)
	}
	// 600 points / 256 per batch = 3 requests for the big namespace + 1 for the
	// closet namespace; the key point is it is NOT a single oversized PUT.
	if upsertCalls < 3 {
		t.Errorf("upsert requests = %d, want batched (>=3) — a single giant PUT is the bug", upsertCalls)
	}
	if len(created) == 0 {
		t.Errorf("expected at least one collection to be created, got none")
	}
}

// newSoT opens a throwaway migrated SQLite and returns the vector source of truth.
func newSoT(t *testing.T) *sqlitevec.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sot.db")
	gdb, err := gorm.Open(glebarez.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	goose.SetBaseFS(dbpkg.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return sqlitevec.New(gdb)
}
