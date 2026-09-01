// Package longmemeval measures how a memory is WRITTEN and how it is ASKED FOR,
// against a corpus this project did not author.
//
// It is deliberately not part of internal/palace's eval, and the split is the
// point rather than a packaging preference. That eval scores ranked lists by
// MRR over cases generated from our own drawers, and both halves of that make it
// blind to this question. Its corpus cannot disagree with us — ADR-032 measured
// vector-only as the best arm on one question set and the worst on another, same
// palace, same commit, because the paraphrase questions were written by a model
// FROM the drawers they had to find. And its metric cannot lose for verbatim
// text: raw text is a superset of any summary of it, so under MRR every writing
// rule that compresses, splits or re-titles is scored by an instrument that has
// already decided against it. docs/adr/BACKLOG.md records that as a standing
// finding and prescribes the remedy this package implements — when a claim does
// not fit the instrument, extend the instrument.
//
// So the unit here is a CELL: one write policy crossed with one query policy,
// scored by whether a reader answered the question correctly from what recall
// returned, inside a token budget every cell shares. The shared budget is what
// makes it a metric a superset cannot automatically win. ADR-047 carries the
// decision, the pre-registered rule for promoting a policy into the centralised
// skills, and what would falsify it.
package longmemeval
