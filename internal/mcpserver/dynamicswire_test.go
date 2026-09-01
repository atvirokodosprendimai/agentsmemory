package mcpserver

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// jsonTagName pulls the wire key out of a struct tag, with or without options.
var jsonTagName = regexp.MustCompile(`json:"([a-z][a-z0-9_]*)(?:,[^"]*)?"`)

// dynamicsBlock isolates the Dynamics struct body so a tag elsewhere in palace.go
// cannot widen the forbidden set.
var dynamicsBlock = regexp.MustCompile(`(?s)type Dynamics struct \{(.*?)\n\}`)

// dynamicsKeys returns the wire keys of palace.Dynamics, read from its source.
//
// The universe is DERIVED rather than written out as a literal, because a list kept
// beside the truth is the shape this repository rejects: a fifth dynamics field
// added tomorrow joins this check on the same commit instead of waiting for someone
// to remember ADR-048. It returns the keys rather than asserting on them, so the falsifiability
// subtest can drive the same function over a fixture that IS an offender.
func dynamicsKeys(src string) []string {
	block := dynamicsBlock.FindStringSubmatch(src)
	if block == nil {
		return nil
	}
	var keys []string
	for _, m := range jsonTagName.FindAllStringSubmatch(block[1], -1) {
		keys = append(keys, m[1])
	}
	sort.Strings(keys)
	return keys
}

// dynamicsKeysDeclaredInThisPackage reports every forbidden key this package still
// DECLARES AS A JSON TAG, as "key (file)" strings.
//
// ⚠ The scope is the name, not a hedge. It reads json tags in this package's own
// non-test sources, so a handler returning palace.Tunnel directly, embedding it, or
// building a map[string]any{"strength": …} puts the key back on the wire with this
// gate green. That is the same blindness wirekeys_test.go records against itself,
// and the same remedy: say the narrower true thing rather than the broader false one.
//
// Both halves of the gate go through here — the real run over the package's own
// sources and the falsifiability subtest over a fixture — so severing the matching
// turns the subtest red rather than leaving a disabled gate announcing success.
// That shape is why AGENTS.md requires the falsifiable half to share the function
// rather than reimplement it.
func dynamicsKeysDeclaredInThisPackage(forbidden []string, sources map[string]string) []string {
	var found []string
	for file, src := range sources {
		for _, m := range jsonTagName.FindAllStringSubmatch(src, -1) {
			for _, bad := range forbidden {
				if m[1] == bad {
					found = append(found, m[1]+" ("+file+")")
				}
			}
		}
	}
	sort.Strings(found)
	return found
}

// TestNoDynamicsFieldIsDeclaredOnTheWireInThisPackage fails when this package declares
// a json tag naming a field of palace.Dynamics.
//
// ADR-048 retires the L7 dynamics surface: strength, stability, last_activated and
// access_count were stamped once by initDynamics and never written again, so every
// result advertised a reinforcement layer the server does not implement. Removing
// them is a one-commit edit; keeping them removed is what this gate is for, because
// the views are hand-maintained and the next person to add a field to hallwayView
// has no reason to know any of this. The owner's 2026-08-28 ruling rejects wiring
// them up, so a field returning to the wire is a regression rather than a feature.
func TestNoDynamicsFieldIsDeclaredOnTheWireInThisPackage(t *testing.T) {
	palaceSrc, err := os.ReadFile("../palace/palace.go")
	if err != nil {
		t.Fatalf("read palace.go: %v", err)
	}
	forbidden := dynamicsKeys(string(palaceSrc))
	if len(forbidden) == 0 {
		t.Fatal("read no json tags from palace.Dynamics, so every check below is vacuous — " +
			"the struct was renamed or its tags removed, and this gate must be re-pointed rather " +
			"than left reporting a clean package it never looked at")
	}

	if found := dynamicsKeysDeclaredInThisPackage(forbidden, packageSources(t)); len(found) > 0 {
		t.Errorf("these palace.Dynamics fields are published on the wire: %s\n"+
			"  ADR-048 retired them: nothing writes them after initDynamics stamps them, so each one "+
			"describes a reinforcement layer this server does not implement. Remove the field from "+
			"the view rather than documenting it.", strings.Join(found, ", "))
	}

	// ⚠ A corpus with zero offenders cannot exercise the branch that reports one, so
	// the falsifiability half is a SUBTEST rather than a sibling: the acceptance fence
	// runs one test name, and a sibling would sit outside it where a mutant is graded
	// against a check the fence never ran.
	t.Run("a returned key is caught", func(t *testing.T) {
		fixture := map[string]string{
			"fake.go": "type v struct {\n\tAccessCount int `json:\"access_count\"`\n}",
		}
		found := dynamicsKeysDeclaredInThisPackage(forbidden, fixture)
		if len(found) != 1 || !strings.HasPrefix(found[0], "access_count") {
			t.Fatalf("the detector reported %v over a fixture that DOES publish access_count; "+
				"a gate that cannot see an offender cannot report a clean package either", found)
		}
	})
}
