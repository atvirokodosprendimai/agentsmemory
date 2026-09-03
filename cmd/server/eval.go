// eval.go implements `agentsmemory eval`: does recall actually return the memory
// that answers the question, and does each ranking stage earn its place?
//
// The corpus is the team's own memory, and the questions are generated from it —
// for each sampled drawer, a local model writes a question that drawer answers,
// and retrieval is scored on whether it comes back. That makes labelling free,
// which is the only reason an eval like this gets run more than once.
//
// Two honesty constraints shape it:
//
//   - The generator is told to PARAPHRASE and avoid the drawer's distinctive
//     terms. Without that, the questions inherit the drawer's vocabulary, BM25
//     scores its own homework, and every arm looks excellent.
//   - Absolute numbers from generated questions mean little. The DELTAS between
//     arms are the output worth acting on, which is why the table is the point
//     and a single headline number is not offered.
//
// Generated cases are written to (and re-read from) a file so a ranking change
// can be compared against the same questions rather than a fresh sample.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/gen"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/rerank/tei"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/urfave/cli/v3"
)

// evalCommand builds the `eval` subcommand.
func evalCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "eval",
		Usage: "measure retrieval quality and compare ranking arms on this palace",
		Description: "Samples drawers, has a local model write a question each one answers, then\n" +
			"scores every ranking arm on whether that drawer comes back:\n\n" +
			"  vector                 nearest neighbour only — the baseline to beat\n" +
			"  hybrid                 + Okapi-BM25 fusion\n" +
			"  hybrid+closet          + the closet boost\n" +
			"  hybrid+rerank          + the cross-encoder, no closet boost (only when RERANK_URL is set)\n" +
			"  hybrid+closet+rerank   + both (only when RERANK_URL is set)\n\n" +
			"An arm carries the closet prior only if its name says so. Every other arm —\n" +
			"rrf, the fusion sweeps, the rerank blends — is measured without it.\n\n" +
			"Read the DELTAS, not the absolute numbers: questions generated from a drawer\n" +
			"share vocabulary with it, which flatters every arm equally.\n\n" +
			"  agentsmemory eval --wing wing_acme --n 40 --cases /data/eval.jsonl\n" +
			"  agentsmemory eval --cases /data/eval.jsonl        # re-run the same questions\n" +
			"  agentsmemory eval --project acme --wing wing_acme # a multi-tenant database",
		Flags: append(serveFlags(def),
			projectFlag(),
			&cli.StringFlag{Name: "wing", Usage: "sample drawers from this wing only"},
			&cli.IntFlag{Name: "n", Value: 30, Usage: "how many drawers to sample when generating cases"},
			&cli.BoolFlag{Name: "calibrate", Usage: "derive the abstention thresholds from this run and write a calibration file. Requires a set holding BOTH answerable and verified-absent cases — the separation cannot be computed from one alone"},
			&cli.StringFlag{Name: "calibration-out", Value: "calibration.json", Usage: "where --calibrate writes the calibration file. The tool writes a FILE, never configuration: an operator still has to point the server at it, so a bad calibration cannot become production behaviour by itself"},
			&cli.BoolFlag{Name: "gate", Usage: "exit non-zero unless the 90% Wilson LOWER BOUND on the correct-refusal rate clears --refusal-bar. The bound, not the point estimate: at these sample sizes the estimate carries about +/-0.11 at one standard error, so a gate on it passes on noise"},
			&cli.FloatFlag{Name: "answer-recall", Value: 0.95, Usage: "answer-recall target the higher threshold must hold over REACHABLE-answerable cases"},
			&cli.IntFlag{Name: "refuse-allowance", Value: 1, Usage: "how many reachable-answerable cases may fall below the lower threshold. A COUNT, not a rate: at these sample sizes the achievable recall grid is coarse enough that two rate targets pick the same threshold and the band collapses"},
			&cli.FloatFlag{Name: "refusal-bar", Value: 0.30, Usage: "the correct-refusal rate --gate compares its lower bound against"},
			&cli.StringFlag{Name: "rerank-model", Usage: "operator-declared label for the reranker in force, recorded in the calibration. Not detected: Reranker.Rerank returns floats and does not report what produced them"},
			&cli.StringFlag{Name: "cases", Usage: "read cases from this JSONL file if it exists, otherwise write the generated ones there. Several comma-separated files are merged, which is how answerable and unanswerable questions get scored in one run — the only way the distance separation can be computed"},
			&cli.StringFlag{Name: "gen-model", Sources: cli.EnvVars("EVAL_GEN_MODEL"), Value: "qwen2.5-coder:7b", Usage: "model that writes the questions (must be GENERATIVE — an embedder like bge-m3 cannot answer /api/generate)"},
			&cli.StringFlag{Name: "gen-url", Sources: cli.EnvVars("EVAL_GEN_URL"), Usage: "where the question generator runs; defaults to --ollama-url. It is called as Ollama's own /api/generate — a URL containing /v1 changes the failure hint only, NOT the request, so an OpenAI-compatible endpoint is not supported here"},
			&cli.StringFlag{Name: "gen-api-key", Sources: cli.EnvVars("EVAL_GEN_API_KEY"), Usage: "bearer token for --gen-url; required by hosted providers, ignored by a local one"},
			&cli.IntFlag{Name: "pool", Value: defaultEvalPool, Usage: "candidates fetched per query; every arm re-orders this same pool"},
			&cli.BoolFlag{Name: "supersession-gate", Usage: "decide whether the supersession failure is common enough to justify a mechanism against it, from this run's temporal cases. Refuses rather than answering when the evidence is too thin, unhardened, or missing the pre-registered arm"},
			&cli.Float64Flag{Name: "pair-max-distance", Value: 0.55, Usage: "how close a temporal pair must be before it is offered to the judge (cosine distance; 0 disables the ceiling). Without it, 'nearest older neighbour' is a claim about how sparse the wing is rather than about the two memories"},
			&cli.StringFlag{Name: "arms", Usage: "run only these arms, comma-separated, matched by exact name or PREFIX (e.g. \"vector,hybrid,rerank blend\" — the last selects the whole weight sweep). Empty runs every arm. The retrieved pool is unchanged, so a filtered run stays comparable arm-for-arm with a full one; what changes is that 'vs best' is the best of the arms PRESENT, and the report says so. A filter matching nothing REFUSES rather than printing an empty table"},
			&cli.BoolFlag{Name: "contextual", Usage: "also score a contextual-chunk index: each chunk re-embedded with a little of its parent's context, built into a scratch namespace"},
			&cli.BoolFlag{Name: "allow-degraded", Usage: "run without the reranked arms when the configured reranker fails its preflight, instead of refusing the run. The arms are DROPPED and a warning is recorded — they are never silently scored as plain hybrid, which is what produced one wrong table already"},
			&cli.IntFlag{Name: "contextual-limit", Value: palace.DefaultContextualLimit, Usage: "how many chunks the contextual experiment covers — it costs an embedding pass and a second copy of those vectors, so it is capped rather than corpus-wide"},
			&cli.BoolFlag{Name: "drop-contextual", Usage: "delete the contextual experiment's vectors and exit"},
			&cli.StringFlag{Name: "style", Value: "paraphrase", Usage: "question style: paraphrase (no shared vocabulary), literal (keeps identifiers, like a real developer search), crosslingual (asks in the other language), temporal (asks for the current state of a fact an older memory still contradicts), absent (HARD negatives: questions the palace should NOT answer, keeping the note's identifiers so the negative is a real near-miss), absent-easy (the pre-2026-08-21 generator, which strips identifiers — kept so old case files replay), or real (replay recorded searches, gold judged by the generator model)"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return runEval(ctx, c, def, os.Stdout)
		},
	}
}

// runEval is the whole flow: load or generate cases, score the arms, print.
func runEval(ctx context.Context, c *cli.Command, def config.Config, out io.Writer) error {
	cfg := configFromCmd(c, def)
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	// Named like every other database-level subcommand (wing, inspect, share,
	// plan) rather than resolved through EnsureLocalWorkspace: that helper's
	// refusal to touch a workspace it did not provision guards an UNAUTHENTICATED
	// /mcp, and applying it here made eval the one command that could not measure
	// a multi-tenant palace at all — a database holding any workspace not slugged
	// "local" (the seeded demo team is enough) failed before reading a drawer.
	// Possessing the file is the authorization here, exactly as it already is for
	// `wing export` and `inspect`.
	team, err := resolveProject(ctx, svc, c.String("project"))
	if err != nil {
		return err
	}

	src, err := loadOrGenerateCases(ctx, c, svc, team, out)
	if err != nil {
		return err
	}
	cases, runMeta := src.Cases, src.Meta
	if len(cases) == 0 {
		return fmt.Errorf("no eval cases: the wing has no drawers, or the generator produced nothing usable")
	}

	fmt.Fprintf(out, "\n%d case(s) %s, style %s, pool %d, corpus %s, case set %s (%s)\n\n",
		len(cases), src.Label, c.String("style"), c.Int("pool"), corpusLabel(c.String("wing")),
		palace.CaseSetID(cases), src.Origin)
	// Every arm runs per case, and a reranked arm is real inference, so a case can
	// take seconds. Report each one as it lands: silence for minutes reads as a
	// hang, and the first version of this command was exactly that.
	if c.Bool("drop-contextual") {
		n, err := svc.drawers.DropContextualIndex(ctx, team.ID, c.Int("contextual-limit"))
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "dropped the contextual experiment's vectors (%d point(s))\n", n)
		return nil
	}

	if c.Bool("contextual") {
		// Built here rather than lazily inside the arm: it is an embedding pass
		// over the whole corpus and the operator should see it happen, and pay
		// for it once rather than per case.
		fmt.Fprintf(out, "building the contextual index — one embedding pass over up to %d chunk(s), written beside the real vectors; `eval --drop-contextual` gives the space back\n",
			c.Int("contextual-limit"))
		started := time.Now()
		n, err := svc.drawers.BuildContextualIndex(ctx, team.ID, 32, c.Int("contextual-limit"))
		if err != nil {
			return fmt.Errorf("build contextual index: %w", err)
		}
		fmt.Fprintf(out, "  embedded %d chunk(s) with context in %s\n", n, time.Since(started).Round(time.Second))
	}

	report, err := svc.drawers.EvaluateWith(ctx, team.ID, cases, c.Int("pool"),
		palace.EvalOptions{Contextual: c.Bool("contextual"), AllowDegraded: c.Bool("allow-degraded"), CaseSetOrigin: src.Origin, Arms: splitArms(c.String("arms"))},
		func(done, total int, query string, elapsed time.Duration) {
			fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  %s\n", done, total, elapsed.Seconds(), firstLineOf(query, 62))
		})
	if err != nil {
		return err
	}
	printEvalTable(out, report)
	printClosetBlock(out, report)
	palace.PrintSupersessionTable(out, report)
	if c.Bool("supersession-gate") {
		if err := printSupersessionGate(out, report, runMeta, svc.drawers.SupersessionGatedArmFor(), c.Int("pool")); err != nil {
			fmt.Fprintf(out, "\nsupersession gate: REFUSED — %v\n", err)
		}
	}

	// Deliberately AFTER the tables and BEFORE the results file: the curve reads
	// the same rows the tables were printed from, and a failing --gate must still
	// leave the numbers on screen that explain why it failed.
	calErr := runCalibration(ctx, c, cfg, report, cases, src.Styles, out)

	// The full result goes to disk: per-case ranks per arm, warnings, config.
	// The printed table is a VIEW of this file, not the record — a run that only
	// exists in scrollback cannot be compared with the next one, and comparing
	// runs is the entire reason cases are saved.
	if resPath := resultsPath(c.String("cases")); resPath != "" {
		if err := writeResults(resPath, c, report, cases); err != nil {
			fmt.Fprintf(out, "  (could not save the results file: %v)\n", err)
		} else {
			fmt.Fprintf(out, "full results (per-case ranks, config, warnings): %s\n", resPath)
		}
	}

	// The run record is the file the ADR's evidence directory holds. It is
	// separate from the results file because that one carries the queries, and
	// the evidence directory is committed while the palace it measures is not.
	if cPath := cellsPath(c.String("cases")); cPath != "" {
		rec := cellsConfig{
			Pool: c.Int("pool"), Cases: len(cases),
			ClosetScale: cfg.ClosetBoost, BM25Weight: cfg.BM25Weight,
			RerankConfigured: cfg.RerankURL != "", RerankWeight: cfg.RerankWeight, RerankPool: cfg.RerankPool,
			Ranking: svc.drawers.RankingProfile(),
		}
		if err := writeCells(cPath, report, runMeta, rec); err != nil {
			fmt.Fprintf(out, "  (could not save the run record: %v)\n", err)
		} else {
			fmt.Fprintf(out, "run record (commit, ranking config, closet cells; no case text): %s\n", cPath)
		}
	}
	return calErr
}

// resultsPath derives where the results artifact lands: beside the first case
// file, named after it.
func resultsPath(casesFlag string) string {
	first := strings.TrimSpace(strings.Split(casesFlag, ",")[0])
	if first == "" {
		return ""
	}
	return strings.TrimSuffix(first, ".jsonl") + ".results.json"
}

// writeResults persists the run in full.
func writeResults(path string, c *cli.Command, report palace.EvalReport, cases []palace.EvalCase) error {
	type armOut struct {
		Arm      string  `json:"arm"`
		MRR      float64 `json:"mrr"`
		CILo     float64 `json:"ci_lo"`
		CIHi     float64 `json:"ci_hi"`
		Recall1  int     `json:"recall1"`
		Recall5  int     `json:"recall5"`
		NotFound int     `json:"not_found"`
		Ranks    []int   `json:"ranks"`
	}
	arms := make([]armOut, 0, len(report.Arms))
	for _, m := range report.Arms {
		ci := palace.BootstrapMRR(m.Ranks)
		arms = append(arms, armOut{Arm: string(m.Arm), MRR: m.MRR, CILo: ci.Lo, CIHi: ci.Hi,
			Recall1: m.Recall1, Recall5: m.Recall5, NotFound: m.NotFound, Ranks: m.Ranks})
	}
	payload := map[string]any{
		"created":  time.Now().UTC().Format(time.RFC3339),
		"pool":     c.Int("pool"),
		"wing":     c.String("wing"),
		"cases":    len(cases),
		"warnings": report.Warnings,
		"arms":     arms,
		"details":  report.Details,
	}
	raw, err := json.MarshalIndent(payload, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// loadOrGenerateCases reads a case file when one exists, and otherwise samples
// drawers and generates questions — writing them out so the next run compares
// like with like.

// generateTemporalCases builds the CatTemporal case set. Each case pairs a dated
// drawer with its nearest semantic neighbour whose content date is strictly
// older (OlderNeighbor): the newer drawer is the expected answer and the older
// one is RECORDED on the case as its Distractor, because the metric scores where
// the superseded version landed, not only where the gold did. Pair discovery runs
// BEFORE question generation so no LLM round trip is spent on a drawer with
// nothing to supersede.
//
// ⚠This comment used to say the older drawer "needs no field of its own — it
// stays in the corpus as the distractor". That sentence was the bug: the pair was
// verified, counted, and then thrown away unrecorded, so every consumer of
// Distractor read an empty set and the supersession gate could only ever refuse.
// It is written out here rather than deleted because it is the reasoning a future
// reader would otherwise reconstruct, and it is wrong.
//
// The returned caseFileMeta carries the pair record — how many candidates were
// found, how many a judge confirmed, and which model judged them. It is what the
// supersession gate checks its first precondition against, so it travels with the
// cases instead of only reaching disk.
func generateTemporalCases(ctx context.Context, c *cli.Command, svc *services, team tenant.Team, out io.Writer) ([]palace.EvalCase, string, caseFileMeta, error) {
	wing := c.String("wing")
	drawers, err := svc.drawers.DatedDrawers(ctx, team.ID, wing, c.Int("n"))
	if err != nil {
		return nil, "", caseFileMeta{}, fmt.Errorf("list dated drawers: %w", err)
	}
	if len(drawers) == 0 {
		// The corpus may well have drawers — just none whose chronology is known,
		// and without a date "was later corrected" cannot be labelled. Name that
		// distinctly so the reader fixes the dates, not the wing.
		return nil, "", caseFileMeta{}, fmt.Errorf("no drawers with a content date in %s of workspace %q — temporal cases need dated memories (a date in the source name, frontmatter, or first lines), so file some or widen --wing",
			corpusLabel(wing), team.Slug)
	}

	gen := &questionGen{
		url:     genURL(c),
		model:   c.String("gen-model"),
		apiKey:  strings.TrimSpace(c.String("gen-api-key")),
		prompt:  evalPromptTemporal,
		http:    &http.Client{Timeout: 120 * time.Second},
		verbose: out,
	}
	fmt.Fprintf(out, "generating temporal questions with %s (%d dated drawers)…\n", gen.model, len(drawers))
	genStart := time.Now()
	var cases []palace.EvalCase
	// One successful generation proves the generator is configured; until then a
	// failure means misconfiguration, mirroring the first-drawer abort of the
	// main loop. It cannot key off the loop index here, because pair discovery
	// may legitimately skip any number of drawers before the first ask happens.
	proven := false
	var pairCandidates, verifiedPairs int
	for i, d := range drawers {
		started := time.Now()
		older, ok, err := svc.drawers.OlderNeighbor(ctx, team.ID, d, c.Int("pool"), c.Float64("pair-max-distance"))
		if err != nil {
			// Pair discovery uses the embedder and the vector store — the same
			// dependencies the eval itself cannot run without — so a failure here
			// is a broken setup, not this drawer's fault. Abort rather than skip.
			return nil, "", caseFileMeta{}, fmt.Errorf("find older neighbour: %w", err)
		}
		if !ok {
			fmt.Fprintf(out, "  [%2d/%2d] skipped: no dated older neighbour for %q (%s) — nothing in the corpus it supersedes\n",
				i+1, len(drawers), firstLineOf(d.Content, 40), d.ContentDate)
			continue
		}
		pairCandidates++
		// Distance says the two are close; only the judge can say the older one
		// records an EARLIER STATE of the same fact rather than a different fact
		// nearby. A pair it declines is not a temporal case, and an error is a
		// drop rather than a pass — an unverified pair that looks verified is
		// what makes the whole file unusable.
		switch confirmed, err := verifyPair(ctx, d, older, gen); {
		case err != nil:
			return nil, "", caseFileMeta{}, fmt.Errorf("verify temporal pair: %w", err)
		case !confirmed:
			fmt.Fprintf(out, "  [%2d/%2d] skipped: the judge does not read %q as superseding its neighbour\n",
				i+1, len(drawers), firstLineOf(d.Content, 40))
			continue
		}
		verifiedPairs++
		q, err := gen.ask(ctx, d.Content)
		if err != nil {
			if !proven {
				return nil, "", caseFileMeta{}, fmt.Errorf("question generator failed on the first pair, so it is misconfigured rather than unlucky: %w\n"+
					"  check `ollama list` — the model must be a GENERATIVE one (an embedder such as bge-m3 cannot answer /api/generate),\n"+
					"  pull it with `ollama pull %s`, or name one you already have with --gen-model", err, gen.model)
			}
			fmt.Fprintf(out, "  [%2d/%2d] failed: %v\n", i+1, len(drawers), err)
			continue
		}
		if q == "" {
			fmt.Fprintf(out, "  [%2d/%2d] empty answer, skipped\n", i+1, len(drawers))
			continue
		}
		proven = true
		// The pair's dates ride the progress line so an operator can sanity-check
		// the labelling as it happens — a "supersedes" whose dates look wrong is a
		// bad pair, and this line is the only place it is visible.
		fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  %s  (%s supersedes %s)\n",
			i+1, len(drawers), time.Since(started).Seconds(), firstLineOf(q, 62), d.ContentDate, older.ContentDate)
		cases = append(cases, temporalCase(q, wing, d, older))
	}
	if len(cases) == 0 {
		// Distinct from the generic "no eval cases": every sampled drawer was
		// dated, yet none had a dated older neighbour — the corpus has chronology
		// but no supersession to measure, which no --wing change will fix.
		return nil, "", caseFileMeta{}, fmt.Errorf("sampled %d dated drawer(s) but none has a dated older semantic neighbour — temporal cases need at least two dated memories about the same fact; file corrections with dates, or run another --style", len(drawers))
	}
	fmt.Fprintf(out, "generated %d case(s) in %s\n", len(cases), time.Since(genStart).Round(time.Second))
	// Built here and RETURNED, rather than built inside the --cases branch and
	// rebuilt from flags by the caller. The pair record is this run's evidence
	// that a judge confirmed its pairs, and a run that gates without saving a
	// file holds exactly the same evidence as one that saves it. While this was
	// written only to disk, `--style temporal --supersession-gate` in one command
	// refused on a file it had just written with five verified pairs in it, and
	// the refusal advised regenerating with the flag the operator had just used.
	meta := caseFileMeta{
		Generator: gen.model, Style: "temporal", Wing: wing,
		Corpus: len(drawers), Created: time.Now().UTC().Format(time.RFC3339),
		PairCandidates: pairCandidates,
		VerifiedPairs:  verifiedPairs,
		// gen.model, not the flag: the judge is the thing that actually ran, and
		// verifyPair judged with this generator.
		Judge: gen.model,
	}
	if path := c.String("cases"); path != "" {
		if err := writeCases(path, cases, meta); err != nil {
			fmt.Fprintf(out, "  (could not save cases to %s: %v)\n", path, err)
		} else {
			fmt.Fprintf(out, "saved %d case(s) to %s\n", len(cases), path)
		}
	}
	return cases, "generated", meta, nil
}

// generateRealCases replays queries agents actually ran (from search_events)
// as eval cases. There is no seed note to serve as gold, so the gold is a
// judged SET: the broad candidate pool is scored by the generator model as a
// relevance judge, and every accepted memory counts as a correct answer. Two
// of the review's findings meet their fix here — real queries were never
// phrased to suit any arm's feature (the generated styles all are), and a
// multi-member gold stops a valid alternative answer being scored as an error.
//
// The judge is an LLM, not a human, and the pool comes from vector retrieval,
// so a memory no arm can retrieve is invisible to these qrels. Both limits are
// recorded in the case-file provenance rather than papered over.
func generateRealCases(ctx context.Context, c *cli.Command, svc *services, team tenant.Team, out io.Writer) ([]palace.EvalCase, string, error) {
	wing := c.String("wing")
	queries, err := svc.drawers.SampleSearchQueries(ctx, team.ID, wing, c.Int("n"))
	if err != nil {
		return nil, "", fmt.Errorf("sample real queries: %w", err)
	}
	if len(queries) == 0 {
		return nil, "", fmt.Errorf("no recorded searches to replay in %s of workspace %q — real-query cases need search telemetry; run some sessions against this palace first, or use a generated --style",
			corpusLabel(wing), team.Slug)
	}
	judge := &questionGen{
		url:    genURL(c),
		model:  c.String("gen-model"),
		apiKey: strings.TrimSpace(c.String("gen-api-key")),
		prompt: evalPromptRelevanceCheck,
		http:   &http.Client{Timeout: 120 * time.Second},
	}
	fmt.Fprintf(out, "judging %d real queries with %s…\n", len(queries), judge.model)
	var cases []palace.EvalCase
	for i, q := range queries {
		started := time.Now()
		// The judged set is the UNION of what several rankers would surface, not
		// what production returns. Judging production's own page would bake the
		// current ranker's blind spots into the labels: a memory it never
		// surfaces could never be marked relevant, so a better ranker that does
		// surface it would earn nothing, and the case set could only ever
		// confirm the ranker that produced it. Pooling competing systems and
		// judging the union blind is how relevance judgments have been built
		// since TREC, and it is what lets these cases SELECT a ranker rather
		// than ratify one.
		hits, err := svc.drawers.CandidateUnion(ctx, team.ID, q, wing, 5, 50)
		if err != nil {
			return nil, "", fmt.Errorf("pool real query %q: %w", q, err)
		}
		var relevant []string
		for _, h := range hits {
			excerpt := palace.Snippet(h.Content, q, 900)
			reply, jerr := judge.ask(ctx, "QUERY: "+q+"\n\nNOTE:\n"+excerpt)
			if jerr != nil {
				// The FIRST failure aborts, matching the generator preflight
				// doctrine: a judge that cannot score one note is misconfigured,
				// and every later call would fail the same way.
				if i == 0 && len(relevant) == 0 {
					return nil, "", fmt.Errorf("the relevance judge is not usable: %w\n\n%s", jerr, judge.hint(ctx))
				}
				fmt.Fprintf(out, "  [%2d/%2d] judge failed on one note, skipped it: %v\n", i+1, len(queries), jerr)
				continue
			}
			if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(reply)), "YES") {
				relevant = append(relevant, h.ID)
			}
		}
		if len(relevant) == 0 {
			fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  no relevant memory judged for: %s\n", i+1, len(queries), time.Since(started).Seconds(), firstLineOf(q, 50))
			continue
		}
		fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  %d relevant  %s\n", i+1, len(queries), time.Since(started).Seconds(), len(relevant), firstLineOf(q, 50))
		cases = append(cases, palace.EvalCase{Query: q, ExpectAny: relevant, Wing: wing, Category: palace.CatReal})
	}
	if len(cases) == 0 {
		return nil, "", fmt.Errorf("none of the %d replayed queries had a judged-relevant memory — either the palace genuinely cannot answer its own traffic (check the recall stats) or the judge model is refusing everything", len(queries))
	}
	if path := c.String("cases"); path != "" {
		meta := caseFileMeta{
			Generator: judge.model, Style: "real", Wing: wing,
			Corpus: len(queries), Created: time.Now().UTC().Format(time.RFC3339),
		}
		if err := writeCases(path, cases, meta); err != nil {
			fmt.Fprintf(out, "  (could not save cases to %s: %v)\n", path, err)
		} else {
			fmt.Fprintf(out, "saved %d case(s) to %s\n", len(cases), path)
		}
	}
	return cases, "replayed from telemetry", nil
}

// verifyAbsent checks a generated "absent" question against the whole corpus,
// not just the note it was seeded from. The generator only promises the seed
// note cannot answer it; if any other memory can, the case would score a
// correct retrieval as a false positive, and every gate calibrated on such
// cases would be calibrated on invalid labels. Returns the id of a drawer that
// answers the question, or "" when the top hits all fail the answer check.

// evalPromptPairCheck asks the judge the one question distance cannot answer.
//
// Not "are these related" — nearby memories are related all the time. The
// question is whether the older note records an EARLIER STATE of the same fact
// the newer one now states differently, which is what makes returning the older
// one a supersession failure rather than an ordinary irrelevance.
const evalPromptPairCheck = `You are checking whether two notes describe the same fact at two points in time.

Answer YES only if the OLDER note states something about the same specific subject that the NEWER note now states DIFFERENTLY — that is, the newer note supersedes or corrects it.

Answer NO if they are about different subjects, if the older note is merely related, or if both can be true at once.

Reply with YES or NO and nothing else.`

// verifyPair reports whether the judge confirms older records an earlier state
// of the fact newer corrects.
//
// An error is a DROP, not a pass. verifyAbsent learned this the expensive way: a
// checker that cannot answer and returns "fine" fills the case file with
// unverified cases indistinguishable from verified ones, and every number taken
// from that file is then a claim nobody can check.
func verifyPair(ctx context.Context, newer, older palace.Drawer, gen *questionGen) (bool, error) {
	judge := &questionGen{
		url: gen.url, model: gen.model, apiKey: gen.apiKey,
		prompt: evalPromptPairCheck, http: gen.http,
	}
	const excerpt = 900
	reply, err := judge.ask(ctx,
		"NEWER NOTE ("+newer.ContentDate+"):\n"+palace.Snippet(newer.Content, "", excerpt)+
			"\n\nOLDER NOTE ("+older.ContentDate+"):\n"+palace.Snippet(older.Content, "", excerpt))
	if err != nil {
		return false, fmt.Errorf("pair check: %w", err)
	}
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(reply)), "YES"), nil
}

// absentVerifyDepth is how deep the absence check looks before concluding that
// nothing in the palace answers a question.
//
// Chosen from the retrieval ceiling of THIS corpus, re-measured 2026-08-21 on the
// 449-memory palace with n=40 paraphrase questions: gold in top-1 67.5%, top-5
// 82.5%, top-10 92.5%, top-20 95.0%, top-50 97.5%, and none never retrieved. At
// depth 20 the check covers 38 of 40; depth 50 covers 39. At n=40 those differ by
// ONE case, so the extra 30 searches per candidate buy noise.
//
// The previous value was 3, while the ADR claimed a corpus-wide check. The
// justification written for it cited top-20 98% on a ~5,020-drawer mined corpus
// that was reset on 2026-08-19 — figures describing a palace that no longer
// exists. This constant now cites the corpus it is actually chosen for, with its
// date and its n.
//
// It is NOT a corpus-wide proof and must not be read as one: 5% of answerable
// golds sit below depth 20 here. A memory the dense channel never surfaces is one
// recall could not have returned either, so the check matches what the system can
// actually do rather than what the store contains.
const absentVerifyDepth = 20

func verifyAbsent(ctx context.Context, svc *services, teamID, wing, question string, gen *questionGen) (string, error) {
	// No distance gate: absence must be checked broadly. A hit the production
	// gate would drop can still prove the knowledge exists in the palace.
	hits, err := svc.drawers.Search(ctx, teamID, palace.SearchQuery{
		Query: question, Wing: wing, Limit: absentVerifyDepth, SkipTelemetry: true,
	})
	if err != nil {
		return "", err
	}
	checker := &questionGen{
		url: gen.url, model: gen.model, apiKey: gen.apiKey,
		prompt: evalPromptAnswerCheck, http: gen.http,
	}
	for _, h := range hits {
		// The query-centred snippet, not the head of the note: the answer to the
		// question sits near the query terms, and the head of a long mined part
		// is usually about something else entirely.
		excerpt := palace.Snippet(h.Drawer.Content, question, 900)
		reply, err := checker.ask(ctx, "QUESTION: "+question+"\n\nNOTE:\n"+excerpt)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(reply)), "YES") {
			return h.Drawer.ID, nil
		}
	}
	return "", nil
}

// absentCaseOutcome decides what to do with one generated negative, given what
// the absence check concluded.
func absentCaseOutcome(answeredBy string, verr error) (keep bool, reason string) {
	if verr != nil {
		// Unknown is not confirmed. Keeping it writes a row indistinguishable
		// from a verified one, and every downstream number then treats an
		// unchecked assumption as a measurement. The temporal path already
		// enforces this; this one only documented it.
		return false, fmt.Sprintf("dropped: absence check failed, so absence is unverified (%v)", verr)
	}
	if answeredBy != "" {
		return false, fmt.Sprintf("rejected: memory %s answers it, so it is not absent", answeredBy)
	}
	return true, ""
}

// caseSource is where a run's questions came from: the cases themselves, a label
// for the header line, the provenance of the file when one was replayed, and
// whether the run replayed saved questions or wrote its own.
//
// Origin is grouped with the rest rather than derived by the caller because only
// this function knows it — the "--cases names a file that does not exist yet"
// branch is a generate-then-save run, and from the outside it is indistinguishable
// from a replay.
type caseSource struct {
	Cases []palace.EvalCase
	Label string
	// Styles is every style seen across the files that were merged. Meta keeps
	// only ONE block — the last file with a generator wins — so a check reading
	// Meta.Style is order-dependent, and an easy-negative file merged first would
	// report its neighbour's style.
	Styles []string
	Meta   caseFileMeta
	Origin string
}

// loadOrGenerateCases returns the cases, a label saying where they came from,
// and the provenance of the case file when one was replayed. The provenance
// travels because a replayed file's questions were written by whatever generator
// made it, which need not be the one this machine is configured with.
func loadOrGenerateCases(ctx context.Context, c *cli.Command, svc *services, team tenant.Team, out io.Writer) (caseSource, error) {
	var replayMeta caseFileMeta
	path := c.String("cases")
	if path != "" {
		var merged []palace.EvalCase
		var seenStyles []string
		files := strings.Split(path, ",")
		for _, f := range files {
			f = strings.TrimSpace(f)
			cases, meta, err := readCasesWithMeta(f)
			if meta.Generator != "" {
				replayMeta = meta
			}
			if st := strings.TrimSpace(meta.Style); st != "" {
				seenStyles = append(seenStyles, st)
			}
			switch {
			case err == nil:
				merged = append(merged, cases...)
			case os.IsNotExist(err) && len(files) == 1:
				// The generate-then-save flow: a single named file that does not
				// exist yet is where the generated cases will land.
			default:
				// A CORRUPT file, or a missing one among several, silently shrank
				// the case set once; the label still said "from N files" and no
				// run was comparable with any other.
				return caseSource{}, fmt.Errorf("cases file %s: %w", f, err)
			}
		}
		if len(merged) > 0 {
			label := "from " + path
			if len(files) > 1 {
				label = fmt.Sprintf("from %d files", len(files))
			}
			return caseSource{Cases: merged, Label: label, Styles: seenStyles, Meta: replayMeta, Origin: palace.CaseSetReplayed}, nil
		}
	}

	if c.String("style") == "real" {
		cases, from, err := generateRealCases(ctx, c, svc, team, out)
		return caseSource{Cases: cases, Label: from, Meta: generatedMeta(c), Origin: palace.CaseSetGenerated}, err
	}
	// Temporal cases are shaped differently — a pair is discovered before a
	// question is written — so the style gets its own generation loop instead of
	// growing this one a second set of skip reasons.
	if c.String("style") == "temporal" {
		// Meta comes from the generator, NOT from generatedMeta(c): only the
		// generator knows how many pairs a judge confirmed, and that record is
		// the supersession gate's first precondition. Rebuilding it from flags
		// here is what made `--style temporal --supersession-gate` refuse on its
		// own verified output (#35).
		cases, from, meta, err := generateTemporalCases(ctx, c, svc, team, out)
		return caseSource{Cases: cases, Label: from, Meta: meta, Origin: palace.CaseSetGenerated}, err
	}

	// Sample across the whole corpus rather than its newest slice: on a palace
	// that holds years, the newest thousand memories are one week of work, and an
	// eval built from them measures recall on recent memory only.
	//
	// Reproducibility comes from the saved case file rather than from a seed: the
	// questions are what a re-run must hold constant, and they are on disk.
	drawers, err := svc.drawers.SampleDrawers(ctx, team.ID, c.String("wing"), c.Int("n"))
	if err != nil {
		return caseSource{}, fmt.Errorf("sample drawers: %w", err)
	}
	if len(drawers) == 0 {
		// Named distinctly rather than folded into runEval's "no eval cases": an
		// empty corpus and a broken generator are different faults with different
		// fixes, and reporting them with one sentence sent the reader to inspect a
		// wing that was never the problem.
		return caseSource{}, fmt.Errorf("no drawers to sample in %s of workspace %q — file some memories first, or widen --wing",
			corpusLabel(c.String("wing")), team.Slug)
	}

	prompt, style := evalPromptParaphrase, c.String("style")
	category := palace.CatSingle
	switch style {
	case "literal":
		prompt = evalPromptLiteral
	case "crosslingual":
		prompt, category = evalPromptCrossLingual, palace.CatCrossLingual
	case "absent":
		prompt, category = evalPromptAbsent, palace.CatAbsent
	case "absent-easy":
		// The pre-2026-08-21 negative generator. Kept selectable so case files
		// made with it stay reproducible, and so the hard and easy regimes can be
		// run against each other instead of one silently replacing the other.
		prompt, category = evalPromptAbsentEasy, palace.CatAbsent
	}
	gen := &questionGen{
		url:     genURL(c),
		model:   c.String("gen-model"),
		apiKey:  strings.TrimSpace(c.String("gen-api-key")),
		prompt:  prompt,
		http:    &http.Client{Timeout: 120 * time.Second},
		verbose: out,
	}

	// Ask for ONE question before asking for thirty. A missing model, a wrong URL
	// or a bad key fails identically on every case, and printing that failure n
	// times is thirty lines that say one thing — while the run still takes as long
	// as a working one.
	if _, err := gen.ask(ctx, drawers[0].Content); err != nil {
		return caseSource{}, fmt.Errorf("the question generator is not usable: %w\n\n%s", err, gen.hint(ctx))
	}
	fmt.Fprintf(out, "generating %s questions with %s (%d drawers)…\n", style, gen.model, len(drawers))
	genStart := time.Now()
	var cases []palace.EvalCase
	for i, d := range drawers {
		started := time.Now()
		q, err := gen.ask(ctx, d.Content)
		if err != nil {
			// The FIRST failure aborts. A generator that cannot answer the first
			// drawer is misconfigured rather than unlucky — a missing model, a wrong
			// --ollama-url, a stopped daemon — and every remaining drawer would fail
			// the same way, so continuing buys nothing and costs one round trip each.
			// It also buries the cause: the loop used to end with "no eval cases: the
			// wing has no drawers", blaming a corpus that was never the problem.
			// Later failures still skip, because by then the generator has proven it
			// works and the fault is that drawer's.
			if i == 0 {
				return caseSource{}, fmt.Errorf("question generator failed on the first drawer, so it is misconfigured rather than unlucky: %w\n"+
					"  check `ollama list` — the model must be a GENERATIVE one (an embedder such as bge-m3 cannot answer /api/generate),\n"+
					"  pull it with `ollama pull %s`, or name one you already have with --gen-model", err, gen.model)
			}
			fmt.Fprintf(out, "  [%2d/%2d] failed: %v\n", i+1, len(drawers), err)
			continue
		}
		if q == "" {
			fmt.Fprintf(out, "  [%2d/%2d] empty answer, skipped\n", i+1, len(drawers))
			continue
		}
		fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  %s\n", i+1, len(drawers), time.Since(started).Seconds(), firstLineOf(q, 62))
		expect := d.ID
		var verification *palace.AbsentVerification
		if category == palace.CatAbsent {
			// There is no gold for a question the palace should not answer; what
			// gets measured is whether it returns something confident anyway.
			expect = ""
			// The seed note not answering it is not enough — verify the rest of
			// the corpus cannot either, or drop the case.
			answeredBy, verr := verifyAbsent(ctx, svc, team.ID, c.String("wing"), q, gen)
			keep, reason := absentCaseOutcome(answeredBy, verr)
			if reason != "" {
				fmt.Fprintf(out, "  [%2d/%2d] %s\n", i+1, len(drawers), reason)
			}
			if !keep {
				continue
			}
			// Stamped only on the path where the check actually PASSED, so the
			// field's presence means what it says. A case file merges runs, and a
			// case from before this existed must stay distinguishable from one
			// checked at depth 20 rather than inheriting its credibility.
			verification = &palace.AbsentVerification{
				Checker: gen.model,
				Depth:   absentVerifyDepth,
				At:      time.Now().UTC().Format(time.RFC3339),
			}
		}
		cases = append(cases, palace.EvalCase{
			Query: q, Expect: expect, Wing: c.String("wing"), Category: category,
			AbsentVerification: verification,
		})
	}
	fmt.Fprintf(out, "generated %d case(s) in %s\n", len(cases), time.Since(genStart).Round(time.Second))
	if path != "" && len(cases) > 0 {
		meta := caseFileMeta{
			Generator: gen.model, Style: style, Wing: c.String("wing"),
			Corpus: len(drawers), Created: time.Now().UTC().Format(time.RFC3339),
		}
		if err := writeCases(path, cases, meta); err != nil {
			fmt.Fprintf(out, "  (could not save cases to %s: %v)\n", path, err)
		} else {
			fmt.Fprintf(out, "saved %d case(s) to %s — pass --cases %s to re-run these exact questions\n", len(cases), path, path)
		}
	}
	return caseSource{Cases: cases, Label: "generated", Styles: []string{style}, Meta: replayMeta, Origin: palace.CaseSetGenerated}, nil
}

// genURL is where the question generator runs. It defaults to the embedder's
// Ollama so a single-machine setup configures nothing, and is separable because
// the two jobs have different shapes: embedding is a small, constant, local cost
// that belongs next to the data, while generating eval questions is a one-off
// burst of LLM work an operator may want to send to a bigger hosted model without
// moving their vectors off the box.
func genURL(c *cli.Command) string {
	if u := strings.TrimSpace(c.String("gen-url")); u != "" {
		return u
	}
	return cfg2URL(c.String("ollama-url"))
}

// questionGen asks a model for a question a given memory answers.
//
// The transport lives in internal/gen since ADR-047 T3: the same request path
// had been written twice here and in kgextract.go, and that ADR needed a third
// caller for its reader and judge. What stayed behind is what actually differs
// between callers — this one's prompt, its 1200-rune input cap, its temperature,
// and cleanQuestion keeping only a first line.
//
// The wire format is Ollama's own POST /api/generate ({model, prompt, stream,
// options} in, {response} out), so --gen-url accepts a local Ollama, a remote
// one, or hosted Ollama with a bearer token. It is NOT OpenAI-shaped: an
// OpenAI/Anthropic-compatible endpoint speaks /v1/chat/completions and would need
// a different request and reply, which is deliberately out of scope here — the
// generator is scaffolding for producing eval questions, not a model gateway.
type questionGen struct {
	url    string
	model  string
	apiKey string // sent as Authorization: Bearer when set; hosted providers need it
	prompt string

	http    *http.Client
	verbose io.Writer
}

// client builds the transport for one call. It is rebuilt per call rather than
// stored so the struct's fields stay the single source of truth — every call
// site here constructs a questionGen by field, and a cached client would go
// stale against a field somebody set afterwards.
func (g *questionGen) client() *gen.Client {
	return &gen.Client{URL: g.url, Model: g.model, APIKey: g.apiKey, HTTP: g.http}
}

// openAIShaped reports whether the endpoint should be called as an
// OpenAI-compatible chat API rather than Ollama's own. The /v1 convention is what
// every hosted provider and every local shim agrees on, so it is the honest
// discriminator.
//
// ⚠It governs the wording of hint and NOTHING ELSE — ask has never branched on
// it, and a URL containing /v1 still gets an /api/generate request. ADR-047 and
// its T3 task both claimed a "/v1 branch" here for a while, written from this
// method's name rather than its one call site.
func (g *questionGen) openAIShaped() bool { return g.client().OpenAIShaped() }

// hint turns a generator failure into something actionable: which models the
// endpoint actually serves, when it can be asked.
func (g *questionGen) hint(ctx context.Context) string { return g.client().Hint(ctx) }

// Two prompts, because there are two regimes and they rank differently.
//
// paraphrase is the hard case: the question shares no distinctive vocabulary with
// the note, so lexical matching has nothing to work with and the vector half
// carries the query. literal is the common case: a developer searching months
// later usually DOES remember the identifier, the flag or the error string.
//
// Measuring only one of them produces a confident, wrong conclusion about BM25 —
// which is exactly what the first run of this eval did.
const evalPromptParaphrase = `You are writing an evaluation question for a memory search system.

Below is a note an engineer wrote. Write ONE question that this note answers — the
kind a colleague would type into a search box months later, when they remember the
problem but not the note.

Rules:
- Paraphrase. Do NOT reuse the note's distinctive nouns, identifiers, file names,
  flags, or error strings.
- Ask about the situation or the decision, not about the wording.
- One line, under 20 words, no quotes, no preamble.

NOTE:
%s

QUESTION:`

// evalPromptCrossLingual tests the claim nobody has checked on this palace: that
// a multilingual embedder actually bridges the two languages the memories are
// written in. A bilingual team asks in whichever language it is thinking in, and
// the memory it needs is often in the other one.
const evalPromptCrossLingual = `You are writing an evaluation question for a memory search system.

Below is a note an engineer wrote. Write ONE question that this note answers — but
write it in the OTHER language: if the note is mostly English, ask in Lithuanian;
if it is mostly Lithuanian, ask in English.

Rules:
- Do not translate the note's distinctive identifiers, file names or flags — keep
  those as they are, and put the rest of the question in the other language.
- One line, under 20 words, no quotes, no preamble.

NOTE:
%s

QUESTION:`

// evalPromptAbsent generates questions the palace should NOT answer. Recall has
// only ever been measured on questions with an answer, which means the gate that
// decides "we do not know this" has never been measured at all.
const evalPromptAbsent = `You are writing a HARD NEGATIVE evaluation question for a memory search system.

Below is a note an engineer wrote. Write ONE question about a NEIGHBOURING topic
that this note does NOT answer and could not answer.

Rules:
- KEEP the note's distinctive identifiers, file names, flags and error strings.
  The question must look like it belongs to this note's world.
- Ask about something genuinely NOT covered: a different stage, a different
  component, or the same subject in another project's codebase.
- Plausible for this team to ask, and genuinely unanswered by the note.
- One line, under 20 words, no quotes, no preamble.

NOTE:
%s

QUESTION:`

// evalPromptAbsentEasy is the previous negative generator, kept so existing case
// files stay reproducible under --style absent-easy and so the two regimes can be
// compared rather than swapped silently. It strips the identifiers, which makes
// its negatives separable on surface vocabulary alone.
const evalPromptAbsentEasy = `You are writing a NEGATIVE evaluation question for a memory search system.

Below is a note an engineer wrote. Write ONE question about the same general area
of work that this note does NOT answer and could not answer — a neighbouring
topic, not this one.

Rules:
- Plausible for this team to ask, but genuinely unanswered by the note.
- Do not reuse the note's distinctive identifiers.
- One line, under 20 words, no quotes, no preamble.

NOTE:
%s

QUESTION:`

// evalPromptAnswerCheck asks whether a note answers a question. It exists
// because "absent" used to mean absent from the one sampled note, while the
// evaluator scored it as absent from the whole palace — another memory could
// answer the question perfectly, and the case would count a correct retrieval
// as a false positive.
const evalPromptAnswerCheck = `Does the NOTE below contain the answer to the QUESTION?
Reply with exactly one word: YES or NO.

%s`

// evalPromptRelevanceCheck judges a real query against one retrieved note. Real
// queries are often fragments rather than questions, so this asks about intent
// ("what the searcher wanted") instead of literal answerhood.
const evalPromptRelevanceCheck = `An engineer searched their team memory. Below is their QUERY and one NOTE the
search returned. Is this note what they were looking for — does it contain the
information the query is after?
Reply with exactly one word: YES or NO.

%s`

// evalPromptTemporal generates questions for facts that were later corrected.
// The note the generator sees is the NEWER half of a discovered pair; the older
// version stays in the corpus as a distractor. The question must ask for the
// fact's current state WITHOUT carrying a date — the eval measures whether
// ranking prefers the newer memory on its own, and a date in the question would
// hand it the answer.
const evalPromptTemporal = `You are writing an evaluation question for a memory search system.

Below is a note an engineer wrote. It records the current state of a fact that
changed at some point. Write ONE question asking what the current state of that
fact is — the kind a colleague would type into a search box, wanting today's
answer rather than the history.

Rules:
- Ask about the fact's current state, value, or status — not about when or why
  it changed.
- Do NOT include any date, year, or month in the question.
- One line, under 20 words, no quotes, no preamble.

NOTE:
%s

QUESTION:`

const evalPromptLiteral = `You are writing an evaluation question for a memory search system.

Below is a note an engineer wrote. Write ONE search query that should find this
note — the kind a developer actually types: partly natural language, partly the
exact identifier, file name, flag or error string they remember.

Rules:
- KEEP one or two distinctive terms from the note verbatim (a symbol, file, flag).
- Keep it short, like something typed into a search box.
- One line, under 15 words, no quotes, no preamble.

NOTE:
%s

QUERY:`

// ask returns one generated question, or "" when the model produced nothing
// usable.
func (g *questionGen) ask(ctx context.Context, content string) (string, error) {
	if len([]rune(content)) > 1200 {
		content = string([]rune(content)[:1200])
	}
	// Low temperature: the eval wants a representative question, not a creative
	// one, and reproducibility across runs matters more here than variety.
	raw, err := g.client().Generate(ctx, fmt.Sprintf(g.prompt, content), 0.2)
	if err != nil {
		return "", err
	}
	return cleanQuestion(raw.Text), nil
}

// cleanQuestion trims the shapes a small model wraps an answer in.
func cleanQuestion(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`))
	s = strings.TrimPrefix(s, "QUESTION:")
	return strings.TrimSpace(s)
}

// temporalCase builds the eval case for a verified supersession pair.
//
// The DISTRACTOR is the point of a temporal case: the metric scores where the
// SUPERSEDED version landed, not only where the gold did, and every consumer —
// SupersessionCell, StaleAboveRate, the gate's vacuity rule — reads it. The call
// site used to build this literal inline and omit it, and because
// EvalCase.Distractor is `json:",omitempty"` the field vanished from the case
// file without a word: files recorded verified_pairs above zero while carrying
// no pair at all, so the stale-above rate was structurally always empty and the
// gate could only ever refuse.
//
// It lives in its own function so that omission is a test away rather than a
// review away — a literal spread across a long generator loop is exactly where
// a missing field hides.
func temporalCase(query, wing string, newer, older palace.Drawer) palace.EvalCase {
	return palace.EvalCase{
		Query:      query,
		Expect:     newer.ID,
		Distractor: older.ID,
		Wing:       wing,
		Category:   palace.CatTemporal,
	}
}

// generatedMeta is the provenance of cases this run generated itself: whatever
// this machine is configured with, which is what the generators stamp into the
// case file they write.
func generatedMeta(c *cli.Command) caseFileMeta {
	return caseFileMeta{
		Generator: c.String("gen-model"),
		Style:     c.String("style"),
		Wing:      c.String("wing"),
		Created:   time.Now().UTC().Format(time.RFC3339),
	}
}

// caseFileMeta is the provenance record written as the FIRST line of a case
// file. Two runs of "the same" eval on different machines have already disagreed
// for reasons that were invisible afterwards — different generator models write
// questions of different difficulty, and nothing recorded which model wrote
// which file. A case file that does not say how it was made cannot be compared
// with anything.
type caseFileMeta struct {
	Meta      bool   `json:"meta"`
	Generator string `json:"generator"`
	Style     string `json:"style"`
	Wing      string `json:"wing"`
	Corpus    int    `json:"corpus_drawers"`
	Created   string `json:"created"`
	// Pair provenance, for --style temporal. Without it a replayed run cannot
	// tell a file whose pairs a judge confirmed from one generated before
	// verification existed, and the two produce different numbers from the same
	// command.
	PairCandidates int    `json:"pair_candidates,omitempty"`
	VerifiedPairs  int    `json:"verified_pairs,omitempty"`
	Judge          string `json:"judge,omitempty"`
}

// writeCases saves cases as JSONL.
func writeCases(path string, cases []palace.EvalCase, meta caseFileMeta) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	meta.Meta = true
	if err := enc.Encode(meta); err != nil {
		return err
	}
	for _, c := range cases {
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return nil
}

// caseSetLabel names the questions a verdict was computed over. A run with no
// stamped id predates the stamp; saying so is the correct reading of it.
func caseSetLabel(report palace.EvalReport) string {
	if report.CaseSetID == "" {
		return "an unidentified case set"
	}
	if report.CaseSetOrigin == "" {
		return "case set " + report.CaseSetID
	}
	return fmt.Sprintf("case set %s (%s)", report.CaseSetID, report.CaseSetOrigin)
}

// printEvalTable renders the arms and the cases every arm missed.
func printEvalTable(out io.Writer, report palace.EvalReport) {
	for _, w := range report.Warnings {
		fmt.Fprintf(out, "⚠ %s\n", w)
	}

	// The baseline every arm is compared against is the best MRR in the table.
	// A PAIRED difference that includes zero is reported as INCONCLUSIVE, not as
	// equivalence: the winner was itself picked from this data (winner's curse),
	// and a CI spanning zero means the data cannot rule out a difference — it
	// never means one was ruled out. The table says exactly that and no more.
	best := 0
	for i, m := range report.Arms {
		if m.MRR > report.Arms[best].MRR {
			best = i
		}
	}

	fmt.Fprintf(out, "%-40s %8s %8s %8s %14s %10s   %s\n", "arm", "R@1", "R@5", "MRR", "95% CI", "not found", "vs best")
	fmt.Fprintf(out, "%s\n", strings.Repeat("-", 110))
	for i, m := range report.Arms {
		ci := palace.BootstrapMRR(m.Ranks)
		verdict := ""
		switch {
		case len(m.Ranks) == 0:
			verdict = "no scoreable cases"
		case i == best:
			// The case set is named ON this line and not only in the header. Four
			// runs labelled a BEST arm over four different question sets, the label
			// moved between configurations, and the tables were read as agreeing —
			// the caveat has to sit where the quoted line is.
			verdict = "BEST over " + caseSetLabel(report)
		case len(m.Ranks) == len(report.Arms[best].Ranks):
			if delta := palace.PairedDelta(m.Ranks, report.Arms[best].Ranks); delta.Contains(0) {
				verdict = "inconclusive vs best (CI spans zero)"
			} else {
				verdict = fmt.Sprintf("worse by %.2f–%.2f", -delta.Hi, -delta.Lo)
			}
		}
		fmt.Fprintf(out, "%-40s %7.0f%% %7.0f%% %8.3f %14s %10d   %s\n",
			m.Arm, m.Recall1Pct(), m.Recall5Pct(), m.MRR, ci, m.NotFound, verdict)
	}
	fmt.Fprintf(out, "n=%d — CI column: single-arm bootstrap; 'vs best' verdicts: PAIRED bootstrap on per-case deltas (trust these, not CI overlap). The best arm was picked from this same table, so unadjusted comparisons against it flatter the winner; 'inconclusive' means exactly that, never equivalence\n",
		len(report.Arms[0].Ranks))

	printRetrievalCeiling(out, report)
	printPoolDiagnosis(out, report)
	printCategories(out, report)
	printSeparation(out, report)

	// Questions no arm could answer are usually a bad question rather than bad
	// ranking — the generator drifted off the note. Printing them keeps the table
	// honest, because they drag every arm down equally.
	var lost []string
	for _, d := range report.Details {
		// An absent case retrieving nothing is the CORRECT outcome, not a lost
		// one: listing it as a failure inverts the meaning of the whole category.
		if d.Category == palace.CatAbsent {
			continue
		}
		missed := true
		for _, r := range d.Ranks {
			if r > 0 {
				missed = false
				break
			}
		}
		if missed {
			lost = append(lost, d.Query)
		}
	}
	if len(lost) > 0 {
		fmt.Fprintf(out, "\n%d question(s) no arm retrieved (check whether the question is about the note at all):\n", len(lost))
		for i, q := range lost {
			if i == 5 {
				fmt.Fprintf(out, "  … and %d more\n", len(lost)-5)
				break
			}
			fmt.Fprintf(out, "  - %s\n", q)
		}
	}
}

// printRetrievalCeiling reports where the gold sits in the RETRIEVAL channel's
// own ordering, before any arm re-orders it.
//
// It exists to stop a whole class of misreading. Every arm in the table
// re-orders one shared pool that only the dense channel nominates — BM25 can
// promote a candidate but never introduce one, because there is no independent
// lexical retrieval here. So the arms differ in ORDERING and cannot differ in
// what was retrievable, and this line is the ceiling all of them play under.
// It is also the number to look at before believing that a published
// "hybrid improves recall" result applies: those widen the candidate pool,
// which this architecture does not do.
func printRetrievalCeiling(out io.Writer, report palace.EvalReport) {
	ranks := report.PoolRanks
	if len(ranks) == 0 {
		return
	}
	within := func(k int) int {
		n := 0
		for _, r := range ranks {
			if r > 0 && r <= k {
				n++
			}
		}
		return n
	}
	pct := func(n int) float64 { return 100 * float64(n) / float64(len(ranks)) }
	missing := 0
	for _, r := range ranks {
		if r == 0 { // never surfaced by the retrieval channel
			missing++
		}
	}

	fmt.Fprintf(out, "\nretrieval ceiling — where the answer sits by VECTOR DISTANCE alone, before any arm re-orders:\n")
	fmt.Fprintf(out, "  in pool: %.0f%%   top-1 %.0f%%   top-5 %.0f%%   top-10 %.0f%%   top-20 %.0f%%   top-50 %.0f%%\n",
		pct(len(ranks)-missing), pct(within(1)), pct(within(5)), pct(within(10)), pct(within(20)), pct(within(50)))
	if missing > 0 {
		fmt.Fprintf(out, "  %d of %d answer(s) were never retrieved at all — no ranking change can reach those; they need a wider pool, a different embedding, or a lexical channel that can NOMINATE candidates rather than only reorder them\n",
			missing, len(ranks))
	}
	fmt.Fprintf(out, "  every arm above re-orders this same pool, so arm-vs-arm differences are ordering results, never retrieval ones\n")
}

// printPoolDiagnosis separates the two failures a single score hides.
//
// A memory can be missed because ranking put it below the page (a RANKING
// failure, which reranking and fusion address) or because it never entered the
// candidate pool at all (a RETRIEVAL failure, which no amount of reranking can
// fix — the answer was never on the table). They call for opposite work, and on
// a large corpus the second becomes the common one while the score alone still
// just says "worse".
func printPoolDiagnosis(out io.Writer, report palace.EvalReport) {
	// Only ScopePool arms re-order the shared candidate set, so only their misses
	// mean "the gold was never on the table". The other scopes count something
	// else entirely and are reported on their own terms below — ArmContextual
	// retrieves from its own capped index, and ArmProduction is scored over the
	// PAGE Search returns, which is far shorter than the pool.
	//
	// This filters on the classification rather than naming arms, because naming
	// them is how production got folded in: the contextual case was excluded by
	// name when it was found, and the next arm with a non-pool scope inherited
	// the bug. A new arm now has to declare a scope (ArmScope is exhaustive by
	// construction) before it can appear here at all.
	worst, cases := 0, 0
	extra := map[palace.SupersessionScope]int{}
	for _, m := range report.Arms {
		if palace.ArmScope(m.Arm) != palace.ScopePool {
			if m.NotFound > extra[palace.ArmScope(m.Arm)] {
				extra[palace.ArmScope(m.Arm)] = m.NotFound
			}
			continue
		}
		if m.NotFound > worst {
			worst = m.NotFound
		}
		if m.Cases > cases {
			cases = m.Cases
		}
	}

	if worst > 0 {
		fmt.Fprintf(out, "\n%d of %d question(s) had their answer OUTSIDE the candidate pool — a retrieval failure, not a ranking one.\n", worst, cases)
		fmt.Fprintf(out, "  No reranker can recover those. Raise --pool and re-run: if they come back, the ranking is fine and the pool was too small;\n")
		fmt.Fprintf(out, "  if they stay missing, the embedding is not placing those memories near their question.\n")
	}

	// Each non-pool scope gets its own sentence naming ITS knob. A count printed
	// without the knob that moves it is worse than no count: the reader reaches
	// for the one knob the text mentions, which is the wrong one.
	if n := extra[palace.ScopeOwnIndex] - worst; n > 0 {
		fmt.Fprintf(out, "\nthe contextual arm missed %d question(s) beyond the shared pool's misses — those golds fall outside its capped sample; the knob is --contextual-limit, not --pool.\n", n)
	}
	if n := extra[palace.ScopePage] - worst; n > 0 {
		fmt.Fprintf(out, "\nproduction missed %d question(s) beyond the pool's misses — it is scored over the PAGE Search returns, not the pool, so those golds were retrieved and ranked below the page cut. Raising --pool does not move them: the knobs are the search limit and RERANK_POOL (which widens the fetch when a reranker is configured).\n", n)
	}
}

// printCategories breaks the leading arm out by question kind. An average over
// categories hides the failure that matters: a system can be perfect on
// single-hop questions and blind on the ones that need the CURRENT version of a
// corrected fact, and the mean looks fine.
func printCategories(out io.Writer, report palace.EvalReport) {
	if len(report.Arms) == 0 {
		return
	}
	// Break out the arm an operator will actually run: production first, then the
	// configured blend, then the plain fusion — never a sweep extreme, which is
	// what "last in the list" silently was once the sweeps existed.
	best := report.Arms[len(report.Arms)-1]
	for _, want := range []palace.EvalArm{palace.ArmProduction, palace.ArmReranked, palace.ArmHybridCloset} {
		for _, m := range report.Arms {
			if m.Arm == want && len(m.ByCategory) > 0 {
				best = m
				break
			}
		}
		if best.Arm == want {
			break
		}
	}
	if len(best.ByCategory) <= 1 {
		return // nothing to break out
	}
	fmt.Fprintf(out, "\n%s, by question kind\n", best.Arm)
	fmt.Fprintf(out, "%-16s %6s %8s %8s\n", "category", "cases", "R@1", "MRR")
	cats := make([]string, 0, len(best.ByCategory))
	for cat := range best.ByCategory {
		cats = append(cats, cat)
	}
	sort.Strings(cats)
	for _, cat := range cats {
		m := best.ByCategory[cat]
		if cat == palace.CatAbsent {
			fmt.Fprintf(out, "%-16s %6d %8s %8s   (scored by the separation below)\n", cat, m.Cases, "—", "—")
			continue
		}
		r1 := 0.0
		if m.Cases > 0 {
			r1 = float64(m.Recall1) / float64(m.Cases) * 100
		}
		fmt.Fprintf(out, "%-16s %6d %7.0f%% %8.3f\n", cat, m.Cases, r1, m.MRR)
	}
}

// printSeparation reports where answerable and unanswerable questions land in
// distance, which is the only honest basis for setting max_distance — the gate
// that decides when the palace should say it does not know. Overlapping
// distributions mean no threshold can separate them, and that is worth knowing
// before tuning a number.
func printSeparation(out io.Writer, report palace.EvalReport) {
	if len(report.AbsentDistances) == 0 || len(report.GoldDistances) == 0 {
		return
	}
	gold := append([]float64(nil), report.GoldDistances...)
	absent := append([]float64(nil), report.AbsentDistances...)
	sort.Float64s(gold)
	sort.Float64s(absent)
	fmt.Fprintf(out, "\ntop-1 distance: answerable %.3f–%.3f (median %.3f) | unanswerable %.3f–%.3f (median %.3f)\n",
		gold[0], gold[len(gold)-1], median(gold), absent[0], absent[len(absent)-1], median(absent))
	if gold[len(gold)-1] < absent[0] {
		fmt.Fprintf(out, "  clean separation — max_distance between %.3f and %.3f would answer only what it can\n",
			gold[len(gold)-1], absent[0])
		return
	}
	fmt.Fprintf(out, "  distributions OVERLAP — no max_distance separates them; a confidence gate needs a different signal\n")
	printRerankSeparation(out, report)
	// ...and this says WHICH different signal, instead of leaving the sentence
	// above as advice nobody can act on.
	printContrastiveSeparation(out, report)
}

// printRerankSeparation asks the same question of the cross-encoder's score.
//
// Cosine distance answers "how similar", which is not the question — a note about
// the same system in the same vocabulary is similar to a question it cannot
// answer, which is exactly why the distributions overlap. A cross-encoder score
// answers "does this document answer this query", which IS the question, so it is
// the natural candidate for the gate that decides when to say nothing.
func printRerankSeparation(out io.Writer, report palace.EvalReport) {
	if len(report.GoldRerank) == 0 || len(report.AbsentRerank) == 0 {
		return
	}
	gold := append([]float64(nil), report.GoldRerank...)
	absent := append([]float64(nil), report.AbsentRerank...)
	sort.Float64s(gold)
	sort.Float64s(absent)
	fmt.Fprintf(out, "top-1 rerank score: answerable %.3f–%.3f (median %.3f) | unanswerable %.3f–%.3f (median %.3f)\n",
		gold[0], gold[len(gold)-1], median(gold), absent[0], absent[len(absent)-1], median(absent))
	if absent[len(absent)-1] < gold[0] {
		fmt.Fprintf(out, "  CLEAN SEPARATION — a cross-encoder threshold between %.3f and %.3f answers only what the palace holds\n",
			absent[len(absent)-1], gold[0])
		return
	}
	fmt.Fprintf(out, "  overlapping too — but compare the medians: %.3f against %.3f is the size of the signal available\n",
		median(gold), median(absent))
}

// printContrastiveSeparation ranks every available signal by how well it actually
// separates answerable from unanswerable, and names the winner.
//
// The distance report above ends by saying the distributions overlap and "a
// confidence gate needs a different signal" — and then does not say which. The
// answer is measurable from the same run, so it is measured here rather than left
// to the reader's eye on two medians.
//
// The signals are of two KINDS. A level (top_rerank) is where the best document
// scored; a shape (the gaps and spreads) is how that score stood relative to the
// rest of the page. A similarity score has no anchored zero and its scale moves
// with the query, which is why the query-performance-prediction family is built
// from shapes. Which kind wins HERE is what this prints, because that is a
// property of the corpus and not of the argument.
//
// AUC rather than a median gap, because it is threshold-free: two signals can show
// the same median difference while one orders the cases far better, and a gate's
// question is about ordering.
//
// Reached only when the distance distributions OVERLAP, which is deliberate: a run
// where distance already separates cleanly has nothing to choose between, and the
// caller has already said so.
func printContrastiveSeparation(out io.Writer, report palace.EvalReport) {
	type signal struct {
		name string
		pick func(palace.EvalCaseResult) float64
		// higher reports whether a LARGER value means "answerable". Stated per
		// signal rather than assumed, because getting it wrong silently inverts
		// the AUC into a confident backwards answer.
		higher bool
	}
	// Distance shapes exist on every page. Cross-encoder shapes exist only when a
	// reranker ran, and listing them as flat zeros would put dead signals in the
	// ranking at a meaningless AUC 0.50 — so they are offered only when something
	// actually scored them.
	signals := []signal{
		{"dist_gap", func(d palace.EvalCaseResult) float64 { return d.DistGap }, true},
		{"dist_spread", func(d palace.EvalCaseResult) float64 { return d.DistSpread }, true},
	}
	reranked := false
	for _, d := range report.Details {
		if d.RerankScored {
			reranked = true
			break
		}
	}
	if reranked {
		signals = append(signals,
			signal{"top_rerank", func(d palace.EvalCaseResult) float64 { return d.TopRerank }, true},
			signal{"top_gap", func(d palace.EvalCaseResult) float64 { return d.TopGap }, true},
			signal{"score_spread", func(d palace.EvalCaseResult) float64 { return d.ScoreSpread }, true},
		)
	}

	var answerable, unanswerable []palace.EvalCaseResult
	for _, d := range report.Details {
		if d.Population == palace.PopAbsent {
			unanswerable = append(unanswerable, d)
		} else {
			answerable = append(answerable, d)
		}
	}
	// An AUC over one class is undefined, not weak. Say nothing.
	if len(answerable) == 0 || len(unanswerable) == 0 {
		return
	}

	fmt.Fprintf(out, "\nseparation by signal (n=%d answerable, %d unanswerable):\n",
		len(answerable), len(unanswerable))
	best, bestAUC := "", -1.0
	for _, sg := range signals {
		a := make([]float64, 0, len(answerable))
		for _, d := range answerable {
			a = append(a, sg.pick(d))
		}
		u := make([]float64, 0, len(unanswerable))
		for _, d := range unanswerable {
			u = append(u, sg.pick(d))
		}
		auc := separationAUC(a, u, sg.higher)
		fmt.Fprintf(out, "  %-13s AUC %.2f\n", sg.name, auc)
		if auc > bestAUC {
			best, bestAUC = sg.name, auc
		}
	}
	fmt.Fprintf(out, "  best separating signal: %s (AUC %.2f)", best, bestAUC)
	switch {
	case bestAUC >= 0.9:
		fmt.Fprintf(out, " — strong enough to build the gate on\n")
	case bestAUC >= 0.7:
		fmt.Fprintf(out, " — usable, but calibrate the threshold on more cases than this\n")
	default:
		fmt.Fprintf(out, " — NO signal here separates; a gate built on any of them would be noise\n")
	}
}

// separationAUC is the probability that a randomly chosen answerable case scores
// above a randomly chosen unanswerable one, ties counted as half.
//
// Written out rather than pulled in: it is six lines, and the alternative is a
// dependency for a statistic whose definition is the thing that has to be right.
func separationAUC(answerable, unanswerable []float64, higher bool) float64 {
	if len(answerable) == 0 || len(unanswerable) == 0 {
		return 0
	}
	var wins, ties float64
	for _, a := range answerable {
		for _, u := range unanswerable {
			switch {
			case a == u:
				ties++
			case (a > u) == higher:
				wins++
			}
		}
	}
	return (wins + 0.5*ties) / float64(len(answerable)*len(unanswerable))
}

// median of a sorted slice.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// corpusLabel names the scope for the header line.
func corpusLabel(wing string) string {
	if wing == "" {
		return "all wings"
	}
	return wing
}

// cfg2URL defaults the generator endpoint to the configured Ollama.
func cfg2URL(ollamaURL string) string {
	if strings.TrimSpace(ollamaURL) == "" {
		return "http://localhost:11434"
	}
	return ollamaURL
}

// firstLineOf truncates an error body for a one-line message.
func firstLineOf(s string, max int) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	// Truncate by runes, not bytes: half this palace's content is Lithuanian,
	// and a byte cut can split a multibyte rune into mojibake mid-progress-line.
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// printClosetBlock renders the comparison ADR-003 is decided on.
//
// It is separate from the arms table on purpose. Every "vs best" verdict there
// compares an arm against a baseline chosen from the same table, which is a fine
// diagnostic and a bad basis for flipping a default — the winner is selected by
// the same data that then judges it. This block names its pair before the run
// and prints what it excluded, so the number can be checked rather than trusted.
func printClosetBlock(out io.Writer, report palace.EvalReport) {
	cats := map[string]bool{}
	var order []string
	for _, d := range report.Details {
		if d.Category == palace.CatAbsent || cats[d.Category] {
			continue
		}
		cats[d.Category] = true
		order = append(order, d.Category)
	}
	if len(order) == 0 {
		return
	}

	fmt.Fprintf(out, "\ncloset prior — %s minus %s, preselected before the run (unlike the 'vs best' column, whose baseline is chosen from this same table):\n",
		palace.ArmHybridCloset, palace.ArmHybrid)
	fmt.Fprintf(out, "  %-18s %9s %12s %10s %16s %12s %7s\n",
		"category", "admitted", "unreachable", "ΔMRR", "95% paired CI", "Δrecall@1", "moved")
	for _, cat := range order {
		c := palace.ClosetDelta(report, cat)
		fmt.Fprintf(out, "  %-18s %9d %12d %+10.3f %16s %+12.3f %7d\n",
			cat, c.Admitted, c.Unreachable, c.DeltaMRR, c.Interval, c.DeltaRecall1, c.Moved)
	}
	fmt.Fprintln(out, "  Δ is closet minus no-closet: negative means the prior COSTS. 'unreachable' cases are")
	fmt.Fprintln(out, "  excluded because their gold never entered the pool, so no arm could have ranked it;")
	fmt.Fprintln(out, "  'moved' is how many admitted cases the two arms ordered differently at all — a Δ near")
	fmt.Fprintln(out, "  zero with nothing moved is a different finding from one where many cases cancelled.")
}

// cellsConfig is the ranking configuration a run was taken under. It travels
// with the numbers because a delta is only interpretable against the settings
// that produced it — an abstention threshold or a flipped default is valid for a
// configuration, never in the abstract.
type cellsConfig struct {
	Pool             int
	Cases            int
	ClosetScale      float64
	BM25Weight       string
	RerankConfigured bool
	RerankWeight     float64
	RerankPool       int
	// Ranking is the RESOLVED profile the service ranked with — fusion, lexical
	// weight and normaliser, closet scale, reranker. The record carried the
	// closet scale and the BM25 weight and named neither the fusion nor the
	// normaliser, so two runs at the same commit, one on rrf and one on linear,
	// produced different numbers and identical records. Resolved rather than
	// requested, because a reranker that was configured and did not come up is
	// exactly the case where the two differ and only the resolved one describes
	// what ranked.
	Ranking string
}

// buildStamp reports the commit the running binary was built from, and whether
// the tree was dirty. A run record that cannot name its own code is a set of
// numbers nobody can reproduce.
//
// It returns "unknown" rather than guessing when the binary carries no VCS
// stamp, which is what `go run` and some container builds produce.
func buildStamp() (commit string, dirty bool) {
	commit = "unknown"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return commit, false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return commit, dirty
}

// writeCells persists the run record the evidence directory holds.
//
// Two rules govern what goes in it, and they pull against each other. It must
// carry enough that two runs can be compared — the commit, the ranking config,
// the generator that wrote the questions — because runs of "the same" eval have
// already disagreed for reasons that were invisible afterwards. And it must
// carry nothing that came out of the palace: this file is committed to a public
// repository, the palace it measures is private, and the case files and results
// that DO hold queries and drawer ids stay untracked beside it.
//
// The case-set id is admissible for that reason: it is a one-way hash, so it
// identifies a question set to anyone who already holds it and discloses nothing
// to anyone who does not.
//
// ⚠ `wing` IS THE EXCEPTION THAT CARRIES AN IDENTITY, and the rule above
// overstated itself until this was written down.
//
// It is not the only palace-derived field. `corpus_drawers` is a SAMPLE size, not
// a corpus size: `len(drawers)`, bounded by `--n` AND by how many distinct
// `source_file` values the over-fetch reached, because ListRandom keeps at most
// one drawer per source (repo.go:797). Under `--style real` it is not drawers at
// all but `len(queries)`. It says how many items this run took and nothing about
// how big the palace is. A size discloses no identity either way.
//
// The wing is different in kind: it names a project. The name is an operator
// input rather than palace content, but it NAMES A PRIVATE PROJECT: `mine-claude` derives a wing from a session's
// working directory, so on a real palace it is somebody's project name, and this
// record is committed. `TestNoRealProjectNamesInWings` catches it — verified
// 2026-08-28 by planting one in an evidence file and watching the gate go red —
// which means an eval over a real mined wing produces evidence that cannot be
// committed. ADR-003 T3 needs the field to prove two mined runs share a corpus,
// so removing it removes a check; the tradeoff is filed in BACKLOG.md rather
// than settled here.
func writeCells(path string, report palace.EvalReport, meta caseFileMeta, cfg cellsConfig) error {
	commit, dirty := buildStamp()

	var cells []palace.ClosetCell
	seen := map[string]bool{}
	for _, d := range report.Details {
		if d.Category == palace.CatAbsent || seen[d.Category] {
			continue
		}
		seen[d.Category] = true
		cells = append(cells, palace.ClosetDelta(report, d.Category))
	}

	payload := map[string]any{
		"created":           time.Now().UTC().Format(time.RFC3339),
		"commit":            commit,
		"dirty":             dirty,
		"style":             meta.Style,
		"wing":              meta.Wing,
		"generator":         meta.Generator,
		"corpus_drawers":    meta.Corpus,
		"cases":             cfg.Cases,
		"pool":              cfg.Pool,
		"closet_scale":      cfg.ClosetScale,
		"bm25_weight":       cfg.BM25Weight,
		"rerank_configured": cfg.RerankConfigured,
		"rerank_weight":     cfg.RerankWeight,
		"rerank_pool":       cfg.RerankPool,
		"ranking":           cfg.Ranking,
		"case_set_id":       report.CaseSetID,
		"case_set_origin":   report.CaseSetOrigin,
		"warnings":          report.Warnings,
		"cells":             cells,
	}
	raw, err := json.MarshalIndent(payload, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// cellsPath is the run record's path, derived from the case file's stem exactly
// as resultsPath is, so a run's questions, results and record sit together.
func cellsPath(casesFlag string) string {
	first := strings.TrimSpace(strings.Split(casesFlag, ",")[0])
	if first == "" {
		return ""
	}
	return strings.TrimSuffix(first, ".jsonl") + ".cells.json"
}

// readCasesWithMeta is readCases plus the provenance line it used to drop.
//
// A replayed run knew its own --style flag and nothing about the generator that
// actually wrote the questions, so a case file produced by one model and
// replayed on a machine configured for another looked identical in every record
// it left behind.
func readCasesWithMeta(path string) ([]palace.EvalCase, caseFileMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, caseFileMeta{}, err
	}
	defer f.Close()
	var (
		cases []palace.EvalCase
		meta  caseFileMeta
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.Contains(line, `"meta":true`) {
			// A malformed provenance line loses the provenance, not the run.
			_ = json.Unmarshal([]byte(line), &meta)
			continue
		}
		var c palace.EvalCase
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, meta, fmt.Errorf("%s: %w", path, err)
		}
		cases = append(cases, c)
	}
	return cases, meta, sc.Err()
}

// supersessionGateReady reports why the gate must refuse to answer, or nil.
//
// It refuses rather than answering thinly, and each refusal names its own cause:
// "the gate said no" is useless if an operator cannot tell a thin corpus from an
// unhardened case file from a broken run.
//
// The floor is on pairs that are BOTH judge-verified and non-vacuous in THIS run.
// Not the generation-time verified_pairs integer — that knows nothing about the
// pool this run used, so it counts cases whose superseded version never entered
// the pool and no arm could have ranked.
//
// pool is the run's --pool, never the default: vacuity is a property OF a pool,
// so a count of vacuous pairs printed beside the wrong pool sends an operator to
// raise a number they already raised.
//
// The four refusals are deliberately separate. They are reached in the order a
// run fails them, and each names a different action: harden the file, load the
// right cases, widen the pool, grow the corpus. Collapsing the last three into
// one sentence — as this did — printed "0 pair(s) present" about a file whose own
// provenance line read verified_pairs: 5, because the number came from the run's
// scoreable cells while the sentence was about the file's record.
func supersessionGateReady(cell palace.SupersessionCell, meta caseFileMeta, pool int) error {
	if meta.VerifiedPairs == 0 && meta.Judge == "" {
		// The trailing count is LABELLED as the run's, because the sentence before
		// it is about the FILE's record and the two are different quantities. This
		// printed "0 pair(s) present" against a file whose provenance line read
		// verified_pairs: 5 — one number doing duty for both facts, and the reader
		// drew the conclusion the harness had made unavailable.
		return fmt.Errorf("this case file carries no pair-verification record, so its temporal cases "+
			"may pair unrelated memories — regenerate with --style temporal (pairs are judged at "+
			"generation) or point --cases at a file that was. What is missing is the record, not "+
			"necessarily the cases: this run scored %d pair(s)", cell.Cases+cell.Vacuous)
	}
	if cell.Cases+cell.Vacuous == 0 {
		return fmt.Errorf("the case file records %d judge-verified pair(s) but this run scored none — "+
			"no case reached the gated arm carrying a distractor, so what ran was not the supersession "+
			"measurement. Check that the --cases file is the temporal one and that --style did not "+
			"replace it", meta.VerifiedPairs)
	}
	if cell.Cases == 0 {
		return fmt.Errorf("all %d verified pair(s) are vacuous at --pool %d: the superseded version "+
			"never entered the candidate pool, so no arm could have ranked it above anything and there "+
			"is nothing to measure. Raise --pool", cell.Vacuous, pool)
	}
	if cell.Cases < palace.SupersessionMinCases() {
		return fmt.Errorf("only %d verified pair(s) are non-vacuous at --pool %d (%d were vacuous): "+
			"below %d the interval straddles almost any bar. Grow the dated corpus or raise --pool — "+
			"the bar is not the thing to change",
			cell.Cases, pool, cell.Vacuous, palace.SupersessionMinCases())
	}
	return nil
}

// gatedArmCell finds the pre-registered arm in a report, by identity.
//
// Never the nearest available arm: a degraded run drops the reranked arms, and
// gating whatever is left answers a different question under the same name. That
// substitution is the selection this gate exists to remove.
func gatedArmCell(report palace.EvalReport, want palace.EvalArm) (palace.SupersessionCell, error) {
	if want == "" {
		return palace.SupersessionCell{}, fmt.Errorf(
			"this server's ranking is not reconstructed by any eval arm, so there is no faithful number to " +
				"gate on — the arms are fixed pipelines and naming the nearest one is how the gate came to " +
				"judge a configuration nobody ran. Run the gate on a served configuration an arm reproduces, " +
				"or add the arm")
	}
	var reranked bool
	for _, m := range report.Arms {
		if m.Arm == want {
			return m.Supersession, nil
		}
		if strings.Contains(string(m.Arm), "rerank") {
			reranked = true
		}
	}
	if !reranked {
		return palace.SupersessionCell{}, fmt.Errorf(
			"the gate is registered against %q and this report has no reranked arm at all — the run was "+
				"degraded (--allow-degraded drops them when the cross-encoder fails its preflight); fix the "+
				"reranker and re-run rather than gating a different arm", want)
	}
	return palace.SupersessionCell{}, fmt.Errorf(
		"the gate is registered against %q, which this report does not contain although other reranked arms "+
			"do — the constant is stale and must move in the same commit that changed production ranking", want)
}

// defaultEvalPool is the --pool flag's default: how many candidates each query
// fetches before any arm re-orders them.
//
// It used to be a second copy of the literal on the flag, kept so the gate's
// refusal message could name a pool. That made the message wrong on every run
// that passed --pool: one at --pool 128 reported its vacuous pairs "at --pool 50"
// and advised raising a number the operator had already raised. The messages now
// take the run's actual pool, so this is the flag's default and nothing else.
const defaultEvalPool = 50

// printSupersessionGate renders the pre-registered verdict, or the reason it
// refuses to give one.
//
// A refusal is the useful answer more often than a verdict is: below the case
// floor the interval straddles almost any bar, and a gate that answers anyway
// teaches people to ignore it. Each refusal names its own cause so an operator
// can tell a thin corpus from an unhardened case file from a degraded run.
func printSupersessionGate(out io.Writer, report palace.EvalReport, meta caseFileMeta, want palace.EvalArm, pool int) error {
	cell, err := gatedArmCell(report, want)
	if err != nil {
		return err
	}
	if err := supersessionGateReady(cell, meta, pool); err != nil {
		return err
	}

	verdict := palace.SupersessionVerdict(cell, palace.SupersessionBar())
	// The veto is selection-aware: the best of the swept bands is compared at
	// alpha/k, and only vetoes if it also costs no general ranking.
	// Near-misses are collected, not discarded. A band that closes the failure
	// and is rejected on ranking cost produces an explanation the veto has
	// already computed, and it is exactly the sentence that stops someone
	// re-running the sweep next month — but the first version of this loop
	// adopted the outcome only when the STATUS changed, so that explanation was
	// computed and thrown away every time. Found by review, measured: 246
	// characters produced, 0 printed.
	var nearMiss []string
	for _, m := range report.Arms {
		band, ok := palace.RecencyBandCell(m)
		if !ok {
			continue
		}
		delta := palace.PairedDelta(m.Ranks, gatedArmRanks(report, want))
		v := palace.ApplyRecencyVeto(verdict, band, delta, palace.RecencyBandCount())
		if v.Status != verdict.Status {
			verdict = v
			break
		}
		if v.Reason != "" {
			nearMiss = append(nearMiss, fmt.Sprintf("%s: %s", m.Arm, v.Reason))
		}
	}

	fmt.Fprintf(out, "\nsupersession gate — %s\n", strings.ToUpper(verdict.Status))
	fmt.Fprintf(out, "  arm %s (pre-registered from the SERVED ranking, never chosen by score), %d verified non-vacuous pair(s) at --pool %d\n",
		want, cell.Cases, pool)
	fmt.Fprintf(out, "  stale-above %.1f%% %s against a bar of %.2f; excluding unreachable corrections: %.1f%%\n",
		100*verdict.Rate, verdict.Interval, palace.SupersessionBar(), 100*verdict.RateReachable)
	if verdict.Reason != "" {
		fmt.Fprintf(out, "  %s\n", verdict.Reason)
	}
	for _, nm := range nearMiss {
		fmt.Fprintf(out, "  near-miss — %s\n", nm)
	}
	return nil
}

// gatedArmRanks returns the pre-registered arm's per-case ranks, for the
// non-inferiority comparison. Empty when the arm is absent, which the caller has
// already refused on.
func gatedArmRanks(report palace.EvalReport, want palace.EvalArm) []int {
	for _, m := range report.Arms {
		if m.Arm == want {
			return m.Ranks
		}
	}
	return nil
}

// runCalibration turns this run's labelled rows into thresholds, a verdict and a
// file — or explains why it will not.
//
// Nothing here fires unless the operator asked for it. An eval that silently wrote
// a calibration would make every ordinary measurement a candidate operating point,
// and the whole reason this is a file rather than configuration is that a human
// decides when one becomes real.
func runCalibration(ctx context.Context, c *cli.Command, cfg config.Config, report palace.EvalReport, cases []palace.EvalCase, styles []string, out io.Writer) error {
	if !c.Bool("calibrate") && !c.Bool("gate") {
		return nil
	}

	rows := make([]palace.CalibrationRow, 0, len(report.Details))
	for _, d := range report.Details {
		rows = append(rows, palace.CalibrationRow{
			Population: d.Population,
			// The score the SERVED gate will compare. Measured on this corpus the
			// absolute cross-encoder score separates answerable from unanswerable
			// at 0.81 AUC against 0.61-0.71 for every page-shape statistic, so the
			// level is what the curve is fitted over and the shapes are reported
			// beside it rather than used.
			Score:  d.TopRerank,
			Scored: d.RerankScored,
		})
	}
	th := palace.RecommendThresholds(rows, c.Float("answer-recall"), c.Int("refuse-allowance"))

	fmt.Fprintf(out, "\ncalibration — %d reachable-answerable, %d verified-absent, %d unreachable (excluded)\n",
		th.Reachable, th.Absent, th.Unreachable)
	if th.Reachable == 0 || th.Absent == 0 {
		// The Stop Condition, as an outcome rather than a crash: a curve needs
		// both populations, and one of them alone is not a weak measurement, it is
		// no measurement.
		//
		// Two very different causes reach here and they need different advice. If
		// NOTHING was scored, the run had no reranker and the case set is
		// irrelevant — telling the reader to merge more files is a remedy that
		// cannot work, which is worse than no advice because they will try it.
		scored := 0
		for _, r := range rows {
			if r.Scored {
				scored++
			}
		}
		switch {
		case scored == 0:
			fmt.Fprintf(out, "  no curve: nothing in this run carries a cross-encoder score, so there is\n"+
				"  no quantity to threshold. The gate reads the served page's top-1 rerank score;\n"+
				"  pass --rerank-url to score it. Merging more case files will not help.\n")
		default:
			fmt.Fprintf(out, "  no curve: a threshold needs BOTH answerable and verified-absent cases in one\n"+
				"  run, and this one has %d and %d. Merge two case files with\n"+
				"  --cases answerable.jsonl,absent.jsonl.\n", th.Reachable, th.Absent)
		}
		if c.Bool("gate") {
			return fmt.Errorf("gate requested but the run holds no separation to gate on")
		}
		return nil
	}
	fmt.Fprintf(out, "  answer_at    %s (target recall %.2f, achieved %.3f)\n",
		fmtThreshold(th.AnswerAt), c.Float("answer-recall"), th.AchievedRecall)
	fmt.Fprintf(out, "  refuse_below %s (allowance %d)\n", fmtThreshold(th.RefuseBelow), c.Int("refuse-allowance"))
	if th.BandEmpty {
		fmt.Fprintf(out, "  the band is EMPTY: both rules chose the same threshold, so this corpus supports\n"+
			"  three verdicts and not four — there is no score range where the honest answer is\n"+
			"  'I am not sure'.\n")
	}

	// The curve BEFORE the verdict, because a FAIL is only actionable once the
	// reader can see whether any threshold would have passed.
	curve := palace.RiskCoverageCurve(rows)
	printCurve(out, curve, c.Float("answer-recall"), c.Float("refusal-bar"))

	refused := 0
	if th.AnswerAt != nil {
		for _, r := range rows {
			if r.Scored && r.Population == palace.PopAbsent && r.Score < *th.AnswerAt {
				refused++
			}
		}
	}
	// T2 step 4: how many verified-absent cases fall below the LOWER boundary, not
	// the upper one. The lower boundary sits just under the answerable tail by
	// construction, so this count can be zero — and when it is, the fourth verdict
	// can never fire on evidence like this. That has to be said in as many words,
	// because a design documented as four verdicts and delivering three is a
	// difference nobody notices until they look for the missing one.
	belowRefuse := 0
	if th.RefuseBelow != nil {
		for _, r := range rows {
			if r.Scored && r.Population == palace.PopAbsent && r.Score < *th.RefuseBelow {
				belowRefuse++
			}
		}
	}
	fmt.Fprintf(out, "  verified-absent below refuse_below: %d of %d\n", belowRefuse, th.Absent)
	if belowRefuse == 0 {
		fmt.Fprintf(out, "    zero — the 'no answer' verdict can NEVER fire on evidence like this sample.\n"+
			"    This corpus supports THREE verdicts, not four.\n")
	}

	pass, rate, lower, n := palace.RefusalGate(th.Absent, refused, c.Float("refusal-bar"))
	fmt.Fprintf(out, "  correct refusal at answer_at: %d/%d = %.3f, 90%% lower bound %.3f, bar %.2f — %s\n",
		refused, n, rate, lower, c.Float("refusal-bar"), gateWord(pass))

	if ok, why := calibrationEligible(cases, styles...); !ok {
		fmt.Fprintf(out, "  NO calibration file: %s.\n"+
			"  The curve above still stands — it is what T3 compares regimes with — but nothing\n"+
			"  here may become an operating point.\n", why)
		if c.Bool("gate") && !pass {
			return fmt.Errorf("abstention gate FAILED: refusal lower bound %.3f is below the %.2f bar over %d verified-absent cases",
				lower, c.Float("refusal-bar"), n)
		}
		return fmt.Errorf("refusing to write a calibration: %s", why)
	}

	if !c.Bool("calibrate") {
		if c.Bool("gate") && !pass {
			return fmt.Errorf("abstention gate FAILED: refusal lower bound %.3f is below the %.2f bar over %d verified-absent cases",
				lower, c.Float("refusal-bar"), n)
		}
		return nil
	}

	// The fingerprint identifies the instrument. Without a reranker there is
	// nothing to fingerprint, and a calibration that cannot be checked against the
	// thing that produced it is a number with no provenance.
	// palace.Service deliberately exposes no accessor to its own reranker, so the
	// probe is built from the SAME configuration the service was built from. It
	// identifies the endpoint and model — which is what the fingerprint is for —
	// rather than the particular client object.
	if strings.TrimSpace(cfg.RerankURL) == "" {
		return fmt.Errorf("no --rerank-url configured, so there is nothing to fingerprint; a " +
			"calibration that cannot be checked against the instrument that produced it is a " +
			"number with no provenance")
	}
	canary, err := palace.ScoreCanary(ctx, tei.New(cfg.RerankURL, cfg.RerankTimeout))
	if err != nil {
		return fmt.Errorf("cannot fingerprint the reranker, so no calibration is written: %w", err)
	}
	cal := palace.Calibration{
		AnswerAt: th.AnswerAt, RefuseBelow: th.RefuseBelow,
		AnswerRecallTarget: c.Float("answer-recall"), RefuseAllowance: c.Int("refuse-allowance"),
		AchievedRecall: th.AchievedRecall,
		Reachable:      th.Reachable, Absent: th.Absent, Unreachable: th.Unreachable,
		GatePassed: pass, RefusalRate: rate, RefusalLower: lower, RefusalBar: c.Float("refusal-bar"),
		RerankModel:   strings.TrimSpace(c.String("rerank-model")),
		Profile:       rankingProfileLabel(c),
		Canary:        canary,
		ScoresBounded: palace.ScoresLookBounded(canary),
	}
	path := c.String("calibration-out")
	if err := cal.Save(path); err != nil {
		return fmt.Errorf("write calibration: %w", err)
	}
	fmt.Fprintf(out, "  wrote %s (id %s, fingerprint over %d canary pairs, tolerance %.4f)\n",
		path, cal.ID, len(canary), widestTolerance(canary))
	fmt.Fprintf(out, "  the server does NOT read this by itself — point it at the file to make it real.\n")

	if c.Bool("gate") && !pass {
		return fmt.Errorf("abstention gate FAILED: refusal lower bound %.3f is below the %.2f bar over %d verified-absent cases",
			lower, c.Float("refusal-bar"), n)
	}
	return nil
}

// fmtThreshold prints a threshold that may be ABSENT, without letting absent read
// as the number zero — which on an unbounded cross-encoder scale is an ordinary
// score sitting in the middle of the distribution.
func fmtThreshold(v *float64) string {
	if v == nil {
		return "(none derivable)"
	}
	return fmt.Sprintf("%.4f", *v)
}

// gateWord names the go/no-go. Distinct from doctor.go's verdictWord, which
// answers a different question with a different sentence.
func gateWord(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}

// widestTolerance is the largest spread any canary pair showed, which is what a
// startup check has to allow for across all of them.
func widestTolerance(pairs []palace.CanaryPair) float64 {
	worst := 0.0
	for _, p := range pairs {
		if p.MaxDeviation > worst {
			worst = p.MaxDeviation
		}
	}
	return worst
}

// rankingProfileLabel records the ranking configuration the thresholds were
// derived under. A threshold calibrated with one fusion mode and applied under
// another is calibrated on nothing.
func rankingProfileLabel(c *cli.Command) string {
	return fmt.Sprintf("fusion=%s bm25=%s closet=%.2f rerank-pool=%d rerank-weight=%s",
		c.String("fusion"), c.String("bm25-weight"), c.Float("closet-boost"),
		c.Int("rerank-pool"), c.String("rerank-weight"))
}

// printCurve shows the exchange rate between answering and refusing, and then
// answers the question a bare verdict cannot: is there any threshold at all that
// clears both declared bars.
//
// Without that last line a FAIL is ambiguous. "Retune the threshold" and "this
// corpus cannot support the design" look identical from a single failing operating
// point, and they send the reader to completely different work.
func printCurve(out io.Writer, curve []palace.CurvePoint, answerRecall, refusalBar float64) {
	if len(curve) == 0 {
		return
	}
	fmt.Fprintf(out, "\n  risk-coverage — what each threshold costs:\n")
	fmt.Fprintf(out, "    %-12s %-16s %s\n", "threshold", "answer recall", "correct refusal")
	// A handful of rows rather than every observed score: the shape is the point,
	// and forty near-identical lines bury it.
	step := 1
	if len(curve) > 12 {
		step = len(curve) / 10
	}
	for i := 0; i < len(curve); i += step {
		p := curve[i]
		fmt.Fprintf(out, "    %-12.4f %5.3f (%2d)      %5.3f (%2d)\n",
			p.Threshold, p.AnswerRecall, p.Answered, p.CorrectRefusal, p.Refused)
	}
	if last := curve[len(curve)-1]; (len(curve)-1)%step != 0 {
		fmt.Fprintf(out, "    %-12.4f %5.3f (%2d)      %5.3f (%2d)\n",
			last.Threshold, last.AnswerRecall, last.Answered, last.CorrectRefusal, last.Refused)
	}
	if p := palace.ViablePoint(curve, answerRecall, refusalBar); p != nil {
		fmt.Fprintf(out, "    a threshold at %.4f clears BOTH bars (recall %.3f >= %.2f, refusal %.3f >= %.2f)\n",
			p.Threshold, p.AnswerRecall, answerRecall, p.CorrectRefusal, refusalBar)
	} else {
		fmt.Fprintf(out, "    NO threshold on this corpus clears both bars (recall >= %.2f AND refusal >= %.2f).\n"+
			"    That is a finding about the SIGNAL, not about the tuning: retuning cannot reach a\n"+
			"    point that does not exist. Change the score the gate reads, the corpus it is\n"+
			"    calibrated on, or the targets themselves.\n", answerRecall, refusalBar)
	}
}

// calibrationEligible reports whether this case set may produce a shipped
// threshold, and why not when it may not.
//
// Two disqualifications, both about whether the negatives mean what they claim.
// An absent case with no verification provenance was never confirmed absent, so
// some other memory may answer it perfectly and calibrating on it tunes the gate
// to refuse questions the palace can answer. A set generated with
// --style absent-easy was confirmed, but against negatives whose identifiers were
// stripped — measured on this corpus those sit twice as far from answerable
// questions as the hard ones, so a threshold fitted to them is fitted to a gap
// production will not have.
//
// This gates the FILE, never the report. T3 has to compare the two regimes side by
// side, so the curve still prints; what is withheld is the artefact an operator
// could point a server at.
func calibrationEligible(cases []palace.EvalCase, styles ...string) (bool, string) {
	// EVERY style present, not the one the merged metadata happened to keep. A
	// merged set carries one metadata block and the last file with a generator
	// wins, so reading a single style makes the check order-dependent — and an
	// easy-negative file merged first would report its neighbour's style.
	for _, style := range styles {
		if strings.TrimSpace(style) != "absent-easy" {
			continue
		}
		return false, "one of the case files was generated with --style absent-easy, whose negatives " +
			"drop the note's identifiers and are separable on vocabulary alone; a threshold fitted " +
			"to them is fitted to a gap production will not have"
	}
	absent, unverified := 0, 0
	for _, c := range cases {
		if c.Category != palace.CatAbsent {
			continue
		}
		absent++
		if c.AbsentVerification == nil {
			unverified++
		}
	}
	if absent == 0 {
		return false, "the case set holds no absent cases, so there is no separation to fit a threshold to"
	}
	if unverified > 0 {
		return false, fmt.Sprintf("%d of %d absent case(s) carry no verification provenance — their absence "+
			"was never checked, so a memory may answer them that nobody looked for", unverified, absent)
	}
	return true, ""
}

// splitArms parses the comma-separated --arms value, dropping empty fields so a
// trailing comma or a stray space cannot become a pattern that matches nothing.
func splitArms(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
