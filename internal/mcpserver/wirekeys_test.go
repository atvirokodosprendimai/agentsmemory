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
	"line": "a field INSIDE code_anchors, same reason as checked_at.",
	"updated_at": "INERT, and never even reaches the wire. tunnels.updated_at is " +
		"TEXT NOT NULL DEFAULT '' (00009_graph.sql:48), nothing in this codebase assigns it, " +
		"and GORM's automatic timestamping does not apply to a string field with an explicit " +
		"column tag. So it is empty for every tunnel this store has written, and omitempty " +
		"then drops it. Describing a field no response carries would be a promise with " +
		"nothing behind it.",
	"last_activated": "INERT, and must not be advertised. internal/palace/hallway.go:143 " +
		"records that nothing has ever written it after initDynamics stamped it at creation, " +
		"so it reports the moment a hallway was derived and never an activation. Describing " +
		"it would promise reinforcement this store does not implement — issue #38, where the " +
		"finding is that decay and reinforcement are not miscomputed, they do not exist.",
}

// omitemptyTag finds a response field that is absent from an ordinary answer.
var omitemptyTag = regexp.MustCompile(`json:"([a-z][a-z0-9_]*),omitempty"`)

// descriptionText pulls the prose an agent actually reads at the call.
var descriptionText = regexp.MustCompile(`(?s)mcp\.(?:With)?Description\((.*?)\)\)`)

// TestEveryOmitemptyWireKeyIsDescribed: a field a caller cannot discover by
// looking at one response must be named in the prose beside the call.
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
// Baseline when written: 26 omitempty response fields, 9 of them undescribed. Those
// nine were fixed rather than allowlisted, except where a written reason says why
// prose would buy nothing.
func TestEveryOmitemptyWireKeyIsDescribed(t *testing.T) {
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

	var undescribed []string
	for key, file := range keys {
		if strings.Contains(prose.String(), key) {
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
	for key, why := range undescribedOnPurpose {
		if strings.TrimSpace(why) == "" {
			t.Errorf("undescribedOnPurpose[%q] has no reason — the reason IS the review", key)
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
