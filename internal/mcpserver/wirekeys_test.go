package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// undescribedOnPurpose lists omitempty response fields that need no sentence,
// with the reason. An entry is a JUDGEMENT, and the reason is what makes it
// reviewable — TestUndescribedOnPurposeIsJustified refuses one without it.
var undescribedOnPurpose = map[string]string{
	"parent_id": "structural, and already explained where it matters: am_get_drawer's " +
		"description says a long memory is several chunks sharing a parent and that any " +
		"chunk's id works. Naming the field again buys nothing.",
	"checked_at": "a field INSIDE code_anchors, which is described. Documenting an object's " +
		"sub-fields one by one would put the whole schema in the prose.",
	"updated_at": "INERT, and never even reaches the wire. tunnels.updated_at is " +
		"TEXT NOT NULL DEFAULT '' (00009_graph.sql:48), nothing in this codebase assigns it, " +
		"and GORM's automatic timestamping does not apply to a string field with an explicit " +
		"column tag. So it is empty for every tunnel this store has written, and omitempty " +
		"then drops it. Describing a field no response carries would be a promise with " +
		"nothing behind it.",
}

// omitemptyTag finds a response field that is absent from an ordinary answer.
var omitemptyTag = regexp.MustCompile(`json:"([a-z][a-z0-9_]*),omitempty"`)

// descriptionText pulls the prose an agent actually reads at the call.
var descriptionText = regexp.MustCompile(`(?s)mcp\.(?:With)?Description\((.*?)\)\)`)

// TestEveryOmitemptyWireKeyInThisPackageIsDescribed: a field a caller cannot discover
// by looking at one response must be named in the prose beside the call.
//
// ⚠ THE CLASS IS THE POINT, and it is narrower than "every field". A field that is
// always present is discoverable from any single answer — call the tool once and it
// is there. An `omitempty` field is absent BY CONSTRUCTION until the case that
// produces it, so a caller who has never hit that case has no way to learn it
// exists, and every gate this repository already has is blind to that: the field is
// emitted, so the reachability check passes; the value is right, so the behavioural
// test passes; and the one surface an agent reads says nothing.
//
// Measured on me, 2026-08-27: kg_query's `resolution` was wired end to end and
// named in no description. I read the description, learned nothing, went to the
// source, misread it, and shipped the wrong claim into the protocol that auto-loads
// into every install. That is the defect this gate is for — not unreachable,
// undiscoverable.
//
// ⚠ THE NAME SAYS "IN THIS PACKAGE" BECAUSE THE UNIVERSE IS THE PACKAGE, NOT THE
// WIRE, and the first name overclaimed. `packageSources` globs *.go in
// internal/mcpserver: 26 omitempty keys, against 79 repo-wide. Response types from
// internal/palace reach the wire through here by EMBEDDING (graphStatsView embeds
// palace.GraphStats; mine.go embeds palace.MineResult) and by field type
// (searchHitView.Corrections []palace.Correction) — and this gate is blind to all of
// it. Proven in review by adding an omitempty field to palace.Correction: it reached
// the wire and both gates stayed green.
//
// Two real fields sit in that blind spot today: palace/kg.go's `replacement_id` and
// `elsewhere_wing` on Correction, both absent by construction until a record has
// been corrected. `replacement_id` is the field telling a reader the memory in front
// of them has been contradicted — the same stakes as stale_index, which this gate
// did catch.
//
// A THIRD population is invisible to any struct-tag scan: conditional map[string]any
// keys, set inside `if` blocks. out["stale_hits"], out["warning"],
// out["supersedes"], out["reason"], out["ended_at"] and others are emitted where no
// tag exists to find. Out of scope here and named so the next reader knows it.
//
// Widening the universe — a reflect walk from the registered view types, following
// embedding and field types — is the better gate and its own change. What must not
// ship is a gate whose name claims a third of the surface it covers.
//
// Baseline when written: 26 omitempty response fields in this package, 9 undescribed,
// fixed rather than allowlisted except where a written reason says prose buys nothing.
func TestEveryOmitemptyWireKeyInThisPackageIsDescribed(t *testing.T) {
	files := packageSources(t)

	var prose strings.Builder
	keys := map[string]string{} // key -> file that declares it
	for path, src := range files {
		for _, m := range omitemptyTag.FindAllStringSubmatch(src, -1) {
			keys[m[1]] = filepath.Base(path)
		}
		for _, m := range descriptionText.FindAllStringSubmatch(src, -1) {
			prose.WriteString(m[1])
			prose.WriteString("\n")
		}
	}

	// A universe of zero is a gate that cannot fail. This package is full of
	// omitempty tags; zero means the pattern broke, not that the schema went quiet.
	if len(keys) == 0 {
		t.Fatal("no omitempty response field found in this package — it carries dozens, so " +
			"the pattern stopped matching and this check is passing vacuously")
	}
	if prose.Len() == 0 {
		t.Fatal("no tool or parameter description found — the matcher broke, and with no " +
			"prose to search every key would report as undescribed")
	}

	// ⚠ WORD BOUNDARY, NOT SUBSTRING, and two keys were passing on a coincidence
	// before it. `stale` had three substring matches and ZERO standalone ones: it
	// was credited to the word "staleness", in a sentence about erasure that has
	// nothing to do with the field — and deleting the entire stale_index sentence
	// left it green. `drawer_id` matched only inside `source_drawer_id`. A check
	// that greps for a token is satisfied by any unrelated appearance of it, which
	// is the same defect this gate exists to catch, one level down.
	//
	// A boundary is necessary and NOT sufficient: `note` and `wing` still pass on
	// the English word and on parameter descriptions respectively. Crediting a field
	// only from the description of the tool that EMITS it is the version that cannot
	// be satisfied by accident; it is more work than this change and is recorded as
	// a follow-up rather than pretended away.
	described := func(key string) bool {
		return regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\b`).MatchString(prose.String())
	}
	var undescribed []string
	for key, file := range keys {
		if described(key) {
			continue
		}
		if _, ok := undescribedOnPurpose[key]; ok {
			continue
		}
		undescribed = append(undescribed, key+" ("+file+")")
	}
	sort.Strings(undescribed)
	for _, u := range undescribed {
		t.Errorf("%s is absent from an ordinary response and named in no description.\n"+
			"  A caller who has not hit the case that produces it cannot learn it exists. "+
			"Name it where the call is made, or add it to undescribedOnPurpose with the reason.", u)
	}
}

// TestUndescribedOnPurposeIsJustified refuses an entry with no reason, and one
// that no longer names a real field.
//
// The second half is the one that rots. An entry outliving the field it excuses is
// invisible: nothing fails, the list just quietly grows a name for something that
// does not exist, and the next reader trusts it.
//
// It scans NON-TEST sources only. The allowlist literal lives in this file, so a
// scan including tests would find every entry matching itself and the stale-entry
// half could never fire — the exact bug cmd/server's equivalent recorded in its own
// comment after making it.
func TestUndescribedOnPurposeIsJustified(t *testing.T) {
	var all strings.Builder
	for _, src := range packageSources(t) {
		all.WriteString(src)
	}
	// The prose, so a dead entry can be detected.
	var prose strings.Builder
	for _, src := range packageSources(t) {
		for _, m := range descriptionText.FindAllStringSubmatch(src, -1) {
			prose.WriteString(m[1])
			prose.WriteString("\n")
		}
	}

	for key, why := range undescribedOnPurpose {
		if strings.TrimSpace(why) == "" {
			t.Errorf("undescribedOnPurpose[%q] has no reason — the reason IS the review", key)
		}
		// ⚠ AN EXEMPTION THAT IS NO LONGER NEEDED IS INDISTINGUISHABLE FROM ONE THAT
		// IS. `line` was dead weight when this was added: it is named in the prose
		// and the entry excused nothing. A list that only grows records judgements
		// nobody is making any more.
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\b`).MatchString(prose.String()) {
			t.Errorf("undescribedOnPurpose[%q] is dead: the field IS named in a description, so "+
				"the exemption excuses nothing. Delete it — an unnecessary entry reads exactly "+
				"like a necessary one.", key)
		}
		if !strings.Contains(all.String(), `json:"`+key+`,omitempty"`) {
			t.Errorf("undescribedOnPurpose[%q] excuses a field this package no longer emits; "+
				"delete the entry rather than leaving a name for something that is not there", key)
		}
	}
}

// packageSources reads this package's non-test Go files, keyed by path.
func packageSources(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	out := map[string]string{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		out[p] = string(src)
	}
	if len(out) == 0 {
		t.Fatal("no non-test source read — the glob is relative to the package directory " +
			"and returned nothing, so every check downstream is vacuous")
	}
	return out
}
