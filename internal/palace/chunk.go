package palace

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Chunking parameters, ported verbatim from the frozen Python miner so Go and
// Python split identical text into identical drawers (vectors stay comparable):
// 800-char windows, 200-char overlap, and a 50-char floor below which a trailing
// fragment is folded into its predecessor rather than emitted as its own drawer.
const (
	ChunkSize    = 800 // target characters per chunk
	ChunkOverlap = 200 // characters shared between adjacent chunks for context continuity
	ChunkMin     = 50  // a trailing remnant shorter than this is merged back, never emitted alone
)

// Chunk is one slice of a larger text: the verbatim window plus its ordinal
// position. Index is what makes a drawer ID stable across re-adds of the same
// source, so the same input always yields the same chunk boundaries and ids.
type Chunk struct {
	Content string
	Index   int
}

// ChunkText splits text into overlapping windows of size with the given overlap,
// folding any final remnant shorter than min into the previous chunk. Text at or
// under size is returned as a single chunk; empty/whitespace-only text yields no
// chunks. overlap is clamped below size so the window always advances (a defence
// against a caller passing overlap >= size, which would otherwise loop forever).
func ChunkText(text string, size, overlap, min int) []Chunk {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	// Operate on runes, not bytes, so multibyte content (UTF-8) is never sliced
	// mid-character — a corrupt half-rune would poison both storage and search.
	runes := []rune(trimmed)
	if len(runes) <= size {
		return []Chunk{{Content: trimmed, Index: 0}}
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= size {
		overlap = size - 1 // guarantee forward progress
	}
	step := size - overlap

	var chunks []Chunk
	for start := 0; start < len(runes); start += step {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		piece := strings.TrimSpace(string(runes[start:end]))
		// A trailing remnant below the floor is appended to the previous chunk
		// instead of standing alone, so search never sees a near-empty drawer.
		if end == len(runes) && end-start < min && len(chunks) > 0 {
			last := &chunks[len(chunks)-1]
			last.Content = strings.TrimSpace(last.Content + " " + piece)
			break
		}
		if piece != "" {
			chunks = append(chunks, Chunk{Content: piece, Index: len(chunks)})
		}
		if end == len(runes) {
			break
		}
	}
	return chunks
}

// DrawerID is the deterministic identity of a drawer: a SHA-256 of the locating
// tuple (team, wing, room, source, chunkIndex). Identity-by-location is what
// makes add_drawer idempotent — re-adding the same source overwrites the same
// rows rather than accumulating duplicates. The NUL separator cannot occur in
// any of the textual inputs, so distinct tuples can never collide by clever
// concatenation (e.g. wing "a", room "bc" vs wing "ab", room "c").
func DrawerID(teamID, wing, room, sourceFile string, chunkIndex int) string {
	h := sha256.New()
	for _, part := range []string{teamID, wing, room, sourceFile, strconv.Itoa(chunkIndex)} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
