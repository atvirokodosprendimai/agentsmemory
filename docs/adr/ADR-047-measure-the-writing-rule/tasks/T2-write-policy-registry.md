# Task ADR-047-T2: The write-policy registry, and a flag that can select every member of it

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** `longmemeval.WritePolicy`, `longmemeval.WritePolicies()`, `longmemeval.WritePolicyByName()`, `longmemeval.WritePolicyUsage()`
**Consumes:** `longmemeval.Dataset` / `Question` / `Session` (T1)
**Data dependency:** hermetic — policies are pure functions from a `Question` to a slice of records; the palace write happens in T4.

## Goal

Turn "how an agent should write a memory" into a named, registered, selectable function, and make
it structurally impossible for one to exist without the flag being able to choose it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/longmemeval/writepolicy.go` | add | the registry and every shipped policy |
| `internal/longmemeval/writepolicy_test.go` | add | per-policy behaviour |
| `internal/longmemeval/policy_test.go` | add | `TestEveryDeclaredPolicyIsSelectable` — derives its universe from the registry, so a policy added tomorrow joins the check on the same commit |

The registry IS the selection mechanism, so it is named here rather than in a composition root:
T4's flag reads `WritePolicies()` for its allowed values and its `--help` text, and deleting a
registration removes the row from both.

## Ordered Steps

1. Write the failing tests first (TDD red), including `TestEveryDeclaredPolicyIsSelectable`.
2. Define `WritePolicy{Name, Describe string, Write func(Question) []Record}` where `Record` is
   `{Room, Content string, SessionID string, AnsweringTurns []int}` — deliberately not a palace
   type, so a policy cannot reach the store. `SessionID` names the haystack session the record's
   text came from and `AnsweringTurns` the indices of that session's `has_answer` turns it
   carries. Both are **provenance, not content**: neither is written into the drawer and neither
   reaches the reader, so no policy can spend budget on them or leak the answer's location into
   the prompt. T4 keeps the returned drawer id against the `Record` that produced it, which is
   what lets the retrieval-only column be scored against `answer_session_ids` at all. Without
   this, once a transformed record is in the store nothing recovers which session produced it —
   ordinal position cannot, because `one-fact` and `bounded` change the record count and
   duplicate content is legal. A policy that cannot name its source session is a policy whose
   retrieval column is unscoreable, so this is a contract rather than a convenience. (Found in
   review of PR #148: T2 defined the record and T4 required the column, and neither task could
   see that the first made the second impossible.)
3. Register `verbatim` — one record per haystack session, turns joined unedited. **This is the
   baseline the ADR names**, and it is registered first so a reader of the file meets it first.
4. Register `question-first` — each session rewritten so its first line is the question it answers,
   per `start-here`'s rule. This is the rule under test, not an assumption.
5. Register `one-fact` — each `has_answer` turn and its neighbour as their own record, the
   "give experience its own record" rule taken to its limit.
6. Register `bounded` — verbatim, but split at the 1600-rune threshold the skills teach, so the
   size rule is separable from the titling rule.
7. Every policy is deterministic and does no I/O. A policy needing a model is a later task; mixing
   one in here would make the row a measurement of that model.
8. Write `TestEveryDeclaredPolicyIsSelectable` so it iterates `WritePolicies()` and asserts each
   name resolves through `WritePolicyByName` and appears in `WritePolicyUsage()`.

   ⚠**`WritePolicyUsage()` is produced HERE, and it is why this step is writable at all.** The
   earlier draft said "appears in the flag's usage string" — but the flag lives in
   `cmd/server/longmemeval.go`, which T4 creates and which is in T4's Affected Files, not this
   task's. There is no flag to assert against while T2 is being executed, so the step as written
   could only have been satisfied by deferring the check to T4 and calling T2 done without it.
   This task therefore owns the RENDERING of the allowed-values text and gates that every
   registered policy appears in it; T4's flag is required to use this function rather than
   formatting its own list, and T4's `TestLongmemevalHelpListsEveryRegisteredPolicy` closes the
   rung at the command level. Two gates, one for each half of "the caller can discover it".

   Found while executing T2, and it is the same shape as the contract defect PR #148's review
   found between this task and T4: two task files each internally coherent, jointly unsatisfiable,
   with the DAG edge between them correct — a `Consumes` edge asserts order, never sufficiency.
9. Write `TestEveryPolicyPreservesSessionProvenance` so it iterates `WritePolicies()`, runs each
   over a fixture question, and asserts every returned `Record` names a `SessionID` that is in
   that question's haystack, and that the union of `AnsweringTurns` across a policy's records
   covers every `has_answer` turn the question has. It derives its universe from the registry for
   the same reason `TestEveryDeclaredPolicyIsSelectable` does: a policy added tomorrow joins the
   check on the same commit, rather than being the one whose retrieval column is silently blank.

## Acceptance

```bash
set -o pipefail
  if [ -n "$(gofmt -l internal/longmemeval)" ]; then echo "gofmt"; exit 1; fi
  go vet ./... || exit 1
  go test ./internal/longmemeval/ -run "TestVerbatimPolicyIsOneRecordPerSession|TestQuestionFirstPolicyOpensWithTheQuestion|TestOneFactPolicyKeepsEveryAnsweringTurn|TestBoundedPolicySplitsAtTheThreshold|TestEveryDeclaredPolicyIsSelectable|TestEveryPolicyPreservesSessionProvenance|TestEveryPolicyIsDeterministic" -count=1 -v 2>&1 | tee /tmp/a47t2.out
  grep -q -- "--- PASS: TestVerbatimPolicyIsOneRecordPerSession" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestQuestionFirstPolicyOpensWithTheQuestion" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestOneFactPolicyKeepsEveryAnsweringTurn" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestBoundedPolicySplitsAtTheThreshold" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestEveryDeclaredPolicyIsSelectable" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestEveryPolicyPreservesSessionProvenance" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestEveryPolicyIsDeterministic" /tmp/a47t2.out || exit 1
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a47t2.out; then echo "vacuous or failing"; exit 1; fi
go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestVerbatimPolicyIsOneRecordPerSession` | `internal/longmemeval/writepolicy_test.go` | the baseline edits nothing and loses no turn | — |
| `TestQuestionFirstPolicyOpensWithTheQuestion` | `internal/longmemeval/writepolicy_test.go` | the rule is implemented as stated, not approximated | — |
| `TestOneFactPolicyKeepsEveryAnsweringTurn` | `internal/longmemeval/writepolicy_test.go` | no `has_answer` turn is dropped by the split | — |
| `TestBoundedPolicySplitsAtTheThreshold` | `internal/longmemeval/writepolicy_test.go` | splitting happens at the documented rune count, counted in runes not bytes | — |
| `TestEveryDeclaredPolicyIsSelectable` | `internal/longmemeval/policy_test.go` | every registered policy is reachable by name and named in the usage text | — |
| `TestEveryPolicyIsDeterministic` | `internal/longmemeval/writepolicy_test.go` | two calls on one question give identical records | — |
| `TestEveryPolicyPreservesSessionProvenance` | `internal/longmemeval/policy_test.go` | every registered policy names the haystack session each record came from, and covers every `has_answer` turn — without which T4's retrieval column cannot be scored | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the per-policy tests |
| 2 — something selects it | `WritePolicyByName`, plus the per-policy tests, which name their policy and fail with "no write policy named X is registered" when it goes missing. ⚠**NOT `TestEveryDeclaredPolicyIsSelectable` — measured 2026-09-01, severing the `verbatim` registration left that test GREEN.** A gate whose universe is DERIVED from the registry cannot notice a deletion FROM that registry: the deleted policy leaves the universe along with the wiring, so there is nothing left to assert about. Derivation buys immunity to a stale checklist and buys nothing against deletion, and the two need different gates. The deletion is caught here only because four named policies are asserted by name in the same fence |
| 3 — the caller can discover it | `WritePolicyUsage()`, built from `WritePolicies()` and asserted by the same test — the half of this rung that is reachable while T2 runs. The command-level half is T4's `TestLongmemevalHelpListsEveryRegisteredPolicy`, which fails if the flag formats its own list instead of calling this |
| 4 — it is used | T4's grid; nothing measures usage of an eval command and nothing should |

## Mutation Log

- 2026-09-01 · ab57313* · mutant killed · exit 1 · `internal/longmemeval/writepolicy.go` · usage text stops naming policies, so no --help derived from it could list one · acceptance-sha256:750606dc99b967694be06451db3021b87a982674c1740a0d2f84af96f2c6805c
- 2026-09-01 · ab57313* · mutant killed · exit 1 · `internal/longmemeval/writepolicy.go` · bounded stops naming its source session, so T4 retrieval column is unscoreable for it · acceptance-sha256:750606dc99b967694be06451db3021b87a982674c1740a0d2f84af96f2c6805c
- 2026-09-01 · ab57313* · mutant killed · exit 1 · `internal/longmemeval/writepolicy.go` · usage text stops naming policies, so no --help derived from it could list one · acceptance-sha256:1062d17f8b543b8f91050845fa84cc7ad543446e4c1f96248fdf28b92cc0045f
- 2026-09-01 · ab57313* · mutant killed · exit 1 · `internal/longmemeval/writepolicy.go` · bounded stops naming its source session, so T4 retrieval column is unscoreable for it · acceptance-sha256:1062d17f8b543b8f91050845fa84cc7ad543446e4c1f96248fdf28b92cc0045f

## Invariants

- `verbatim` is registered and never removed: it is the baseline every delta is against.
- No policy performs I/O, calls a model, or touches `internal/palace`.
- A policy's `Describe` is its `--help` text; the two never diverge because there is only one.

## Risks

- A policy that quietly drops content would look like a strong compressor. Mitigation:
  `TestOneFactPolicyKeepsEveryAnsweringTurn` asserts the answering turns survive, which is the
  content whose loss the judged metric would otherwise attribute to the policy being "concise".

## Stop Condition

Stop if `question-first` cannot be implemented without a model: rewriting a session's opening line
into the question it answers may need generation, and if it does, this policy moves to T3 where a
model is already in play — and the ADR's claim that the rows differ only by policy needs the
model's contribution stated.

## Out of Scope

- Writing any record into a palace (that is T4)
- A policy that calls a model to summarise (deferred: `docs/adr/BACKLOG.md` §"From ADR-047")

## Verification Log
- 2026-09-01 · ab57313* · exit 1 · `set -o pipefail …` · acceptance-sha256:750606dc99b967694be06451db3021b87a982674c1740a0d2f84af96f2c6805c
  ```
  --- last 3 line(s) of stderr
  # github.com/atvirokodosprendimai/agentsmemory/internal/longmemeval
  # [github.com/atvirokodosprendimai/agentsmemory/internal/longmemeval]
  vet: internal/longmemeval/writepolicy_test.go:22:45: undefined: WritePolicy
  ```
- 2026-09-01 · ab57313* · exit 0 · `set -o pipefail …` · acceptance-sha256:750606dc99b967694be06451db3021b87a982674c1740a0d2f84af96f2c6805c
- 2026-09-01 · ab57313* · exit 0 · `set -o pipefail …` · acceptance-sha256:1062d17f8b543b8f91050845fa84cc7ad543446e4c1f96248fdf28b92cc0045f
