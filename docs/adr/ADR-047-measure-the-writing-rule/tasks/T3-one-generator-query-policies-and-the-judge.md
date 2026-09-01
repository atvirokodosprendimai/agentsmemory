# Task ADR-047-T3: Extract one generative client, then the query policies and the blind judge

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `gen.Client`, `longmemeval.QueryPolicy`, `longmemeval.QueryPolicies()`, `longmemeval.Judge`
**Consumes:** `longmemeval.Question` (T1)
**Data dependency:** needs a generative endpoint for a real run; the Acceptance fence is hermetic and drives `gen.Client` and `Judge` against an `httptest` server.

## Goal

Have exactly one generative-model client in this repository, reachable from `internal/`, and build
the reader and the judge on it — the judge blind to which cell produced the answer it scores.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/gen/client.go` | add | the one implementation, moved from `cmd/server/eval.go:812-1033` |
| `internal/gen/client_test.go` | add | Ollama and OpenAI-compatible wire shapes, and the `/v1` branch |
| `cmd/server/eval.go` | edit | delete `questionGen`, call `gen.Client`; `genURL` and the `EVAL_GEN_*` flags stay where they are |
| `cmd/server/kgextract.go` | edit | delete the duplicate at `:215-267`, call `gen.Client` |
| `internal/longmemeval/querypolicy.go` | add | the registry and every shipped query policy |
| `internal/longmemeval/judge.go` | add | the reader prompt, the judge prompt, the binary verdict |
| `internal/longmemeval/querypolicy_test.go`, `internal/longmemeval/judge_test.go` | add | their tests |

The move is the reason this task is `L`: two existing commands change call sites in the same
commit as the extraction, and both must build together or neither does.

## Ordered Steps

1. Write the failing tests first (TDD red): `TestGenClientCallsOllamaGenerate`,
   `TestGenClientCallsAnOpenAICompatibleEndpointForAV1URL`,
   `TestJudgeNeverSeesThePolicyName`, `TestJudgeIsBinary`, `TestQueryPolicyVerbatimIsTheQuestion`.
2. Move `questionGen` to `internal/gen` as `Client`, unchanged in behaviour, keeping its
   `openAIShaped()` `/v1` rule and its error text about an embedder being unable to answer
   `/api/generate` — that message has saved a real debugging session and is not re-worded here.
3. Repoint `cmd/server/eval.go` and `cmd/server/kgextract.go`. Run the existing `cmd/server` suite;
   it is inside this task's fence for exactly this reason.
4. Register query policies: `verbatim` (the question as typed — the baseline), `named-thing`
   (`start-here`'s rule: name the entity before asking), `decomposed` (multi-hop questions asked as
   several searches whose results are merged, capped so it cannot buy its win with more retrieval).
5. Write the reader prompt: question, then the assembled memories, and an instruction to answer
   from them alone. It is one string, held constant across the grid.
6. Write the judge: it receives question, `question_type`, the abstention flag, gold answer and
   candidate answer, and returns `correct` / `incorrect`. It receives no policy names, no cell
   label, no ordering hint — `TestJudgeNeverSeesThePolicyName` asserts the rendered prompt
   contains none of the registered policy names.
7. Branch the judge prompt on `question_type`, because the upstream evaluator does and a generic
   consistency prompt is therefore not the benchmark's metric: preference items are scored
   against a rubric rather than for equality, temporal items tolerate a stated off-by-one,
   knowledge-update items accept the superseded value when the update is present too, and `_abs`
   items are scored for unanswerability against their own prompt. Pin each branch with a test.
   Where a branch deliberately departs from upstream, say so in the results header and name the
   metric as ours — reporting a house metric under the benchmark's name is the failure this step
   exists to prevent. (Found in review of PR #148.)
7. Make the judge's parse strict: an unparseable verdict is an error that aborts the cell, never a
   silent `incorrect`. A judge failure scored as a wrong answer is a model outage recorded as a
   policy losing.

## Acceptance

```bash
set -o pipefail
  if [ -n "$(gofmt -l internal/gen internal/longmemeval cmd/server)" ]; then echo "gofmt"; exit 1; fi
  go vet ./... || exit 1
  go test ./internal/gen/ ./internal/longmemeval/ -run "TestGenClientCallsOllamaGenerate|TestGenClientCallsAnOpenAICompatibleEndpointForAV1URL|TestQueryPolicyVerbatimIsTheQuestion|TestJudgeNeverSeesThePolicyName|TestJudgeIsBinary|TestJudgeRefusesAnUnparseableVerdict|TestJudgePromptBranchesOnQuestionType|TestJudgeScoresAnAbstentionQuestionForUnanswerability" -count=1 -v 2>&1 | tee /tmp/a47t3.out
  grep -q -- "--- PASS: TestGenClientCallsOllamaGenerate" /tmp/a47t3.out || exit 1
  grep -q -- "--- PASS: TestGenClientCallsAnOpenAICompatibleEndpointForAV1URL" /tmp/a47t3.out || exit 1
  grep -q -- "--- PASS: TestQueryPolicyVerbatimIsTheQuestion" /tmp/a47t3.out || exit 1
  grep -q -- "--- PASS: TestJudgeNeverSeesThePolicyName" /tmp/a47t3.out || exit 1
  grep -q -- "--- PASS: TestJudgeIsBinary" /tmp/a47t3.out || exit 1
  grep -q -- "--- PASS: TestJudgeRefusesAnUnparseableVerdict" /tmp/a47t3.out || exit 1
  grep -q -- "--- PASS: TestJudgePromptBranchesOnQuestionType" /tmp/a47t3.out || exit 1
  grep -q -- "--- PASS: TestJudgeScoresAnAbstentionQuestionForUnanswerability" /tmp/a47t3.out || exit 1
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a47t3.out; then echo "vacuous or failing"; exit 1; fi
  go test ./cmd/server/ -count=1 || exit 1
go test ./... -count=1
```

`go test ./cmd/server/` runs separately and before the repo-wide run because the extraction's whole
risk is that those two existing commands stop working, and a repo-wide pass reported as one number
does not say which package saved the task.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestGenClientCallsOllamaGenerate` | `internal/gen/client_test.go` | the default wire shape is unchanged by the move | — |
| `TestGenClientCallsAnOpenAICompatibleEndpointForAV1URL` | `internal/gen/client_test.go` | the `/v1` branch survives the move | — |
| `TestQueryPolicyVerbatimIsTheQuestion` | `internal/longmemeval/querypolicy_test.go` | the baseline column adds nothing | — |
| `TestEveryDeclaredQueryPolicyIsSelectable` | `internal/longmemeval/policy_test.go` | same universe-from-the-registry check as T2, for columns | — |
| `TestJudgeNeverSeesThePolicyName` | `internal/longmemeval/judge_test.go` | the rendered judge prompt contains no registered policy name | — |
| `TestJudgeIsBinary` | `internal/longmemeval/judge_test.go` | correct/incorrect, no partial credit to argue about later | — |
| `TestJudgeRefusesAnUnparseableVerdict` | `internal/longmemeval/judge_test.go` | a model outage is an error, not a lost point | — |
| `TestJudgePromptBranchesOnQuestionType` | `internal/longmemeval/judge_test.go` | the rendered prompt differs per `question_type`, so the score is the benchmark's metric rather than a generic consistency check | — |
| `TestJudgeScoresAnAbstentionQuestionForUnanswerability` | `internal/longmemeval/judge_test.go` | an `_abs` item is judged for refusing to answer, not for matching a gold string it has none of | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the tests above |
| 2 — something selects it | `gen.Client` is selected by three call sites; deleting the `internal/gen` import from `eval.go` fails the build, which is the strongest form of this rung |
| 3 — the caller can discover it | the `EVAL_GEN_*` flags and their `--help` text, already documented in `.env.example:148-156`; no new variable is introduced, so `TestDocumentedEnvVarsAreRead` and `TestReadEnvVarsAreDocumented` stay satisfied by the existing entries |
| 4 — it is used | `eval` and `kgextract` already use it; the third caller arrives in T4 |

## Mutation Log

## Invariants

- After this task there is exactly one implementation of the generative call in the tree.
- No new environment variable. The `EVAL_GEN_*` trio serves all three callers.
- The judge prompt is a pure function of (question, gold, candidate).

## Risks

- The extraction changes behaviour subtly — a header, a timeout default, a stream flag — and
  `eval` degrades quietly. Mitigation: the `cmd/server` suite runs inside the fence, and the two
  wire-shape tests pin the request bodies rather than only the responses.
- A hidden third copy exists somewhere. Mitigation: `grep -rn "api/generate"` after the move should
  return `internal/gen` and nothing else; that grep is worth a line in the sign-off.

## Stop Condition

Stop if repointing `kgextract.go` requires changing its prompt or its parsing: this task is a pure
move, and a move that needs behaviour changes to land is two tasks wearing one id.

## Out of Scope

- Changing which model anything defaults to (permanent: `EVAL_GEN_MODEL`'s default is not this
  ADR's to move)
- A judge with partial credit or a rubric (deferred: `docs/adr/BACKLOG.md` §"From ADR-047")

## Verification Log
