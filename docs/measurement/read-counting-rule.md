# The read counting rule

> **Owner:** ADR-044 (make a small read trustworthy) · **Fact:** F-5
> **Status:** active — this file's content IS its identity; see *Changing this rule* below

Every baseline in `baselines/` cites this file by the SHA-256 of its normalized content. That
citation is the rule's identity: a baseline that does not carry one, or carries one that no longer
matches, is not a baseline anyone may quote a rate from.

## The quantity

**A read acted on without a second call.**

A recall is counted when it is LOGGED — a `search_events` row exists for it. It is counted as *acted
on without a second call* when no `drawer_fetches` row names its `search_id`. A recall followed by
one or more fetches needed a second call and is counted the other way.

    acted_on_without_a_second_call = recalls_logged − recalls_fetched

`recalls_fetched` is the count of DISTINCT `search_id` values in `drawer_fetches`, which is what
`palace.Service.CountFetches` already returns.

## The window

A rate is only ever quoted for an explicit window, and the window is part of the figure. A baseline
records the window's start and end in UTC, and the number of logged recalls in it.

## What does NOT count

- **Read FREQUENCY.** How often an agent reaches for the palace is a different quantity, it is what
  ADR-041's instrument already counts, and it is not what F-1, F-2 and F-7 change. A mechanism that
  made every hit trustworthy could leave frequency completely unmoved, so a frequency rule would
  repeat ADR-041's failure in a new location — counting the quantity that is easy to count rather
  than the one being claimed.
- **An unlogged recall.** `SkipTelemetry` means some recalls write no `search_events` row at all.
  They are outside the denominator, and a baseline says so rather than pretending the denominator is
  every recall that happened.
- **A fetch naming a recall this server never logged.** The `search_id` on a fetch is client-supplied
  and shape-checked, not a foreign key. Such a row is a finding in its own right; it is not evidence
  that a logged recall needed a second call.
- **Reads that are not recalls.** `am_get_drawer` called without a `search_id`, `am_list_drawers`,
  `am_bootstrap`. The quantity is about whether a RECALL answered, not about read volume.

## Why the complement rather than the ratio

ADR-028 T4 owns "the fetch ratio reported with its population". This rule deliberately does not
restate it: a recall followed by a fetch is a read that needed a second call, so the quantity here is
that ratio's complement. When T4 lands, a baseline consumes its reported figure. Until then a
baseline computes the complement from raw `drawer_fetches` rows and says which half it used.

## Changing this rule

Any change to this file's content changes its digest and therefore **invalidates every baseline that
cites the old one** (ADR-044 F-6). That is the intended behaviour, not an inconvenience: a rate
quoted across a rule change compares two different quantities, which is the defect the digest exists
to make impossible. Re-collect rather than re-cite.

The digest covers this file's content with line endings normalized to `\n`, trailing whitespace
stripped from each line, and exactly one trailing newline — so a reformat that changes no words does
not invalidate anything, and a change of words always does.
