package palace

import (
	"context"
	"strings"
	"testing"
)

// countingEmbedder records what the service asked it to embed, so a test can
// assert not only that an oversized update was refused but that the refusal
// happened BEFORE the embedder was called. A version that embeds first and
// checks afterwards passes a refusal test while still paying the cost — and,
// against a real server, still receiving the truncated vector the bound exists
// to prevent.
type countingEmbedder struct {
	fakeEmbedder
	oneCalls int
	longest  int
}

func (c *countingEmbedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	c.oneCalls++
	if n := len([]rune(input)); n > c.longest {
		c.longest = n
	}
	return c.fakeEmbedder.EmbedOne(ctx, input)
}

// TestCorrectingWithLongContentChunksInsteadOfTruncating is the gate for the
// invariant teiembed.go states in prose: nothing reaches the embedder as one
// piece above what the model can represent.
//
// It was prose, and a live path violated it. ChunkText bounds Add; Update used to
// re-embed a whole memory with EmbedOne and never chunk, so a memory created small
// and grown in place was the one unbounded input — and because the client asks for
// truncation rather than an error, an oversized update returned a vector for the
// prefix and reported success. The memory still read back whole from am_get_drawer
// while being unfindable past the cut.
//
// It was fixed by REFUSING an oversized update, and this test asserted that
// refusal. ADR-038 T4 removed the need: a content change is now a supersede, which
// files the new text through Add, which chunks. So the same invariant holds by a
// better route — the caller is no longer told to delete and re-file by hand — and
// what is asserted here is the invariant, not the refusal that used to enforce it.
func TestCorrectingWithLongContentChunksInsteadOfTruncating(t *testing.T) {
	emb := &countingEmbedder{}
	svc := newTestServiceWith(t, emb)
	ctx := context.Background()

	const team = "team-1"
	added, err := svc.Add(ctx, team, AddInput{
		Wing: "wing_a", Room: "decisions", SourceFile: "seed", Content: "the original short memory",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := added.Drawers[0].ID

	oversized := strings.Repeat("x ", MaxEmbedRunes)
	res, err := svc.Update(ctx, team, id, DrawerPatch{
		Content: &oversized, Reason: "the short version left out the whole procedure",
	})
	if err != nil {
		t.Fatalf("a correction longer than the embedder's window must be chunked, not refused: %v", err)
	}
	if res.Supersedes != id {
		t.Errorf("supersedes = %q, want %q", res.Supersedes, id)
	}

	chunks, err := svc.GetMemory(ctx, team, res.Drawer.ID)
	if err != nil {
		t.Fatalf("read the correcting memory: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("the correcting memory is %d chunk(s); content of %d runes must be split, or the "+
			"tail is stored and never findable", len(chunks), len([]rune(oversized)))
	}
	for _, c := range chunks {
		if n := len([]rune(c.Content)); n > MaxEmbedRunes {
			t.Errorf("a chunk of %d runes reached the store; the embedder takes at most %d in one "+
				"piece and truncates rather than failing, so anything past that is unfindable",
				n, MaxEmbedRunes)
		}
	}
	if emb.longest > MaxEmbedRunes {
		t.Errorf("the longest string handed to the embedder was %d, above the %d limit — this is the "+
			"invariant, and it must hold whichever path files the text", emb.longest, MaxEmbedRunes)
	}
}

// TestMaxEmbedRunesStaysConservativeAcrossBackends pins the REASONING rather than
// the number, and its name is deliberately not "…IsSmallerThanTheSmallestBackend":
// nothing in this repository measures any model's window, so no test here can
// honestly claim that.
//
// What it does assert is that the bound stays far below the model actually in
// front of us. Both shipped backends run bge-m3 — TEI's is fixed by --model-id,
// and config.Default() sets OllamaEmbedModel to "bge-m3" — so 8192 tokens is
// today's real ceiling and 4000 characters is nowhere near it. The margin exists
// for the model an operator SWAPS IN, not for the one that ships.
//
// So: raising MaxEmbedRunes toward a specific model's window should have to argue
// with a failing test first, and the argument owed is about what the smallest
// model anyone might configure can hold — not about bge-m3.
func TestMaxEmbedRunesStaysConservativeAcrossBackends(t *testing.T) {
	// A deliberately conservative figure for what a modest embedding model holds,
	// in characters because the palace cannot ask a tokenizer and the
	// chars-per-token ratio is script-dependent. It is a policy line, not a
	// measurement, and it is written here so that moving it is a visible decision.
	const conservativeWindowRunes = 4096

	if MaxEmbedRunes > conservativeWindowRunes {
		t.Fatalf("MaxEmbedRunes is %d, above the %d this repo is willing to assume of a backend "+
			"nobody has measured. Raising it needs evidence about the smallest model an operator "+
			"can configure, not about bge-m3 — which both shipped backends happen to run",
			MaxEmbedRunes, conservativeWindowRunes)
	}
	if MaxEmbedRunes <= ChunkSize {
		t.Fatalf("MaxEmbedRunes (%d) is at or below ChunkSize (%d), which would make every "+
			"multi-chunk-sized live document unupdatable and defeat the point of the bound",
			MaxEmbedRunes, ChunkSize)
	}
}
