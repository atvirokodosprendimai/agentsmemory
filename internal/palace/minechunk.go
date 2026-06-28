package palace

import "strings"

// Mining uses a different chunker than add_drawer. add_drawer's ChunkText slides
// a fixed window with overlap; the miner instead prefers to BREAK ON STRUCTURE —
// a paragraph or line boundary in the back half of the window — so a drawer tends
// to hold a whole thought, and it records the 1-indexed line range each chunk
// came from (closets reference it). This is a faithful port of the frozen miner's
// chunk_text. The two chunkers are intentionally separate; unifying them would
// change add_drawer's existing drawer boundaries.

// Mining chunk parameters. Sized for bge-m3 (8192-token window) rather than
// frozen's 800/100 default — see chunk.go for the rationale. The mine and
// add_drawer paths now share the same 1600/320/50 sizing (previously they diverged
// on overlap, 100 vs 200); unifying them keeps both chunkers producing comparably
// sized drawers, even though their split *strategy* still differs (boundary-aware
// here, fixed-window there).
const (
	MineChunkSize    = 1600 // ~400 bge-m3 tokens
	MineChunkOverlap = 320  // 20% overlap for context continuity
	MineChunkMin     = 50
)

// mineChunk is one boundary-aware chunk: its verbatim text, ordinal index among
// emitted chunks, and the 1-indexed line range it spans in the stripped source.
type mineChunk struct {
	Content   string
	Index     int
	LineStart int
	LineEnd   int
}

// mineChunkText splits content into boundary-aware chunks. For each window it
// first tries to end on a paragraph break ("\n\n"), then a line break ("\n"), but
// only if that break falls in the window's back half (past start+size/2) so a
// chunk is never cut tiny; otherwise it takes the full window. A chunk shorter
// than min after trimming is dropped (not emitted) but the scan still advances.
// Adjacent chunks overlap by overlap characters for context continuity. Operates
// on runes so multibyte text is never split mid-codepoint, and on the stripped
// source so line numbers are stable.
func mineChunkText(content string, size, overlap, min int) []mineChunk {
	runes := []rune(strings.TrimSpace(content))
	n := len(runes)
	if n == 0 {
		return nil
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= size {
		overlap = size - 1 // guarantee forward progress
	}

	var chunks []mineChunk
	for start := 0; start < n; {
		end := start + size
		if end > n {
			end = n
		}
		// Prefer a structural boundary in the back half of the window. rfind returns
		// where the separator STARTS, so the chunk excludes the trailing newline(s).
		if end < n {
			if pos := rfindRunes(runes, "\n\n", start, end); pos > start+size/2 {
				end = pos
			} else if pos := rfindRunes(runes, "\n", start, end); pos > start+size/2 {
				end = pos
			}
		}

		chunk := strings.TrimSpace(string(runes[start:end]))
		if len([]rune(chunk)) >= min {
			chunks = append(chunks, mineChunk{
				Content:   chunk,
				Index:     len(chunks),
				LineStart: countNewlines(runes, 0, start) + 1,
				LineEnd:   countNewlines(runes, 0, end) + 1,
			})
		}

		if end < n {
			start = end - overlap // step back by the overlap for the next window
		} else {
			start = end // reached the end; terminate
		}
	}
	return chunks
}

// rfindRunes returns the highest index i in [start, end) at which sub begins and
// fully fits before end (i+len(sub) <= end), or -1 if sub does not occur there —
// the rune-slice analogue of Python's str.rfind(sub, start, end).
func rfindRunes(runes []rune, sub string, start, end int) int {
	s := []rune(sub)
	if len(s) == 0 || end-start < len(s) {
		return -1
	}
	for i := end - len(s); i >= start; i-- {
		if string(runes[i:i+len(s)]) == sub {
			return i
		}
	}
	return -1
}

// countNewlines counts '\n' runes in runes[start:end] — the line-number basis for
// a chunk's 1-indexed LineStart/LineEnd.
func countNewlines(runes []rune, start, end int) int {
	c := 0
	for i := start; i < end; i++ {
		if runes[i] == '\n' {
			c++
		}
	}
	return c
}
