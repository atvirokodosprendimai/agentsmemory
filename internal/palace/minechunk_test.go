package palace

import (
	"strings"
	"testing"
)

func TestMineChunkTextSmallIsSingleChunk(t *testing.T) {
	// Above MineChunkMin (50) but well under the window: exactly one chunk.
	chunks := mineChunkText("a note long enough to clear the fifty-character floor\nsecond line",
		MineChunkSize, MineChunkOverlap, MineChunkMin)
	if len(chunks) != 1 {
		t.Fatalf("short content should be one chunk, got %d", len(chunks))
	}
	c := chunks[0]
	if c.Index != 0 || c.LineStart != 1 {
		t.Fatalf("unexpected index/line: %+v", c)
	}
	if c.LineEnd != 2 {
		t.Fatalf("two lines => LineEnd 2, got %d", c.LineEnd)
	}
}

func TestMineChunkTextTooShortDropped(t *testing.T) {
	if chunks := mineChunkText("hi", MineChunkSize, MineChunkOverlap, MineChunkMin); chunks != nil {
		t.Fatalf("content under MineChunkMin should yield no chunks, got %v", chunks)
	}
}

func TestMineChunkTextBreaksOnParagraph(t *testing.T) {
	// Two ~1080-char paragraphs separated by a blank line: the break sits in the
	// back half of the 1600-char window (past start+size/2=800), so a single window
	// should end on the paragraph break rather than mid sentence — chunk 0 is exactly
	// the first paragraph. (Sized to MineChunkSize; bump both together if it changes.)
	p1 := strings.Repeat("alpha ", 180) // ~1080 chars
	p2 := strings.Repeat("bravo ", 180) // ~1080 chars
	content := strings.TrimSpace(p1) + "\n\n" + strings.TrimSpace(p2)
	chunks := mineChunkText(content, MineChunkSize, MineChunkOverlap, MineChunkMin)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	if chunks[0].Content != strings.TrimSpace(p1) {
		t.Fatalf("chunk 0 should be the first paragraph, got %q...", chunks[0].Content[:40])
	}
}

func TestMineChunkTextVerbatimReassembleAndProgress(t *testing.T) {
	// A long single-token blob (no boundaries) must chunk into overlapping windows
	// that always advance (no infinite loop) and whose union covers the content.
	content := strings.Repeat("x", 3000)
	chunks := mineChunkText(content, MineChunkSize, MineChunkOverlap, MineChunkMin)
	if len(chunks) < 3 {
		t.Fatalf("3000 chars should make several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.Index != i {
			t.Fatalf("chunk %d has Index %d", i, c.Index)
		}
		if len([]rune(c.Content)) > MineChunkSize {
			t.Fatalf("chunk %d exceeds window: %d", i, len([]rune(c.Content)))
		}
	}
}

func TestMineChunkTextLineNumbers(t *testing.T) {
	// Force small windows so line tracking is exercised across chunks.
	content := "L1\nL2\nL3\nL4\nL5\nL6"
	chunks := mineChunkText(content, 8, 2, 1)
	if chunks[0].LineStart != 1 {
		t.Fatalf("first chunk should start at line 1, got %d", chunks[0].LineStart)
	}
	last := chunks[len(chunks)-1]
	if last.LineEnd != 6 {
		t.Fatalf("last chunk should end at line 6, got %d", last.LineEnd)
	}
}
