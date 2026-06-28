package palace

import (
	"context"
	"testing"
)

func TestMergeWing(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mustAdd(t, svc, team, AddInput{Wing: "old-a", Room: "r", Content: "a memory about cats that clears the floor easily"})
	mustAdd(t, svc, team, AddInput{Wing: "old-b", Room: "s", Content: "another memory about dogs that clears the floor too"})

	res, err := svc.MergeWing(ctx, team, []string{"old-a", "old-b"}, "merged")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Drawers != 2 {
		t.Fatalf("expected 2 drawers relabeled, got %d", res.Drawers)
	}

	moved, _ := svc.List(ctx, team, "merged", "", 50, 0)
	if len(moved) != 2 {
		t.Fatalf("merged wing should hold both drawers, got %d", len(moved))
	}
	for _, w := range []string{"old-a", "old-b"} {
		left, _ := svc.List(ctx, team, w, "", 50, 0)
		if len(left) != 0 {
			t.Fatalf("source wing %q should be empty after merge, got %d", w, len(left))
		}
	}

	// Idempotent / self-merge: merging the target into itself changes nothing.
	again, err := svc.MergeWing(ctx, team, []string{"merged"}, "merged")
	if err != nil || again.Drawers != 0 {
		t.Fatalf("self-merge should be a no-op, got %+v err=%v", again, err)
	}
}

func TestMemoriesFiledAway(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	empty, err := svc.MemoriesFiledAway(ctx, team)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if empty.Count != 0 || empty.Message != "No memories filed yet" {
		t.Fatalf("empty palace summary wrong: %+v", empty)
	}

	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a filed memory long enough to be a drawer"})
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "s", Content: "a second filed memory long enough as well"})

	res, err := svc.MemoriesFiledAway(ctx, team)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if res.Count != 2 || res.Wings != 1 || res.Rooms != 2 {
		t.Fatalf("summary counts wrong: %+v", res)
	}
	if res.LastFiledAt == "" {
		t.Fatalf("expected a last_filed_at, got empty")
	}
}
