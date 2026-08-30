package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Bindings for docs/specs/2026-08-27-recall-before-asserting.md — the two facts
// this package owns. Both belong to mechanisms that ADR-041 recorded as NOT
// SHIPPED, for reasons measured and written down.
//
// ⚠ THEY SKIP RATHER THAN FAIL, AND THE SKIP RETIRES ITSELF. A permanent red is
// not a gate: it blocks every merge, so it gets deleted or the branch gets
// abandoned, and either way the binding is gone. A permanent skip is worse — it
// is a test that cannot fail, which this repository has shipped four times.
//
// So the skip is CONDITIONAL ON THE RECORD. While the owning task is recorded
// `blocked`, the mechanism does not exist and there is nothing to assert; the
// skip prints the recorded reason. The moment that task is recorded `done`, the
// mechanism shipped and the skip becomes a FAILURE demanding the real assertion.
// A binding cannot be quietly outlived by the thing it binds.

// adr041TaskIndex is the record these bindings read for the status of the task
// that owns each fact.
const adr041TaskIndex = "../../docs/adr/ADR-041-the-recall-that-does-not-depend-on-remembering/tasks/README.md"

var taskIndexRow = regexp.MustCompile(`(?m)^\|\s*(T\d+)\s*\|\s*[^|]+?\s*\|\s*([a-z]+)\s*\|`)

// statusOfTask returns the recorded status of one ADR-041 task.
func statusOfTask(t *testing.T, id string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(adr041TaskIndex))
	if err != nil {
		t.Fatalf("read the ADR-041 task index: %v — without it this binding cannot tell a "+
			"mechanism that has not shipped from one that shipped untested", err)
	}
	for _, m := range taskIndexRow.FindAllStringSubmatch(string(raw), -1) {
		if m[1] == id {
			return m[2]
		}
	}
	t.Fatalf("the ADR-041 task index names no %s — this binding is pinned to a task that no "+
		"longer exists, so it can no longer tell whether its mechanism shipped", id)
	return ""
}

// TestF11InstructionsNameTheClassOfClaimNotTheDuty is ADR-041 T6's gate: the
// handshake names the CLASS OF CLAIM that needs a recall, and gives no bare order
// to recall.
//
// ⚠ THE BARE IMPERATIVE WAS MEASURED NOT TO WORK, which is why replacing it is the
// mechanism rather than softening it. "RECALL BEFORE YOU ACT" sat at the top of
// every handshake and did not fire on the session that wrote this spec; ADR-017
// measured the same thing from the other side — the whole protocol delivered to a
// subagent produced 0 recalls in 5 dispatches, one short paragraph produced 5. An
// instruction competes with every other instruction in the context. A NAME for a
// class of claim is something the agent can notice itself about to make.
//
// ⚠ IT REPLACES RATHER THAN ADDS (F-8, and the F-7 ceiling). Prose added to a
// document the agent already receives in full is not a mechanism under this spec,
// so this test also fails if the imperative survives beside the cue.
func TestF11InstructionsNameTheClassOfClaimNotTheDuty(t *testing.T) {
	// The duty, in every form the old text used. A cue that leaves the order in
	// place has added a paragraph, which F-8 says is not a mechanism.
	for _, imperative := range []string{
		"RECALL BEFORE YOU ACT",
		"before reading code",
	} {
		if strings.Contains(serverInstructions, imperative) {
			t.Errorf("serverInstructions still carries the bare imperative %q. F-11 replaces the "+
				"order with a name for the class of claim; keeping both is the added paragraph "+
				"F-8 rules out as a mechanism", imperative)
		}
	}

	// The class itself, in the three shapes F-2 defines it by. These are what an
	// agent has to recognise itself about to write.
	// ⚠ EACH SHAPE MUST BE UNIQUE TO THE CUE. "never" alone is credited by the
	// pre-existing "never a safe default" in the unchanged scope paragraph — the
	// substring-credit class AGENTS.md names, where a check passes on text it is not
	// about. The phrase from the cue is what is asserted.
	for _, shape := range []string{"still works a given way", "does not do something", "never decided"} {
		// Case-insensitive: the handshake may emphasise a shape in caps, and a gate that
		// fails on capitalisation is a gate about typography.
		if !strings.Contains(strings.ToLower(serverInstructions), shape) {
			t.Errorf("serverInstructions does not name the %q shape. F-2's countable unit is a "+
				"claim that NOTHING CHANGED, and an agent cannot notice itself making one if "+
				"the handshake never says what one looks like", shape)
		}
	}

	// The reason, without which the class is a list of words. Source cannot show
	// that nothing changed, because a fix looks identical to code that was always
	// right — that is the whole argument for asking the palace instead.
	if !strings.Contains(serverInstructions, "always") {
		t.Error("serverInstructions names the class but not why source cannot settle it: a fix " +
			"looks identical to code that was ALWAYS right, which is the one thing reading the " +
			"tree cannot tell you and the reason this class is the palace's to answer")
	}
}
