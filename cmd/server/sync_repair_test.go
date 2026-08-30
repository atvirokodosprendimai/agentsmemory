package main

import (
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// recordingPayloadStore counts SetPayload calls per store it is standing in for.
type recordingPayloadStore struct {
	store.VectorStore
	calls int
	ids   []string
}

func (r *recordingPayloadStore) SetPayload(_ context.Context, _ string, ids []string, _ map[string]string) error {
	r.calls++
	r.ids = append(r.ids, ids...)
	return nil
}

// Count completes the interface the embedded nil VectorStore leaves open — the
// serving gate (ADR-033 R2) records the index count after a successful write.
func (r *recordingPayloadStore) Count(context.Context, string) (int, error) { return 0, nil }

// TestRepairWritesBothStores: `sync --repair-payload` must correct the SOURCE OF
// TRUTH as well as the index.
//
// It repaired only the index, so the next plain `sync` — which replays the source
// of truth into the index — put the stale payload straight back. Two commands
// that undo each other, and the one an operator reaches for first is the one that
// loses.
//
// This test exists because a commit message claimed the fix had been made when
// only the payload map's TYPE had changed and the receiver was still the raw
// index client. A reviewer found it. Nothing in the suite could have.
func TestRepairWritesBothStores(t *testing.T) {
	sot := &recordingPayloadStore{}
	index := &recordingPayloadStore{}
	hybrid := store.NewHybrid(fakeSourceOfTruth{sot}, index)

	if err := hybrid.SetPayload(context.Background(), "team", []string{"d1", "d2"}, map[string]string{"wing": "wing_acme"}); err != nil {
		t.Fatalf("SetPayload: %v", err)
	}
	if sot.calls == 0 {
		t.Error("the source of truth was not written — the next `sync` replays its stale payload " +
			"over the repair, so the repair does not survive")
	}
	if index.calls == 0 {
		t.Error("the index was not written — the repair does not take effect for search at all")
	}
}

// TestRepairIsHandedTheHybridNotTheIndex reads the call site.
//
// The property above is about store.Hybrid and holds whatever sync passes it. What
// broke was the ARGUMENT: sync handed repairNamespacePayload the bare Qdrant
// client, so the hybrid's two-store write was never reached. Only the call site
// shows that, and it needs a live Qdrant to exercise, so it is read off the source.
func TestRepairIsHandedTheHybridNotTheIndex(t *testing.T) {
	src := readRepoFile(t, "cmd", "server", "sync.go")
	if !strings.Contains(src, "repairNamespacePayload(ctx, gdb, hybrid, ns)") {
		t.Error("sync does not hand repairNamespacePayload the hybrid — if it is given the bare index, " +
			"the repair fixes search and leaves the source of truth stale, and the next sync undoes it")
	}
}

// fakeSourceOfTruth lets a plain VectorStore stand in as the durable half.
type fakeSourceOfTruth struct {
	store.VectorStore
}

func (f fakeSourceOfTruth) AllPoints(context.Context, string) ([]store.Point, error) { return nil, nil }
func (f fakeSourceOfTruth) Namespaces(context.Context) ([]string, error)             { return nil, nil }
