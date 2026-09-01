# Task ADR-047-T2: The write-policy registry, and a flag that can select every member of it

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `longmemeval.WritePolicy`, `longmemeval.WritePolicies()`, `longmemeval.WritePolicyByName()`
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
   name resolves through `WritePolicyByName` and appears in the flag's usage string.
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
  go test ./internal/longmemeval/ -run "TestVerbatimPolicyIsOneRecordPerSession|TestQuestionFirstPolicyOpensWithTheQuestion|TestOneFactPolicyKeepsEveryAnsweringTurn|TestBoundedPolicySplitsAtTheThreshold|TestEveryDeclaredPolicyIsSelectable|TestEveryPolicyPreservesSessionProvenance" -count=1 -v 2>&1 | tee /tmp/a47t2.out
  grep -q -- "--- PASS: TestVerbatimPolicyIsOneRecordPerSession" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestQuestionFirstPolicyOpensWithTheQuestion" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestOneFactPolicyKeepsEveryAnsweringTurn" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestBoundedPolicySplitsAtTheThreshold" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestEveryDeclaredPolicyIsSelectable" /tmp/a47t2.out || exit 1
  grep -q -- "--- PASS: TestEveryPolicyPreservesSessionProvenance" /tmp/a47t2.out || exit 1
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
| 2 — something selects it | `WritePolicyByName`, exercised by `TestEveryDeclaredPolicyIsSelectable`; the mutation is deleting one registration and watching that test go red |
| 3 — the caller can discover it | the flag usage string built from `WritePolicies()`, asserted by the same test |
| 4 — it is used | T4's grid; nothing measures usage of an eval command and nothing should |

## Mutation Log

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
