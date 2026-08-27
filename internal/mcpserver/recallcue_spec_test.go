package mcpserver

import "testing"

// Bindings for docs/specs/2026-08-27-recall-before-asserting.md — the two facts
// this package owns. Deliberately red; see clients/claude-code/recallrate_spec_test.go
// for why a spec's tests travel with the document.

func TestF11InstructionsNameTheClassOfClaimNotTheDuty(t *testing.T) {
	t.Fatal("not built yet — F-11 (and UC2-S1): serverInstructions must name the CLASS OF CLAIM " +
		"that requires a recall — an assertion that nothing changed — and must not carry a bare " +
		"instruction to recall before acting. The bare form is there today and did not fire on " +
		"the session that wrote this spec. ⚠It is candidate #4 of four, the MOST " +
		"compliance-dependent, so F-8's caveat applies to it and it ships last")
}

func TestF14NoSchemaLookupBeforeTheFirstCall(t *testing.T) {
	t.Fatal("not built yet — F-14: the am_* tools are registered so no schema lookup is needed " +
		"before the first call. Deferred loading turns a reflex into a decision, and a decision " +
		"gets made only when there is already a reason — which is exactly when recall is least " +
		"needed. Candidate #1: it asks nothing of the agent, so it ships first")
}
