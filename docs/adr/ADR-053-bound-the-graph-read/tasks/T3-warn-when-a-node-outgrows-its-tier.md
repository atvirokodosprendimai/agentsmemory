# Task ADR-053-T3: Tell the writer when a node has outgrown its tier

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file plus its test)
**Owner:** unassigned
**Produces:** `am_kg_add` fan-out warning
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `the write still succeeding when the warning fires`

## Goal

Make the ~35-leaf convention visible at the moment it is broken, to the only
party who can act on it, without ever losing a fact to a shape.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/kg.go` | edit | count the subject's live edges after the write and return the count |
| `internal/mcpserver/kg.go` | edit | render the warning, and describe it — a field that appears only past a threshold is invisible until the case that produces it, so the description is the only way a caller knows it can arrive |
| `internal/palace/kg_test.go` | edit | the gate, including that the write still lands |

## Ordered Steps

1. [S1] Write `TestAnOversizedNodeWarnsAndStillWrites` first: file enough edges on one subject to pass the limit, and assert BOTH that the response carries the warning and that the fact is queryable afterwards. It fails today because no warning exists (TDD red). ⚠Assert both halves — a warning that accompanied a refusal would satisfy a warning-only check, and refusing is what the owner rejected.
2. [S2] Add the limit as a named constant with the reason beside it: a node over roughly this many leaves is one whose fan-out will spend a reader's whole budget, and splitting by topic is the remedy the skills teach. Cite ADR-053.
3. [S3] Count the subject's CURRENT edges after the write, not before, so the warning describes the node the caller has just created rather than the one they started from.
4. [S4] Render the warning as its own field carrying the count and the advice to split by topic. Do not fold it into an error string — a caller parsing prose for a threshold is a caller who will stop parsing it.
5. [S5] ⚠**The mutant is the warning field dropped from the rendered response while the count is still computed.** That is the inert-mechanism shape: the server does the work, the caller never sees it, and every behavioural test on the write still passes. The gate must go red. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ ./internal/mcpserver/ -run 'TestAnOversizedNodeWarnsAndStillWrites$|TestANodeUnderTheLimitWarnsAboutNothing$|TestAnOversizedNodeWarnsThroughTheToolSurface$' -count=1 2>&1 | tee /tmp/adr053-t3a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr053-t3a.out \
  && go test ./internal/palace/... ./internal/mcpserver/... -count=1 2>&1 | tee /tmp/adr053-t3b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr053-t3b.out
```

⚠ The unit half now runs both packages because the surface test lives in
`internal/mcpserver` — and it exists only because the mutant survived without it.
The earlier note said the opposite; it is corrected rather than replaced, because
the reason the fence was narrowed is still true of the two service tests.

⚠ The narrowing that note describes, not both. Both tests live in
`internal/palace`, and a filter run against a package that holds neither exits 0
with "no tests to run" — which this fence's own guard then catches as a failure.
That is the fence working, and it caught exactly that on its first run: the
regression half is where `internal/mcpserver` belongs.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnOversizedNodeWarnsAndStillWrites` | `internal/palace/kg_test.go` | passing the fan-out limit returns the warning with its count AND leaves the fact queryable | — | S1, S3, S4 |
| `TestANodeUnderTheLimitWarnsAboutNothing` | `internal/palace/kg_test.go` | the field is absent below the limit, so its presence is the signal rather than a value every caller compares against | — | S2, S4 |
| `TestAnOversizedNodeWarnsThroughTheToolSurface` | `internal/mcpserver/kg_test.go` | the warning reaches a caller of `am_kg_add`, not merely a caller of the service | — | S4, S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests above |
| 2 — something selects it | the `am_kg_add` handler renders the field, and `TestAnOversizedNodeWarnsThroughTheToolSurface` goes red when the render is deleted. ⚠ This row was FALSE as first written: both tests drove the service directly, so the mutant that drops the render SURVIVED — measured, not reasoned. A service-level test cannot see a surface-level drop by construction, because it is already past the layer that drops it |
| 3 — the caller can discover it | `am_kg_add`'s description names the warning and the threshold, because a field that only appears past a threshold cannot be discovered by a caller who has never crossed it |
| 4 — it is used | every write evaluates it; the live corpus has one subject at 184 edges that would trigger it today |

## Mutation Log

- 2026-09-04 · eb7b41c* · mutant killed · exit 1 · `internal/mcpserver/kg.go` · the count is computed in the service and dropped at the surface — the server does the work and the caller never learns it happened. This mutant SURVIVED on its first run, when both tests drove the service directly, which is what proved the surface test missing · acceptance-sha256:b2211800385b426dae3f596a6b3df9d6a7c8987c19f7d990dba45a8fd46efda2

## Invariants

- The write always lands. This task never converts a warning into a refusal — that was the owner's decision on 2026-09-04 and the reason is recorded in the ADR's Out of Scope.
- The field is absent below the threshold.
- The count is of CURRENT edges, so retracted ones do not inflate it.

## Risks

- A warning nobody reads changes nothing. Named in the ADR's Risks and accepted deliberately: it is the known cost of warn-over-refuse rather than a defect in this task.
- Counting after every write adds a query to the write path. Mitigated by counting one indexed subject, and by the write already being serialised behind one connection.

## Stop Condition

Stop and ask if the count cannot be taken without a second round trip that
measurably slows the write. The warning is worth a cheap query and is not worth
a slow write; if it is not cheap, the threshold check belongs somewhere other
than the write path and that is a different decision.

## Out of Scope

- Refusing a write above the limit (permanent: boundary: the owner chose warn over refuse on 2026-09-04 — the write is the moment the agent holds the knowledge, and refusing there loses a fact to save a shape)
- Splitting an oversized node automatically (permanent: boundary: where a node splits is a judgement about topic that no tool has the context to make, which is why the warning names the remedy rather than applying it)

## Verification Log
- 2026-09-04 · eb7b41c* · exit 0 · `set -o pipefail …` · acceptance-sha256:b2211800385b426dae3f596a6b3df9d6a7c8987c19f7d990dba45a8fd46efda2 · ms:21952
