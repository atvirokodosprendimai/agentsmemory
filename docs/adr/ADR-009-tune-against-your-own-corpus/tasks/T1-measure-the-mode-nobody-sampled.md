# Task ADR-009-T1: Run the query mode nobody has ever run

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** a `literal`-mode table beside a `paraphrase` one, on the same corpus and the same cases count
**Consumes:** none
**Data dependency:** needs a populated corpus — a ~5,000-drawer palace. A small one tops out at a 100% retrieval ceiling where arms cannot separate, so the run decides nothing

## Goal

Both query modes are measured on one corpus, so the tuner is never asked to choose from half the evidence.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/adr/ADR-009-tune-against-your-own-corpus/evidence/README.md` | add | the two commands as run, the corpus size, and the two tables |
| `docs/adr/ADR-009-tune-against-your-own-corpus/evidence/*.cells.json` | add | machine-readable run records; no case text, no drawer ids |
| `cmd/server/eval_test.go` | edit | **the selection**: a test that the literal style actually produces identifier-bearing questions, since a style that silently degrades to paraphrase would make this whole task a no-op |

## Ordered Steps

1. Write the failing test first (TDD red): `TestLiteralStyleKeepsIdentifiers` — generated literal-mode questions must share rare tokens with their gold memory at a materially higher rate than paraphrase ones. Commit it red.
2. Confirm the style is reachable end to end. It is implemented at `cmd/server/eval.go:577` and has never been run; a style that exists and produces paraphrase-shaped questions would be this repository's characteristic defect in the eval itself.
3. Run both modes on one corpus, same `--n`, same `--pool`, each into its own `--cases` file so the questions are fixed and the run is replayable.
4. Record the retrieval ceiling for each. A mode whose ceiling is far lower is measuring a harder question set, not a worse ranker, and comparing the two without saying so would mislead the tuner.
5. Commit only the `.cells.json` files. The `.jsonl` case files and `.results.json` carry queries and drawer ids from a private palace and stay untracked, with their sha256 recorded.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  go vet ./...
  go test ./cmd/server/ -run "TestLiteralStyleKeepsIdentifiers" -count=1 -v 2>&1 | tee /tmp/t1.out
  grep -q -- "--- PASS: TestLiteralStyleKeepsIdentifiers" /tmp/t1.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/t1.out; then exit 1; fi'
test -s docs/adr/ADR-009-tune-against-your-own-corpus/evidence/literal.cells.json
test -s docs/adr/ADR-009-tune-against-your-own-corpus/evidence/paraphrase.cells.json
```

The two `test -s` lines are the point: the Go test proves the instrument works, and only the evidence files prove it was pointed at a corpus.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestLiteralStyleKeepsIdentifiers` | `cmd/server/eval_test.go` | literal-mode questions really do carry identifiers, so the mode is not paraphrase wearing a label | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| make the literal style fall through to the paraphrase prompt | yes | `TestLiteralStyleKeepsIdentifiers` |
| strip identifiers from the literal prompt template | yes | `TestLiteralStyleKeepsIdentifiers` |

## Out of Scope

- Running the crosslingual, temporal or absent styles (deferred: docs/adr/ADR-001-recall-answers-or-abstains.md and ADR-004 own those; this task needs the two modes the tuner reads)
- Deciding anything from the tables (permanent: T2 owns the decision rule, and a task that both measures and decides is where selection bias enters)

## Invariants

- Both modes use the same corpus, the same `--n` and the same `--pool`, or the comparison is between question sets rather than between rankers.
- No case text or drawer id is committed.

## Risks

- The generator produces weak literal questions and the mode looks useless. Mitigated: step 1's test measures identifier overlap directly, so a weak generator is visible as a generator problem rather than as a retrieval result.

## Stop Condition

Stop and report if the literal mode's retrieval ceiling is far below the paraphrase one — that is a finding about the corpus or the generator and must be understood before any tuner reads either table.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>

## Mutation Log
