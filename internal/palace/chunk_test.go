package palace

import (
	"strings"
	"testing"
)

func TestChunkText(t *testing.T) {
	t.Run("empty or whitespace yields no chunks", func(t *testing.T) {
		if got := ChunkText("   \n\t ", ChunkSize, ChunkOverlap, ChunkMin); got != nil {
			t.Fatalf("want nil, got %v", got)
		}
	})

	t.Run("text at or under size is a single chunk", func(t *testing.T) {
		text := strings.Repeat("a", ChunkSize)
		got := ChunkText(text, ChunkSize, ChunkOverlap, ChunkMin)
		if len(got) != 1 {
			t.Fatalf("want 1 chunk, got %d", len(got))
		}
		if got[0].Index != 0 || got[0].Content != text {
			t.Fatalf("unexpected chunk: index=%d len=%d", got[0].Index, len(got[0].Content))
		}
	})

	t.Run("oversized text splits with sequential indices and overlap", func(t *testing.T) {
		text := strings.Repeat("a", 2000)
		got := ChunkText(text, ChunkSize, ChunkOverlap, ChunkMin)
		if len(got) < 2 {
			t.Fatalf("want multiple chunks, got %d", len(got))
		}
		for i, c := range got {
			if c.Index != i {
				t.Fatalf("chunk %d has index %d", i, c.Index)
			}
			if len([]rune(c.Content)) > ChunkSize {
				t.Fatalf("chunk %d exceeds ChunkSize: %d", i, len([]rune(c.Content)))
			}
			if strings.TrimSpace(c.Content) == "" {
				t.Fatalf("chunk %d is empty", i)
			}
		}
	})

	t.Run("trailing remnant below the floor folds into the previous chunk", func(t *testing.T) {
		// length 1240: windows start at 0, 600, 1200; the 1200 window is 40 chars
		// (< ChunkMin) and end-of-text, so it must merge into its predecessor
		// rather than be emitted as its own near-empty drawer.
		text := strings.Repeat("a", 1240)
		got := ChunkText(text, ChunkSize, ChunkOverlap, ChunkMin)
		for i, c := range got {
			if n := len([]rune(strings.TrimSpace(c.Content))); n < ChunkMin {
				t.Fatalf("chunk %d below floor (%d < %d) — remnant was not folded", i, n, ChunkMin)
			}
		}
	})

	t.Run("overlap >= size still terminates", func(t *testing.T) {
		// A pathological caller: overlap must be clamped so the window advances.
		got := ChunkText(strings.Repeat("ab", 1000), 100, 100, 10)
		if len(got) == 0 {
			t.Fatal("expected chunks, got none")
		}
	})
}

func TestDrawerID(t *testing.T) {
	base := DrawerID("team1", "wingA", "roomR", "src.md", 0, "hello")

	t.Run("deterministic", func(t *testing.T) {
		if again := DrawerID("team1", "wingA", "roomR", "src.md", 0, "hello"); again != base {
			t.Fatalf("same inputs gave different ids:\n%s\n%s", base, again)
		}
	})

	t.Run("sensitive to every component including content", func(t *testing.T) {
		variants := []string{
			DrawerID("team2", "wingA", "roomR", "src.md", 0, "hello"),
			DrawerID("team1", "wingB", "roomR", "src.md", 0, "hello"),
			DrawerID("team1", "wingA", "roomS", "src.md", 0, "hello"),
			DrawerID("team1", "wingA", "roomR", "other.md", 0, "hello"),
			DrawerID("team1", "wingA", "roomR", "src.md", 1, "hello"),
			DrawerID("team1", "wingA", "roomR", "src.md", 0, "world"), // content differs
		}
		for i, v := range variants {
			if v == base {
				t.Fatalf("variant %d collided with base id", i)
			}
		}
	})

	t.Run("distinct content with no source does not collide", func(t *testing.T) {
		// The data-loss guard: two memories filed to the same wing/room with no
		// source_file must get different ids so the second cannot overwrite the
		// first.
		a := DrawerID("t", "w", "r", "", 0, "first memory")
		b := DrawerID("t", "w", "r", "", 0, "second memory")
		if a == b {
			t.Fatal("distinct content collided — add_drawer would silently overwrite")
		}
	})

	t.Run("separator prevents concatenation collisions", func(t *testing.T) {
		// ("a","bc") and ("ab","c") must not hash to the same id.
		if DrawerID("t", "a", "bc", "", 0, "x") == DrawerID("t", "ab", "c", "", 0, "x") {
			t.Fatal("concatenation collision: NUL separator not effective")
		}
	})
}
