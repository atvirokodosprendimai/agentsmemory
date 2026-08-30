package palace

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// reassembleMemoryNaive is the PREVIOUS implementation, kept in the test as the
// reference the bounded one must agree with. It re-reads the whole accumulated
// prefix on every chunk, which is the quadratic behaviour S1 measured; here that
// cost is irrelevant and its output is the specification.
func reassembleMemoryNaive(chunks []Drawer) string {
	if len(chunks) == 0 {
		return ""
	}
	if len(chunks) == 1 {
		return chunks[0].Content
	}
	var b strings.Builder
	b.WriteString(chunks[0].Content)
	for i := 1; i < len(chunks); i++ {
		next := chunks[i].Content
		if storedWithoutOverlap(chunks[i]) {
			b.WriteString(next)
			continue
		}
		current := b.String()
		lr, rr := []rune(current), []rune(next)
		maxRunes := min(ChunkOverlap, len(lr), len(rr))
		overlap := 0
		for n := maxRunes; n > 0; n-- {
			if string(lr[len(lr)-n:]) == string(rr[:n]) {
				overlap = n
				break
			}
		}
		if overlap == 0 && current != "" && next != "" {
			if isWordRune(lr[len(lr)-1]) && isWordRune(rr[0]) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(string(rr[overlap:]))
	}
	return b.String()
}

// TestReassembleMemoryMatchesTheNaiveImplementation is the correctness half of
// S1. The bounded rewrite is only worth having if it is byte-identical, and
// "looks equivalent" is not a claim this repo accepts — so it is checked against
// the previous implementation over inputs chosen to hit the branches that differ:
// real overlap, no overlap with a word boundary, no overlap without one,
// unicode, and chunks stored without overlap at all.
func TestReassembleMemoryMatchesTheNaiveImplementation(t *testing.T) {
	overlapping := func(body string, size, overlap int) []Drawer {
		runes := []rune(body)
		var out []Drawer
		for start := 0; start < len(runes); start += size - overlap {
			end := min(start+size, len(runes))
			out = append(out, Drawer{Content: string(runes[start:end]), ChunkIndex: len(out)})
			if end == len(runes) {
				break
			}
		}
		return out
	}

	varied := func(n int) string {
		var b strings.Builder
		for i := 0; b.Len() < n; i++ {
			fmt.Fprintf(&b, "line %d: distinct prose so overlap removal finds the real seam. ", i)
		}
		return b.String()
	}

	cases := map[string][]Drawer{
		"real overlap":          overlapping(varied(9000), ChunkSize, ChunkOverlap),
		"no overlap, word seam": {{Content: "ending in a wor"}, {Content: "d then continuing"}},
		"no overlap, punctuated": {
			{Content: "a sentence that ends."},
			{Content: "Another that begins."},
		},
		"unicode seam": {
			{Content: "kalbos apie atmintį — pabaiga"},
			{Content: "— pabaiga ir tęsinys su ąžuolais"},
		},
		"single chunk": {{Content: "nothing to join"}},
		"empty":        {},
		"repetitive":   overlapping(strings.Repeat("same phrase over and over ", 300), ChunkSize, ChunkOverlap),
		"diary-style (stored without overlap)": {
			{Content: "first stored window", Agent: "blinkinglight"},
			{Content: "second stored window", Agent: "blinkinglight", ChunkIndex: 1},
		},
	}

	for name, chunks := range cases {
		t.Run(name, func(t *testing.T) {
			want := reassembleMemoryNaive(chunks)
			got := reassembleMemory(chunks)
			if got != want {
				t.Errorf("bounded reassembly differs from the reference:\n got %q\nwant %q", got, want)
			}
		})
	}

	// Randomised, because the hand-written cases are the ones I thought of. Fixed
	// seed so a failure is reproducible from the output alone.
	t.Run("randomised seams", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1))
		alphabet := []rune("ab cd. ąč—\nxyz")
		for i := range 200 {
			n := 1 + rng.Intn(6)
			chunks := make([]Drawer, n)
			for c := range chunks {
				var b strings.Builder
				for range 1 + rng.Intn(80) {
					b.WriteRune(alphabet[rng.Intn(len(alphabet))])
				}
				chunks[c] = Drawer{Content: b.String(), ChunkIndex: c}
			}
			if got, want := reassembleMemory(chunks), reassembleMemoryNaive(chunks); got != want {
				t.Fatalf("case %d differs:\n got %q\nwant %q\nchunks %+v", i, got, want, chunks)
			}
		}
	})
}

// BenchmarkReassembleMemory is the cost half of S1. The bounded implementation
// should be roughly linear in the memory's length; the naive one is quadratic,
// and the gap is what the rewrite is for. Run with -benchmem:
//
//	go test ./internal/palace/ -run '^$' -bench ReassembleMemory -benchmem
func BenchmarkReassembleMemory(b *testing.B) {
	for _, runes := range []int{8_000, 32_000, 128_000} {
		var body strings.Builder
		for i := 0; body.Len() < runes; i++ {
			fmt.Fprintf(&body, "line %d: distinct prose so overlap removal finds the real seam. ", i)
		}
		all := []rune(body.String())
		var chunks []Drawer
		for start := 0; start < len(all); start += ChunkSize - ChunkOverlap {
			end := min(start+ChunkSize, len(all))
			chunks = append(chunks, Drawer{Content: string(all[start:end]), ChunkIndex: len(chunks)})
			if end == len(all) {
				break
			}
		}
		b.Run(fmt.Sprintf("bounded-%dk", runes/1000), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = reassembleMemory(chunks)
			}
		})
		b.Run(fmt.Sprintf("naive-%dk", runes/1000), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = reassembleMemoryNaive(chunks)
			}
		})
	}
}
