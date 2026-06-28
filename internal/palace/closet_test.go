package palace

import (
	"strings"
	"testing"
)

func TestBuildClosetLinesTopicsQuotesAndRefs(t *testing.T) {
	content := "# Cache Design\n\nWe built the cache layer today. " +
		`"This is a long enough quote about caching to be captured."`
	ids := []string{"d1", "d2", "d3", "d4", "d5"}
	lines := buildClosetLines("notes.md", ids, content, "proj", "backend", "2024-11-08:L1-L3")

	if len(lines) < 2 {
		t.Fatalf("expected header/action topics and a quote line, got %v", lines)
	}
	for _, l := range lines {
		// Drawer refs are capped at the first three ids.
		if !strings.HasSuffix(l, "→d1,d2,d3") {
			t.Fatalf("line should reference the first 3 drawers, got %q", l)
		}
		// Tier-6a date locator is present (4-segment form).
		if !strings.Contains(l, "2024-11-08:L1-L3") {
			t.Fatalf("line should carry the date:line segment, got %q", l)
		}
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "cache design") {
		t.Fatalf("markdown header should become a topic, got %v", lines)
	}
}

func TestBuildClosetLinesFallbackAndThreeSegment(t *testing.T) {
	// No headers/actions/quotes and no date segment -> a single 3-segment fallback
	// line keyed on the source location.
	lines := buildClosetLines("path/to/raw.txt", []string{"d1"}, "just some plain prose without structure", "proj", "notes", "")
	if len(lines) != 1 {
		t.Fatalf("expected one fallback line, got %v", lines)
	}
	l := lines[0]
	if strings.Count(l, "|") != 2 { // topic|entities|→refs  == two pipes
		t.Fatalf("no date segment => 3-segment line (2 pipes), got %q", l)
	}
	if !strings.Contains(l, "proj/notes/raw") {
		t.Fatalf("fallback should key on wing/room/source-stem, got %q", l)
	}
}

func TestPackClosets(t *testing.T) {
	lines := []string{strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)}

	// Generous limit packs all three into one document.
	if docs := packClosets(lines, 1500); len(docs) != 1 {
		t.Fatalf("all lines should pack into one closet, got %d", len(docs))
	}
	// Tight limit forces one line per document, never splitting a line.
	docs := packClosets(lines, 50)
	if len(docs) != 3 {
		t.Fatalf("tight limit should give one closet per line, got %d", len(docs))
	}
	for _, d := range docs {
		if strings.Contains(d, "\n") {
			t.Fatalf("a single-line closet must not be split/merged: %q", d)
		}
	}
}
