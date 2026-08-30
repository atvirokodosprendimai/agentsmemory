package web

import (
	"strings"
	"testing"
)

// TestTheServedProtocolDoesNotSayAFragmentLooksComplete is the sibling of
// clients/claude-code's gate over the INSTALLED protocol, and it exists because
// that gate covered one copy of at least four.
//
// bootstrap-memory.md is //go:embed-ed here and served at /bootstrap-memory — the
// copy handed out by the very server whose behaviour changed. It said a fetched
// chunk "looks complete", twice. ADR-044 T4 made that false, and a Follow-up
// phrased against clients/claude-code/bootstrap.md alone would have left this one
// wrong while reporting the correction done.
//
// ⚠ TWO MORE COPIES LIVE IN THE PALACE AS DATA — the centralised start-here skill
// and the am_skillset preamble — where no test in this tree can reach them. They
// go false at DEPLOY rather than at merge. This gate cannot cover them and does
// not pretend to; they are named in the record's Follow-up instead.
func TestTheServedProtocolDoesNotSayAFragmentLooksComplete(t *testing.T) {
	// A universe of zero is a gate that cannot fail.
	if !strings.Contains(bootstrapMemory, "am_get_drawer") {
		t.Fatal("the served protocol never mentions am_get_drawer — this check has stopped " +
			"checking anything")
	}
	if !strings.Contains(bootstrapMemory, "content_truncated") {
		t.Fatal("the served protocol never names content_truncated, so it is not telling a " +
			"reader how to notice a partial fetch")
	}
	if n := strings.Count(bootstrapMemory, "it looks complete"); n > 0 {
		t.Errorf("the served protocol says a fetched chunk \"looks complete\" in %d place(s). "+
			"Since ADR-044 T4 it carries content_truncated with the memory's content_length, and "+
			"this page is served by the server whose behaviour changed — so it teaches every "+
			"reader to distrust a field that works", n)
	}
}
