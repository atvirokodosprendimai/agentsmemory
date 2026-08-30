package main

import (
	"strings"
	"testing"
)

// TestTheShippedProtocolDoesNotSayAFragmentIsUnmarked keeps one load-bearing
// sentence in the shipped protocol from going false again.
//
// bootstrap.md is written into every agent's config directory by the installer and
// auto-loaded every session, so a claim in it is not documentation — it is what
// every session believes before it does anything. From 2026-08-25 it said
// "Nothing marks the fragment as partial" about am_get_drawer. ADR-044 T4 made
// that untrue on 2026-08-29 and the sentence shipped unchanged, teaching every new
// session to distrust a field that works.
//
// ⚠ IT READS THE EMBEDDED ASSET, not the file on disk, because the embedded copy
// is what the installer actually writes. They are the same bytes today, and the
// distinction is the point: a gate that reads something the shipped artifact does
// not come from is a gate over the wrong thing.
//
// Narrow on purpose. This does not verify the protocol against the code in general
// — that is not mechanisable — it pins the ONE claim that was measured false, in
// the manner of TestNoCommentClaimsADrawerIdIsDerivedFromItsContent.
func TestTheShippedProtocolDoesNotSayAFragmentIsUnmarked(t *testing.T) {
	raw, err := assets.ReadFile("bootstrap.md")
	if err != nil {
		t.Fatalf("read the embedded bootstrap protocol: %v", err)
	}
	text := string(raw)
	// ⚠ THE INSTALLED COPY IS NOT THE ONLY ONE, and the first draft of this gate
	// covered one of at least four. internal/web/bootstrap-memory.md carries the
	// same claim and is //go:embed-ed and SERVED at /bootstrap-memory — the copy
	// handed out by the very server whose behaviour changed. Two more live in the
	// palace as centralised data (the start-here skill, the am_skillset preamble),
	// where no test in this tree can reach them: those go false at DEPLOY rather
	// than at merge, which is why the Follow-up names them separately.
	//
	// Found in review. A Follow-up phrased against one file is how the other three
	// stay wrong.
	// A universe of zero is a gate that cannot fail: if the passage this is about
	// has gone, the check is passing over nothing.
	if !strings.Contains(text, "am_get_drawer") {
		t.Fatal("the shipped protocol does not mention am_get_drawer at all — this check has " +
			"stopped checking anything")
	}
	if !strings.Contains(text, "content_truncated") {
		t.Fatal("the shipped protocol never names content_truncated, so it cannot be telling a " +
			"session how to notice a partial fetch — the passage this pins is gone or rewritten")
	}
	for _, claim := range []string{
		"Nothing marks the fragment as partial",
		"nothing marks the fragment as partial",
	} {
		if strings.Contains(text, claim) {
			t.Errorf("the shipped protocol says %q. A fetched chunk has carried content_truncated "+
				"with the memory's content_length since ADR-044 T4, and the installer writes this "+
				"file into every agent's config — so this sentence teaches every new session to "+
				"distrust a field that works", claim)
		}
	}
}
