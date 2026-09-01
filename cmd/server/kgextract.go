// kgextract.go implements `agentsmemory kg-extract`: batch-extract knowledge-graph
// triples from a wing's mined drawers.
//
// The palace accumulates drawers by the thousand but triples only when an agent
// thinks to file one, so the graph starves while the prose that should feed it
// sits indexed a table away. This command closes that loop: it reads each mined
// source back out of the wing, asks a generative model for the durable facts as
// `subject | predicate | object` lines, and files them with the source as
// provenance.
//
// Two properties make it operable on a real palace:
//
//   - Idempotent per source: filing goes through KGReplaceSource, which purges
//     the source's prior extracted triples before adding the new ones, so a
//     re-run replaces rather than duplicates.
//   - Incremental across runs: never-extracted sources are processed first,
//     --limit at a time, and the summary reports how many still have no triples
//     — a full palace is hours of LLM time, so the operator walks it in slices.
//
// The generator is the same Ollama /api/generate scaffolding eval.go uses (same
// flags, same EVAL_GEN_* envs, same first-failure doctrine): a model that cannot
// answer the first ask is misconfigured rather than unlucky, and aborting there
// names the fix instead of burning a round trip per window to fail identically.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/gen"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"github.com/urfave/cli/v3"
)

const (
	// kgExtractWindowRunes is how much source text one generator ask carries —
	// the same cap eval.go's question generator applies, proven to fit the
	// default local model's context. Longer sources are windowed, not truncated:
	// cutting a 50k-rune source to one window would silently discard most of it.
	kgExtractWindowRunes = 1200
	// kgExtractMaxWindows bounds the asks one source may consume so a single
	// pathological source cannot eat the whole run's LLM budget. Hitting it is
	// reported with the skipped amount — a cap is sensible, a silent one is not.
	kgExtractMaxWindows = 24
	// kgExtractFailBreaker is how many consecutive post-preflight failures stop
	// the run: one failed window is that window's fault, several in a row is a
	// dead daemon, and each doomed ask costs a full HTTP timeout.
	kgExtractFailBreaker = 3
)

// kgExtractCommand builds the `kg-extract` subcommand.
func kgExtractCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "kg-extract",
		Usage: "extract knowledge-graph triples from a wing's mined sources with a generative model",
		Description: "Reads each mined source's drawers back out of the wing, asks a generative\n" +
			"model for the durable facts as 'subject | predicate | object' lines, and files\n" +
			"them into the knowledge graph with the source as provenance. Re-running a\n" +
			"source replaces its triples instead of duplicating them.\n\n" +
			"Sources with no extracted triples yet are processed first, --limit at a time,\n" +
			"so repeated runs walk the whole wing incrementally:\n\n" +
			"  agentsmemory kg-extract --wing wing_acme\n" +
			"  agentsmemory kg-extract --wing wing_acme --limit 50 --gen-url https://ollama.com --gen-api-key …\n" +
			"  agentsmemory kg-extract --project acme --wing wing_acme  # a multi-tenant database",
		Flags: append(dataFlags(def),
			projectFlag(),
			&cli.StringFlag{Name: "wing", Required: true, Usage: "extract from this wing's sources"},
			&cli.IntFlag{Name: "limit", Value: 10, Usage: "sources per run (0 = every source; a full palace is hours of LLM time, so run it in slices)"},
			&cli.StringFlag{Name: "gen-model", Sources: cli.EnvVars("EVAL_GEN_MODEL"), Value: "qwen2.5-coder:7b", Usage: "model that extracts the triples (must be GENERATIVE — an embedder like bge-m3 cannot answer /api/generate)"},
			&cli.StringFlag{Name: "gen-url", Sources: cli.EnvVars("EVAL_GEN_URL"), Usage: "where the extractor runs; defaults to --ollama-url, so set it only to generate somewhere other than the embedder (e.g. Ollama Cloud)"},
			&cli.StringFlag{Name: "gen-api-key", Sources: cli.EnvVars("EVAL_GEN_API_KEY"), Usage: "bearer token for --gen-url; required by hosted Ollama, ignored by a local one"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return runKGExtract(ctx, c, def, os.Stdout)
		},
	}
}

// runKGExtract is the whole flow: pick the batch of sources, generate + parse
// triples per source, replace that source's triples, and summarize what remains.
func runKGExtract(ctx context.Context, c *cli.Command, def config.Config, out io.Writer) error {
	cfg := configFromCmd(c, def)
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	// Same trust model as eval, wing export and inspect: possessing the database
	// file is the authorization, and --project names the workspace inside it.
	team, err := resolveProject(ctx, svc, c.String("project"))
	if err != nil {
		return err
	}
	wing := c.String("wing")
	sources, err := svc.drawers.KGExtractSources(ctx, team.ID, wing)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("no drawer sources to extract in wing %q of workspace %q — mine some content first (drawers filed without a source are skipped)", wing, team.Slug)
	}
	batch := sources
	if limit := c.Int("limit"); limit > 0 && limit < len(batch) {
		batch = batch[:limit]
	}

	gen := &tripleGen{
		url:    genURL(c),
		model:  c.String("gen-model"),
		apiKey: strings.TrimSpace(c.String("gen-api-key")),
		http:   telemetry.HTTPClient(120 * time.Second),
	}
	fmt.Fprintf(out, "extracting triples from %d of %d source(s) in %s with %s…\n", len(batch), len(sources), wing, gen.model)

	var totalFiled, totalMalformed, totalRejected, processed int
	proven := false       // flips after the generator's first successful answer
	consecutiveFails := 0 // resets on success; the breaker below reads it
	for i, src := range batch {
		started := time.Now()
		text, err := svc.drawers.KGSourceText(ctx, team.ID, wing, src.Source)
		if err != nil {
			return fmt.Errorf("read source %q: %w", src.Source, err)
		}
		windows := windowRunes(text, kgExtractWindowRunes)
		if len(windows) > kgExtractMaxWindows {
			fmt.Fprintf(out, "  %s spans %d window(s); processing the first %d and skipping the rest — split the source if its tail matters\n",
				src.Source, len(windows), kgExtractMaxWindows)
			windows = windows[:kgExtractMaxWindows]
		}

		var triples []palace.ExtractedTriple
		malformed, windowFails := 0, 0
		for wi, w := range windows {
			raw, err := gen.extract(ctx, w)
			if err != nil {
				// The FIRST failure aborts, copying eval.go's doctrine: a generator
				// that cannot answer its first ask is misconfigured (missing model,
				// wrong URL, stopped daemon) rather than unlucky, and every further
				// window would fail identically. Once it has answered anything, a
				// failure is that window's fault, so it is reported and skipped.
				if !proven {
					return fmt.Errorf("triple generator failed on the first ask, so it is misconfigured rather than unlucky: %w\n"+
						"  check `ollama list` — the model must be a GENERATIVE one (an embedder such as bge-m3 cannot answer /api/generate),\n"+
						"  pull it with `ollama pull %s`, or name one you already have with --gen-model", err, gen.model)
				}
				windowFails++
				consecutiveFails++
				// A run whose generator answered once and then fails call after
				// call is not unlucky either — the daemon died mid-run — and each
				// doomed ask burns a full HTTP timeout. Stop BEFORE replacing the
				// current source, so its prior extraction survives.
				if consecutiveFails >= kgExtractFailBreaker {
					return fmt.Errorf("%d consecutive generator failures — the daemon likely died mid-run (last: %w); nothing was purged for the current source, re-run to continue", consecutiveFails, err)
				}
				fmt.Fprintf(out, "  [%2d/%2d] %s window %d/%d failed, skipped: %v\n", i+1, len(batch), firstLineOf(src.Source, 40), wi+1, len(windows), err)
				continue
			}
			proven = true
			consecutiveFails = 0
			ts, mal := parseTriples(raw)
			triples = append(triples, ts...)
			malformed += mal
		}

		// Replace only when EVERY window was asked successfully: KGReplaceSource
		// purges the source's prior triples first, and a partial generation would
		// trade the facts the failed windows used to contribute for nothing. The
		// source honestly stays unrefreshed and is reported so.
		if windowFails > 0 {
			fmt.Fprintf(out, "  [%2d/%2d] %s: %d of %d window(s) failed — prior triples left intact, re-run to retry\n",
				i+1, len(batch), firstLineOf(src.Source, 40), windowFails, len(windows))
			continue
		}
		res, err := svc.drawers.KGReplaceSource(ctx, team.ID, wing, src.Source, triples)
		if err != nil {
			return fmt.Errorf("file triples for %q: %w", src.Source, err)
		}
		processed++
		totalFiled += res.Filed
		totalMalformed += malformed
		totalRejected += res.Rejected
		fmt.Fprintf(out, "  [%2d/%2d] %5.1fs  %s — %d added, %d malformed, %d rejected\n",
			i+1, len(batch), time.Since(started).Seconds(), firstLineOf(src.Source, 48), res.Filed, malformed, res.Rejected)
	}

	// Recount instead of subtracting: "remaining" means sources with NO extracted
	// triples, and a source processed this run that yielded zero triples is still
	// remaining by that definition — arithmetic on the batch size would hide it.
	after, err := svc.drawers.KGExtractSources(ctx, team.ID, wing)
	if err != nil {
		return fmt.Errorf("recount remaining sources: %w", err)
	}
	remaining := 0
	for _, s := range after {
		if !s.Extracted {
			remaining++
		}
	}
	fmt.Fprintf(out, "\nprocessed %d source(s): %d triple(s) added, %d malformed line(s) skipped, %d rejected by validation\n",
		processed, totalFiled, totalMalformed, totalRejected)
	fmt.Fprintf(out, "%d of %d source(s) still have no extracted triples — re-run to continue\n", remaining, len(after))
	return nil
}

// tripleGen asks a model to extract triples from one window of source text.
//
// It shares only its TRANSPORT with eval.go's questionGen — internal/gen since
// ADR-047 T3 — and differs in every parameter that matters: its own prompt, a
// lower temperature (extraction wants what the text states, and creativity here
// is fabrication), and the raw multi-line body, because the caller parses triples
// out of it where questionGen keeps a cleaned first line. Unifying the parsing
// would have silently changed what this command extracts.
type tripleGen struct {
	url    string
	model  string
	apiKey string // sent as Authorization: Bearer when set; hosted Ollama needs it

	http *http.Client
}

// kgExtractPrompt asks for durable facts only, one pipe-separated triple per
// line. The shape is dictated by the parser (split on the first two pipes) and
// by KGAdd's sanitizers: values must stay short (128-rune cap) and predicates
// must be plain words (SanitizeName rejects punctuation beyond . ' _ -).
const kgExtractPrompt = `You are extracting facts for a team knowledge graph.

Below is an excerpt of an engineer's notes. Extract the DURABLE facts it states
as triples, one per line, in exactly this shape:

subject | predicate | object

Rules:
- Only facts the text states. Never invent, never infer beyond the text.
- Durable facts only: what owns, uses, depends on, stores, or configures what.
  Skip narration ("we tried X"), opinions, questions and TODOs.
- Keep values short: subject and object a few words each, predicate 1-3 plain
  words (no punctuation).
- Output triple lines ONLY — no numbering, no prose, no headings.
- If the excerpt states no durable fact, output nothing.

NOTES:
%s

TRIPLES:`

// extract returns the model's raw answer for one window.
func (g *tripleGen) extract(ctx context.Context, window string) (string, error) {
	c := &gen.Client{URL: g.url, Model: g.model, APIKey: g.apiKey, HTTP: g.http}
	// Low temperature: extraction wants the facts the text states, and a re-run
	// should reproduce them — creativity here is fabrication.
	return c.Generate(ctx, fmt.Sprintf(kgExtractPrompt, window), 0.1)
}

// listMarker matches a leading ordered-list marker ("1. ", "12) ") so numbered
// generator output parses to the fact, not to "1. fact".
var listMarker = regexp.MustCompile(`^\d+[.)]\s+`)

// parseTriples parses generator output into triples, one line each, splitting on
// the FIRST two pipes only — so an object that itself contains a pipe ("reads |
// writes") survives intact instead of shedding its tail. Non-empty lines that do
// not yield three non-empty parts are skipped but COUNTED: a high malformed
// count means the model is ignoring the format, and hiding that would make a
// quiet run and a broken one look the same. A leading list bullet is tolerated,
// because small models emit one despite the prompt and the line under it is
// still a perfectly good triple.
func parseTriples(raw string) ([]palace.ExtractedTriple, int) {
	var out []palace.ExtractedTriple
	malformed := 0
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		for _, bullet := range []string{"- ", "* ", "• "} {
			line = strings.TrimPrefix(line, bullet)
		}
		// Numbered lists ("1. cache | uses | redis") and bold markers arrive from
		// the same small models the bullet tolerance exists for; unstripped they
		// parse "successfully" with a junk subject.
		line = listMarker.ReplaceAllString(line, "")
		line = strings.ReplaceAll(line, "**", "")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			malformed++
			continue
		}
		s, p, o := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		if s == "" || p == "" || o == "" {
			malformed++
			continue
		}
		out = append(out, palace.ExtractedTriple{Subject: s, Predicate: p, Object: o})
	}
	return out, malformed
}

// windowRunes splits text into consecutive windows of at most n runes. Unlike
// questionGen.ask's cap, nothing is dropped — the whole text is covered, one
// window at a time.
func windowRunes(text string, n int) []string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return nil
	}
	out := make([]string, 0, (len(runes)+n-1)/n)
	for start := 0; start < len(runes); start += n {
		out = append(out, string(runes[start:min(start+n, len(runes))]))
	}
	return out
}
