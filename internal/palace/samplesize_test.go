package palace

import (
	"context"
	"fmt"
	"testing"
)

// TestSampleDrawersCountsSourcesNotDrawers pins the function that decides how many
// cases every eval gets, and which had no test anywhere in the tree.
//
// ⚠ `--n` IS A CEILING ON DISTINCT SOURCE FILES, NOT ON DRAWERS. `ListRandom`
// over-fetches `limit*5` rows and then keeps at most one drawer per `source_file`,
// deliberately: a mined session arrives as many chunk drawers sharing one source,
// and two eval cases seeded from the same session are not independent
// observations — the bootstrap treats them as if they were and narrows every
// interval it prints.
//
// The consequence is the one that bites a task author. A wing holding 800 drawers
// across 2 sources yields TWO cases at `--n 80`, not eighty, and nothing reports
// it: `corpus_drawers` in the committed `cells.json` records the post-dedup count,
// so the run looks like a run and its `n` is simply small. ADR-003 T3's floor is
// 40 admitted cases, so the difference decides whether four eval runs mean
// anything — and it is discovered after building the binary and running all four
// unless the precondition is stated in sources.
//
// Two rounds of careful prose about `corpus_drawers` were both wrong before this
// test existed, which is the argument for it.
func TestSampleDrawersCountsSourcesNotDrawers(t *testing.T) {
	ctx := context.Background()

	t.Run("many drawers sharing one source collapse to one case", func(t *testing.T) {
		svc := newTestService(t)
		const team = "team-sample-shared"
		for i := 0; i < 40; i++ {
			mustAdd(t, svc, team, AddInput{
				Wing: "wing_acme", Room: "sessions",
				SourceFile: "one-mined-session.jsonl",
				Content:    fmt.Sprintf("part %d of a single mined session, long enough to be its own chunk", i),
			})
		}
		got, err := svc.SampleDrawers(ctx, team, "wing_acme", 80)
		if err != nil {
			t.Fatalf("sample: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("40 drawers sharing one source sampled %d case(s), want 1\n"+
				"If this ever returns more, the independence the dedup exists for is gone and "+
				"every interval the bootstrap prints is too narrow.", len(got))
		}
	})

	t.Run("hand-filed drawers with no source are each their own cluster", func(t *testing.T) {
		svc := newTestService(t)
		const team = "team-sample-handfiled"
		for i := 0; i < 12; i++ {
			mustAdd(t, svc, team, AddInput{
				Wing: "wing_acme", Room: "decisions",
				Content: fmt.Sprintf("a hand-filed decision number %d, with no source file at all", i),
			})
		}
		got, err := svc.SampleDrawers(ctx, team, "wing_acme", 80)
		if err != nil {
			t.Fatalf("sample: %v", err)
		}
		if len(got) != 12 {
			t.Errorf("12 sourceless drawers sampled %d case(s), want 12\n"+
				"An empty source is not a shared source; collapsing them would silently shrink "+
				"every curated wing's corpus.", len(got))
		}
	})

	t.Run("a corpus can satisfy --n in drawers and miss it by a mile in cases", func(t *testing.T) {
		svc := newTestService(t)
		const team = "team-sample-mixed"
		// 100 drawers — comfortably "≥80" by the count a task author would check —
		// spread across 4 sources. This is the shape a mined wing actually has.
		for src := 0; src < 4; src++ {
			for i := 0; i < 25; i++ {
				mustAdd(t, svc, team, AddInput{
					Wing: "wing_acme", Room: "sessions",
					SourceFile: fmt.Sprintf("mined-session-%d.jsonl", src),
					Content:    fmt.Sprintf("session %d part %d, one chunk of a longer mined document", src, i),
				})
			}
		}
		got, err := svc.SampleDrawers(ctx, team, "wing_acme", 80)
		if err != nil {
			t.Fatalf("sample: %v", err)
		}
		if len(got) != 4 {
			t.Errorf("100 drawers across 4 sources sampled %d case(s), want 4\n"+
				"This is the precondition an ADR task has to state: `--n 80` needs ~80 distinct "+
				"source files, not 80 drawers.", len(got))
		}
	})

	t.Run("the limit is still a ceiling once sources are distinct", func(t *testing.T) {
		svc := newTestService(t)
		const team = "team-sample-ceiling"
		for i := 0; i < 30; i++ {
			mustAdd(t, svc, team, AddInput{
				Wing: "wing_acme", Room: "sessions",
				SourceFile: fmt.Sprintf("distinct-source-%d.jsonl", i),
				Content:    fmt.Sprintf("one part of mined session %d, its own source entirely", i),
			})
		}
		got, err := svc.SampleDrawers(ctx, team, "wing_acme", 10)
		if err != nil {
			t.Fatalf("sample: %v", err)
		}
		if len(got) != 10 {
			t.Errorf("30 distinct sources at --n 10 sampled %d, want 10", len(got))
		}
	})
}
