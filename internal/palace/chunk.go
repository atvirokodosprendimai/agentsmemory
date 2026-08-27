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

	// MaxEmbedRunes bounds a single string handed to the embedder as ONE vector.
	// Set to 4000 by M, 2026-08-25.
	//
	// It exists because ChunkText bounds the ADD path and nothing bounds the
	// UPDATE path: Update re-embeds its whole content with EmbedOne, never
	// re-chunking (deliberately — see Service.Update), so a memory created small
	// and grown in place is the one input that can exceed the model's window. The
	// TEI client asks for truncation so an over-long input cannot fail a whole
	// batch, which means the server answers 200 with a vector for the PREFIX: the
	// tail stays stored, still comes back from am_get_drawer, and is simply
	// unfindable. Silent, and only on this path.
	//
	// ⚠IT IS CONSERVATIVE HEADROOM, NOT A MEASURED CEILING, and saying so matters
	// because the obvious reading is wrong. Both shipped backends run bge-m3 —
	// TEI's is fixed by --model-id and config.Default() sets OllamaEmbedModel to
	// "bge-m3" too — so the model in front of us holds 8192 tokens either way, and
	// 4000 characters is far below that. The bound is not sized to today's model.
	//
	// It is sized so that SWAPPING the model stays survivable. An operator may
	// point EMBED_BACKEND or OLLAMA_EMBED_MODEL at something much smaller, and
	// nothing in this repository measures any model's window or would notice. A
	// limit computed from bge-m3 alone would be a limit only bge-m3 satisfies.
	//
	// ⚠It does NOT make every model safe, and the name of the test guarding it
	// should not be read as claiming so: a 512-token model such as
	// mxbai-embed-large tops out around 2k characters and 4000 would still cut it.
	// This is a floor that removes the unbounded case, chosen by M, not a proof.
	//
	// Characters rather than tokens because the palace cannot ask the tokenizer,
	// and the ratio is script-dependent (~4 chars/token for English, far worse for
	// CJK), so any character bound is an approximation and must therefore err low.
	// Live documents run ~2k runes today, leaving room to grow without meeting it.
	MaxEmbedRunes = 4000
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

// DrawerID computes a CONTENT KEY: a SHA-256 over the locating tuple (team, wing,
// room, source, chunkIndex) and the chunk's content. It is the dedup key, and it
// is not a name — a drawer's id is opaque, minted once and never recomputed
// (ADR-038).
//
// ⚠ THE FUNCTION KEPT ITS OLD NAME AND LOST ITS OLD JOB, which is the one thing
// worth knowing before calling it. Its comment used to open "the deterministic
// identity of a drawer", and that sentence is why four of the five mint paths
// called it directly instead of going through contentKeyOf: the name and the prose
// both said this produced a drawer's id, so nobody looked for a wrapper. The
// wrapper is where the diary exemption lives, and skipping it deduped journal
// entries. TestNoPathRederivesADrawerID now refuses a second caller.
//
// Hashing content as well as location means re-filing identical text matches the
// row already holding it, while two *different* memories filed to the same
// wing/room with no source_file get distinct keys instead of colliding — which is
// what a location-only key would do. The NUL separator cannot occur in any textual
// input, so distinct tuples can never collide by concatenation (e.g. wing "a",
// room "bc" vs wing "ab", room "c").
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
