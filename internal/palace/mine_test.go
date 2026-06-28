package palace

import (
	"context"
	"strings"
	"testing"
)

// TestMineFilesDrawersAndClosets is the end-to-end happy path: a structured
// document mines into multiple drawers plus at least one closet, stamps the
// detected content date, and records entities + author on the drawers.
func TestMineFilesDrawersAndClosets(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	content := "---\ndate: 2024-11-08\n---\n# Cache Design\n\n" +
		strings.Repeat("Redis powers the cache. Redis is fast. ", 30) +
		"\n\nWe built the cache layer and deployed it.\n\n" +
		strings.Repeat("Postgres stores the source of truth. Postgres is durable. ", 30)

	res, err := svc.Mine(ctx, team, MineInput{Content: content, Wing: "proj", Source: "notes.md", Agent: "Claude"})
	if err != nil {
		t.Fatalf("mine: %v", err)
	}
	if res.Drawers < 2 {
		t.Fatalf("structured content should produce multiple drawers, got %d", res.Drawers)
	}
	if res.Closets < 1 {
		t.Fatalf("mining should build at least one closet, got %d", res.Closets)
	}
	if res.Room != DefaultMineRoom {
		t.Fatalf("default room should be %q, got %q", DefaultMineRoom, res.Room)
	}
	if res.ContentDate != "2024-11-08" {
		t.Fatalf("content date from frontmatter should be 2024-11-08, got %q", res.ContentDate)
	}

	list, err := svc.List(ctx, team, "proj", DefaultMineRoom, 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != res.Drawers {
		t.Fatalf("listed %d drawers, mine reported %d", len(list), res.Drawers)
	}
	var sawEntity, sawAgent bool
	for _, d := range list {
		if d.Agent != "claude" {
			t.Fatalf("drawer agent should be lowercased author, got %q", d.Agent)
		}
		sawAgent = true
		if len(d.Entities) > 0 {
			sawEntity = true
		}
		if d.ContentDate != "2024-11-08" {
			t.Fatalf("drawer should carry the content date, got %q", d.ContentDate)
		}
	}
	if !sawAgent || !sawEntity {
		t.Fatalf("expected drawers with author and at least one with entities (agent=%v entity=%v)", sawAgent, sawEntity)
	}
}

// TestMineIdempotentReplacesSource confirms re-mining the same source replaces it:
// a shorter re-mine leaves only the new drawers, no stale chunks.
func TestMineIdempotentReplacesSource(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	long := strings.Repeat("alpha beta gamma delta. ", 200) // many chunks
	first, err := svc.Mine(ctx, team, MineInput{Content: long, Wing: "w", Room: "r", Source: "doc"})
	if err != nil {
		t.Fatalf("mine first: %v", err)
	}
	if first.Drawers < 2 {
		t.Fatalf("expected several drawers first pass, got %d", first.Drawers)
	}

	short := "a concise replacement note that still clears the fifty character floor easily"
	second, err := svc.Mine(ctx, team, MineInput{Content: short, Wing: "w", Room: "r", Source: "doc"})
	if err != nil {
		t.Fatalf("mine second: %v", err)
	}

	list, err := svc.List(ctx, team, "w", "r", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != second.Drawers {
		t.Fatalf("re-mine should replace; listed %d but second pass reported %d", len(list), second.Drawers)
	}
	if len(list) >= first.Drawers {
		t.Fatalf("shorter re-mine should leave fewer drawers than the first pass (%d), got %d", first.Drawers, len(list))
	}
}

// TestMineValidates rejects a missing wing and a missing source.
func TestMineValidates(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	if _, err := svc.Mine(ctx, "team-1", MineInput{Content: "x of sufficient length to clear the floor here", Wing: "", Source: "s"}); err == nil {
		t.Fatal("expected error for empty wing")
	}
	if _, err := svc.Mine(ctx, "team-1", MineInput{Content: "x of sufficient length to clear the floor here", Wing: "w", Source: ""}); err == nil {
		t.Fatal("expected error for empty source")
	}
}
