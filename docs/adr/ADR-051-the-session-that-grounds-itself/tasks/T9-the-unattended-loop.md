# Task ADR-051-T9: The unattended loop — what runs alone, and what still gates

**Depends-on:** T6
**Covers:** none — no spec
**Estimated scope:** M (permission rules, a headless entry point, a persist gate)
**Owner:** unassigned
**Produces:** none
**Consumes:** `one installable unit` (T6)
**Data dependency:** hermetic

## Goal

Make a session nobody is watching leave the palace better than it found it, and write down —
as rules a machine reads, not as judgement an agent exercises mid-run — which actions it may
take alone.

## The line, stated before the code

**Where a session would stop to ask a human about RECALL, it consults the palace instead.**
Why the code is shaped this way, what was tried, what a previous session got wrong, which wing
to write to: every one of those has an answer already recorded, and stopping to ask is the
failure mode rather than the safeguard. That is this project's entire thesis applied to its own
operation.

⚠ **That does not extend to consent for irreversible or outward-facing actions** — a
force-push, a release, a destructive migration, anything published outward. The palace records
what was decided; it cannot consent on a human's behalf to something nobody has decided yet. So
those gate, and they gate in `permissions` rules rather than in an agent's judgement, because a
rule can be reviewed and a judgement cannot. **If the owner wants that line moved it moves in
one file with one review, which is the reason for writing it down rather than the reason for
the line.**

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/.claude-plugin/settings.json` | add | `permissions` allow/deny rules shipped with the plugin |
| `clients/claude-code/unattended.go` | add | the headless entry point |
| `clients/claude-code/hooks/agentsmemory-stop-hook.sh` | edit | the persist gate, using T3's touched-path record |

## Ordered Steps

1. Write the failing tests first (TDD red).
2. Write the `permissions` rules: allow the read and recall surface outright; deny the
   irreversible set by name. **The deny list is the deliverable** — an allow-everything rule
   with a comment is not a gate.
3. Add the headless entry point: `claude -p` with `--output-format json`, a permission mode,
   and the plugin loaded.
4. Strengthen the Stop hook: if the session touched files and filed nothing, say so by exiting
   2, which is the one channel Stop has. Today it nudges; with T3's record it can name what
   went unrecorded.
5. Run the fence, the mutants, the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ \
  -run 'TestTheDenyListNamesTheIrreversibleActions|TestTheAllowListDoesNotAllowEverything|TestTheStopGateFiresWhenWorkWentUnrecorded|TestTheStopGateIsSilentWhenNothingWasTouched' \
  -count=1 2>&1 | tee /tmp/adr051-t9.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t9.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheDenyListNamesTheIrreversibleActions` | `clients/claude-code/unattended_test.go` | Force-push, release and destructive migration are each denied by name — the assertion that fails if the list is quietly emptied | — |
| `TestTheAllowListDoesNotAllowEverything` | `clients/claude-code/unattended_test.go` | A wildcard that would admit the denied set is refused. A permissive rule that passes the test above while allowing everything is the shape this gate exists to catch | — |
| `TestTheStopGateFiresWhenWorkWentUnrecorded` | `clients/claude-code/unattended_test.go` | Touched paths present, nothing filed → exit 2 and the paths are named | — |
| `TestTheStopGateIsSilentWhenNothingWasTouched` | `clients/claude-code/unattended_test.go` | A read-only session ends quietly. A gate that fires on every session is one an operator disables | — |

## Reachability

⚠ **A permission rule is the easiest thing in this record to ship inert.** It parses, it
installs, and nothing proves it ever refused anything — the green-suite-over-a-dead-mechanism
shape §Reachability records four times. `TestTheAllowListDoesNotAllowEverything` is the half
that matters: it drives the real rule set and asserts the denied actions are still denied,
rather than asserting the file contains some strings.

## Mutation Log

Filled by `adr-verify --mutant`. At minimum: the deny list emptied, and a wildcard added to
the allow list — the second must be killed by a different test than the first, or one of them
is not pulling its weight.

## Invariants

- Recall never asks a human.
- Irreversible and outward-facing actions always gate.
- The Stop gate is silent on a session that touched nothing.

## Risks

A deny list is a list kept beside a truth, and this repository's own rule is that such a list
goes stale. It is accepted here because the alternative — inferring irreversibility at runtime
— is a guess wearing a check, and because the list is short and reviewed.

## Stop Condition

Stop and raise it if the Stop gate fires on sessions that legitimately had nothing to file. A
gate an operator learns to ignore is worse than no gate, and that is the failure this task is
most likely to produce.

## Out of Scope

- MCP elicitation. (deferred: `docs/adr/ADR-051-the-session-that-grounds-itself.md` §Follow-ups — it is a human-in-the-loop primitive, and the human in the loop is what this task removes; it returns only for the irreversible set)
- Filing memories without an agent deciding what is worth filing. (permanent: boundary: the persist gate reports what went unrecorded; choosing what deserves a memory is a judgement this record does not automate)

## Verification Log

Filled by `adr-verify`.
