package palace

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Chunking parameters. The frozen Python miner used 800/100; agentsmemory embeds
// with bge-m3, whose context window is 8192 tokens, so 800 characters (~200 tokens)
// used a tiny fraction of it and fragmented sources more than retrieval needs. We
// deliberately diverge from frozen here — a 1600-char window (~400 tokens) keeps
// each drawer in bge-m3's retrieval sweet spot while halving fragmentation, with a
// 320-char (20%) overlap for context continuity and a 50-char floor below which a
// trailing fragment is folded into its predecessor rather than emitted alone.
// (This changes drawer boundaries, so it is intentionally NOT covered by the
// frozen-parity regression suite, which pins ranking math only.)
const (
	ChunkSize    = 1600 // target characters per chunk (~400 bge-m3 tokens)
	ChunkOverlap = 320  // characters shared between adjacent chunks for context continuity (20%)
	ChunkMin     = 50   // a trailing remnant shorter than this is merged back, never emitted alone
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

// diaryChunks splits a diary entry into stored chunks the frozen Python way:
// fixed-width windows of size runes with NO overlap and NO trimming, so the
// verbatim entry round-trips exactly (SanitizeContent already guaranteed it is
// non-empty and valid). This deliberately differs from ChunkText — add_drawer
// overlaps and trims its windows for better recall — because a diary entry is a
// journal record that must be preserved byte-for-byte, and the frozen tool used a
// plain stride here. An entry at or under size is one chunk holding the original
// text untouched. Runes, not bytes, so a multibyte character is never split
// mid-codepoint (matching Python's codepoint-based slicing).
func diaryChunks(text string, size int) []Chunk {
	runes := []rune(text)
	if len(runes) <= size {
		return []Chunk{{Content: text, Index: 0}}
	}
	var chunks []Chunk
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, Chunk{Content: string(runes[start:end]), Index: len(chunks)})
	}
	return chunks
}

// DrawerID is the deterministic identity of a drawer: a SHA-256 of the locating
// tuple (team, wing, room, source, chunkIndex) AND the chunk's content. Hashing
// content too means re-adding identical text is idempotent (same id, replaced in
// place) while two *different* memories filed to the same wing/room with no
// source_file get distinct ids instead of silently overwriting each other —
// which is what would happen if the id were location-only. The NUL separator
// cannot occur in any textual input, so distinct tuples can never collide by
// concatenation (e.g. wing "a", room "bc" vs wing "ab", room "c").
func DrawerID(teamID, wing, room, sourceFile string, chunkIndex int, content string) string {
	h := sha256.New()
	for _, part := range []string{teamID, wing, room, sourceFile, strconv.Itoa(chunkIndex), content} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// diaryEntryID is the identity of a diary drawer. Unlike DrawerID — which hashes
// only locating fields + content, so re-adding identical text is idempotent — a
// diary id also folds in the agent, topic and a per-write seed (the entry's
// timestamp), because a journal is append-only: writing the same reflection twice
// must yield two distinct entries, not silently overwrite one. The room is pinned
// to DiaryRoom (every diary drawer lives there) and the NUL separator keeps
// distinct field tuples from colliding by concatenation, exactly as in DrawerID.
func diaryEntryID(teamID, wing, agent, topic string, chunkIndex int, content, seed string) string {
	h := sha256.New()
	for _, part := range []string{teamID, wing, DiaryRoom, agent, topic, strconv.Itoa(chunkIndex), content, seed} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
