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

// skipWhileBlocked skips only while the owning task is recorded blocked, and
// fails the moment it is recorded done without the real assertion being written.
func skipWhileBlocked(t *testing.T, task, fact, recorded string) {
	t.Helper()
	switch st := statusOfTask(t, task); st {
	case "blocked", "pending":
		t.Skipf("%s is unbound because %s is recorded %q: %s", fact, task, st, recorded)
	case "done":
		t.Fatalf("%s is recorded done, so the mechanism SHIPPED — but %s is still a stub. "+
			"Write the real assertion: a mechanism that ships without its spec binding is "+
			"exactly the untested capability this spec exists to prevent", task, fact)
	default:
		t.Fatalf("%s is recorded %q, which this binding does not know how to read", task, st)
	}
}

func TestF11InstructionsNameTheClassOfClaimNotTheDuty(t *testing.T) {
	// ⚠ THIS RUNS BEFORE THE SKIP, and the order is the whole point. Written after
	// it, this assertion is unreachable: t.Skipf returns, and the check silently
	// never runs — a test that cannot fail, which is the exact defect this
	// repository keeps shipping. It was written that way first and caught by
	// mutating the string below and watching the test stay green.
	//
	// It pins the PREMISE F-11 rests on: the bare imperative is what F-11 exists to
	// replace. If it ever leaves serverInstructions by some other route, the fact's
	// motivation changed and it needs re-reading rather than re-skipping.
	if !strings.Contains(serverInstructions, "RECALL BEFORE YOU ACT") {
		t.Errorf("serverInstructions no longer carries the bare imperative F-11 was written " +
			"against; the fact's premise has changed and it needs re-stating")
	}

	skipWhileBlocked(t, "T6", "F-11 (and UC2-S1)",
		"serverInstructions must name the CLASS OF CLAIM that requires a recall — an assertion "+
			"that nothing changed — and must not carry a bare instruction to recall before "+
			"acting. The bare form is there today and did not fire on the session that wrote "+
			"this spec. It is candidate #4 of four, the MOST compliance-dependent, so F-8's "+
			"caveat applies and it ships last")
}

func TestF14NoSchemaLookupBeforeTheFirstCall(t *testing.T) {
	skipWhileBlocked(t, "T3", "F-14",
		"the am_* tools would be registered so no schema lookup is needed before the first call. "+
			"MEASURED 2026-08-28 and NOT IMPLEMENTABLE AS STATED: deferral is a property of the "+
			"HARNESS, not of the server — a two-tool MCP server is deferred just the same, so no "+
			"registration choice this server makes can remove the lookup. T3 records the finding")
}
