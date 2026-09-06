package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/rerank/tei"
)

// Issue #145. Reranking degrades to hybrid order whenever the configured pool
// cannot be scored inside RERANK_TIMEOUT, and the only evidence is a log line on
// the server plus `reranked: false` on every hit afterwards. An operator setting
// RERANK_POOL has no way to ask, before the fact, what their box can afford —
// measured on one llama.cpp deployment at 0.4-0.6s per document, a pool of 10
// worked, 15 sat on the edge, and 25 upward timed out on every search.
//
// This is the pre-flight the other doctor checks already are for the database.

// rerankProbe is one measurement of a live reranker: the two timings it was
// taken from, and the linear model fitted to them.
type rerankProbe struct {
	// Small and Large are the two batch sizes timed, and their wall times.
	Small, Large     int
	SmallAt, LargeAt time.Duration

	// Fixed is the per-CALL cost — connection, request framing, model warm-up —
	// and PerDoc the marginal cost of one more document.
	//
	// ⚠ TWO POINTS, NOT ONE, and the single-point version is wrong in the
	// direction that matters. Dividing one wall time by its document count
	// charges the whole fixed cost to every document, which overstates the
	// marginal cost and so UNDERSTATES the affordable pool — an operator would be
	// told to shrink a pool that was fine. Two points separate the intercept from
	// the slope.
	Fixed, PerDoc time.Duration
}

// affordablePool is the largest pool whose predicted wall time fits within the
// budget, never below zero.
func (p rerankProbe) affordablePool(budget time.Duration) int {
	if p.PerDoc <= 0 {
		// A reranker fast enough that two batch sizes were indistinguishable.
		// Reporting "infinite" would be a measurement claim this cannot support,
		// so it reports the largest batch actually observed to fit.
		if p.LargeAt <= budget {
			return p.Large
		}
		return 0
	}
	n := int((budget - p.Fixed) / p.PerDoc)
	if n < 0 {
		return 0
	}
	return n
}

// probeReranker times two rerank calls against the live service.
//
// The documents are deliberately unremarkable prose of a realistic length: a
// cross-encoder's cost is driven by token count, so timing it on one-word
// documents would measure a workload nobody runs and report a pool nobody can
// afford.
func probeReranker(ctx context.Context, r palace.Reranker, small, large int) (rerankProbe, error) {
	const query = "what does this deployment do when the pool cannot be scored in time"
	doc := strings.TrimSpace(strings.Repeat(
		"a memory of the length this palace actually stores, long enough that the cross-encoder "+
			"is doing the work an ordinary search would ask of it. ", 3))

	timeOne := func(n int) (time.Duration, error) {
		docs := make([]string, n)
		for i := range docs {
			docs[i] = fmt.Sprintf("%d. %s", i, doc)
		}
		start := time.Now()
		if _, err := r.Rerank(ctx, query, docs); err != nil {
			return 0, err
		}
		return time.Since(start), nil
	}

	smallAt, err := timeOne(small)
	if err != nil {
		return rerankProbe{}, fmt.Errorf("rerank %d document(s): %w", small, err)
	}
	largeAt, err := timeOne(large)
	if err != nil {
		return rerankProbe{}, fmt.Errorf("rerank %d documents: %w", large, err)
	}

	p := rerankProbe{Small: small, Large: large, SmallAt: smallAt, LargeAt: largeAt}
	if large > small && largeAt > smallAt {
		p.PerDoc = (largeAt - smallAt) / time.Duration(large-small)
		p.Fixed = smallAt - p.PerDoc*time.Duration(small)
		if p.Fixed < 0 {
			p.Fixed = 0
		}
	}
	return p, nil
}

// doctorRerank probes the configured cross-encoder and reports what the pool in
// force can actually afford.
//
// It exits non-zero on ONE condition: the configured pool is predicted not to fit
// the timeout, which means every search silently returns hybrid order. A missing
// reranker is not a finding — plenty of deployments run without one, and a check
// that went red on a supported configuration would be switched off, taking the
// real verdict with it.
func doctorRerank(ctx context.Context, cfg config.Config, out io.Writer) error {
	if cfg.RerankURL == "" {
		fmt.Fprintln(out, "rerank: no reranker configured (RERANK_URL is empty) — searches return "+
			"fused hybrid order, which is a supported deployment and not a finding")
		return nil
	}
	budget := cfg.RerankTimeout
	if budget <= 0 {
		budget = config.Default().RerankTimeout
	}
	pool := cfg.RerankPool
	if pool <= 0 {
		pool = palace.DefaultRerankPool
	}

	fmt.Fprintf(out, "rerank: probing %s (pool %d, timeout %s)\n", cfg.RerankURL, pool, budget)
	// The client the server itself builds, with the same budget. A probe with its
	// own HTTP settings would measure a deployment nobody runs.
	probe, err := probeReranker(ctx, tei.New(cfg.RerankURL, budget), 1, 8)
	if err != nil {
		// A reranker that cannot answer a probe of EIGHT documents is already
		// failing every search at a pool of ten or fifty, so this is a finding
		// rather than an inconclusive run.
		fmt.Fprintf(out, "  the probe did not complete: %v\n", err)
		return fmt.Errorf("rerank probe failed: %w", err)
	}

	fmt.Fprintf(out, "  %d doc: %s · %d docs: %s → ~%s fixed + ~%s per document\n",
		probe.Small, probe.SmallAt.Round(time.Millisecond),
		probe.Large, probe.LargeAt.Round(time.Millisecond),
		probe.Fixed.Round(time.Millisecond), probe.PerDoc.Round(time.Millisecond))

	affordable := probe.affordablePool(budget)
	fmt.Fprintf(out, "  largest pool that fits %s: %d\n", budget, affordable)

	// ⚠ REORDER-ONLY, and it is reported at every run rather than only when it
	// bites. The retrieve floor is max(limit×3, pool), so a request whose limit
	// reaches the pool is cross-encoding exactly the page it would have returned
	// anyway: latency for a reordering, never for recall.
	fmt.Fprintf(out, "  reorder-only above limit %d: a search asking for that many hits or more "+
		"pays the cross-encoder and gains no candidate it would not have returned\n", pool)

	if affordable < pool {
		return fmt.Errorf("rerank pool %d exceeds what this reranker can score in %s (about %d): "+
			"every search will silently fall back to hybrid order and report reranked=false",
			pool, budget, affordable)
	}
	fmt.Fprintf(out, "  pool %d fits, with %s to spare\n", pool,
		(budget - (probe.Fixed + probe.PerDoc*time.Duration(pool))).Round(time.Millisecond))
	return nil
}
