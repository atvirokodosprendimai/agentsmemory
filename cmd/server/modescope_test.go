package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"

	"github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	cli "github.com/urfave/cli/v3"
)

// knob is one operator setting and the values the sweep tries for it.
type knob struct {
	name   string
	values []string
	apply  func(config.Config, string) config.Config
}

var sweptKnobs = []knob{
	{"--fusion", []string{"linear", "rrf"}, func(c config.Config, v string) config.Config { c.Fusion = v; return c }},
	{"--bm25-weight", []string{"auto", "auto-idf", "0.0", "0.6"}, func(c config.Config, v string) config.Config { c.BM25Weight = v; return c }},
	{"--lex-norm", []string{"page-max", "ceiling", "saturating"}, func(c config.Config, v string) config.Config {
		c.LexNorm = v
		return c
	}},
	{"--closet-boost", []string{"1", "0"}, func(c config.Config, v string) config.Config {
		if v == "0" {
			c.ClosetBoost = 0
		} else {
			c.ClosetBoost = 1
		}
		return c
	}},
	// Swept because the eval arms ArmBlendSigmoid and ArmBlendRank score exactly
	// these values against the served min-max, so an operator changing it can read
	// a row rather than take somebody's word for it.
	{"--rerank-norm", []string{"sigmoid", "minmax", "rank"}, func(c config.Config, v string) config.Config {
		c.RerankNorm = v
		return c
	}},
	{"--rerank-weight", []string{"0.5", "0", "1"}, func(c config.Config, v string) config.Config {
		switch v {
		case "0":
			c.RerankWeight = 0
		case "1":
			c.RerankWeight = 1
		default:
			c.RerankWeight = 0.5
		}
		return c
	}},
	{"--rerank-pool", []string{"50", "3"}, func(c config.Config, v string) config.Config {
		if v == "3" {
			c.RerankPool = 3
		} else {
			c.RerankPool = 50
		}
		return c
	}},
}

// TestModeScopedKnobsAreDiscovered sweeps the ranking knobs over the real wiring
// and reports which are inert under which mode — computed, never declared.
//
// The predicate is TWO-PART, and that is the whole correctness of it:
//
//  1. K is LIVE AT BASELINE — varying K alone from config.Default() changes the
//     result ordering; and
//  2. K is INERT WHEN D IS SET — with D at a non-default value, varying K over
//     the same range changes nothing.
//
// Only both together mean "D scopes K". The one-part version — cell varies D, K
// does not move the output, therefore D scopes K — was the design an independent
// judge selected, and two adversarial reviewers rejected it for the same reason:
// config.Default() leaves RerankURL empty, so the rerank knobs are inert in
// EVERY cell, and a one-part rule charges that to whichever knob the cell
// happened to vary. Thirteen misattributed cells, one of them the shipped stack.
func TestModeScopedKnobsAreDiscovered(t *testing.T) {
	pairs, inertAtBaseline := sweep(t)
	t.Logf("inert at baseline (attributed to NO other knob): %v", inertAtBaseline)
	t.Logf("mode-scoped pairs discovered (%d):\n  %s", len(pairs), strings.Join(pairs, "\n  "))

	// The known case, discovered rather than asserted into existence.
	want := "--bm25-weight is inert when --fusion=rrf"
	found := false
	for _, p := range pairs {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the sweep did not discover %q. rankRRF takes no weight parameter, so this pair "+
			"exists in the code; a sweep that cannot see it is measuring nothing.\n  found: %v", want, pairs)
	}
}

// TestBaselineInertKnobsAreNotAttributed is the ADR's pre-registered
// falsification, and it asserts on the sweep's OUTPUT rather than on its input.
//
// The first version checked that the rerank knobs do not move the ordering at
// baseline — true, and useless: dropping part 1 of the predicate left that check
// green while the pair list went from 1 to 21. It was testing the fact the rule
// is built on instead of the rule. A knob that cannot move anything at baseline
// must appear in NO pair, because every mode would otherwise look like the thing
// that disabled it.
func TestBaselineInertKnobsAreNotAttributed(t *testing.T) {
	pairs, inertAtBaseline := sweep(t)

	if len(inertAtBaseline) == 0 {
		t.Fatal("no knob is inert at baseline, so this check cannot detect a misattribution — " +
			"config.Default() leaves RerankURL empty and at least the rerank knobs should be here")
	}
	for _, name := range inertAtBaseline {
		for _, p := range pairs {
			if strings.HasPrefix(p, name+" ") {
				t.Errorf("%s is inert at BASELINE and was attributed to another knob anyway: %q\n"+
					"That is the one-part predicate two reviewers rejected: it charges a knob's "+
					"own disabled state to whichever mode the cell happened to vary.", name, p)
			}
		}
	}
}

// confirmedPairs are the observed (knob, gate) pairs whose inertness is confirmed
// from the CODE, each with the reason. The sweep can only observe orderings, so a
// pair it reports is a hypothesis until someone names the mechanism.
//
// This is an allowlist, and an allowlist beside the truth is usually the wrong
// shape — it is tolerable here only because an unclassified observation FAILS
// rather than passing quietly, so the list cannot silently fall behind what the
// sweep finds.
var confirmedPairs = map[string]string{
	"--bm25-weight is inert when --fusion=rrf":   "rankRRF takes no weight parameter at all; rank fusion combines positions, so there is no magnitude to weight",
	"--lex-norm is inert when --fusion=rrf":      "same call: rankRRF takes no normaliser either, because there is no lexical magnitude to normalise",
	"--lex-norm is inert when --bm25-weight=0.0": "rankFused multiplies the normalised lexical term by the weight, so at zero the normaliser's output is annihilated before it reaches the fused score",
}

// sweepBaseline is the configuration the sweep varies knobs FROM. It is
// deliberately NOT config.Default().
//
// The sweep's predicate needs a baseline where a knob is live, so that "inert
// under D" means something. Anchoring it to whichever mode currently ships makes
// it blind to that mode's own relationships: the moment FUSION=rrf became the
// default, --bm25-weight and --lex-norm were inert at baseline, the two-part
// predicate could no longer be evaluated for them, and the sweep reported ZERO
// pairs — including the bm25/rrf pair that plainly exists in the code and that
// this file's own assertion names. A discovery tool anchored to the default stops
// discovering exactly when the default becomes interesting.
//
// So it sweeps from the most permissive mode: linear fusion, where every ranking
// knob has something to do.
func sweepBaseline() config.Config {
	c := config.Default()
	// Fusion only. An earlier version also forced ClosetBoost to 1 and the prior
	// promptly swamped the lexical signal, so the sweep reported --bm25-weight as
	// inert under four unrelated knobs — fixture artefacts, not code facts. A
	// baseline should remove exactly the one obstruction it is there to remove.
	c.Fusion = "linear"
	return c
}

// sweep computes the mode-scoped pairs and the knobs that cannot move anything
// at baseline. Both tests read it, so neither can pass on a fact the other
// disproves.
func sweep(t *testing.T) (pairs, inertAtBaseline []string) {
	t.Helper()
	base, queries := sweepFixture(t)
	cfg := sweepBaseline()

	live := map[string]bool{}
	for _, k := range sweptKnobs {
		live[k.name] = knobMoves(t, base, queries, cfg, k)
	}

	for _, k := range sweptKnobs {
		if !live[k.name] {
			inertAtBaseline = append(inertAtBaseline, k.name)
			continue
		}
		for _, d := range sweptKnobs {
			if d.name == k.name {
				continue
			}
			for _, dv := range d.values[1:] { // values[0] is the family's reference value
				// Conditioned from the SAME baseline liveness was measured from.
				// This read config.Default(), which after the 2026-08-21 flip is rrf
				// — so every conditioned cell silently carried rank fusion, both
				// lexical knobs were inert in all of them, and the sweep attributed
				// that to whichever knob the cell happened to vary. It reported
				// "--bm25-weight is inert when --lex-norm=ceiling" and the cause was
				// neither knob. Measuring liveness in one world and inertness in
				// another is not a two-part predicate, it is two unrelated facts.
				cfg := d.apply(sweepBaseline(), dv)
				if !knobMoves(t, base, queries, cfg, k) {
					pairs = append(pairs, fmt.Sprintf("%s is inert when %s=%s", k.name, d.name, dv))
				}
			}
		}
	}
	sort.Strings(pairs)
	sort.Strings(inertAtBaseline)
	return pairs, inertAtBaseline
}

// knobMoves reports whether varying one knob over its range changes the result
// ordering, holding everything else at cfg.
func knobMoves(t *testing.T, base *palace.Service, queries []string, cfg config.Config, k knob) bool {
	t.Helper()
	var first []string
	for i, v := range k.values {
		// Clone per cell. With* mutates, so reusing one Service would carry each
		// cell's settings into the next and make every knob look inert.
		svc, _ := configureRanking(base.Clone(), k.apply(cfg, v), func(string, time.Duration) palace.Reranker {
			return nil
		})
		got := orderingFor(t, svc, queries)
		if i == 0 {
			first = got
			continue
		}
		if strings.Join(got, "|") != strings.Join(first, "|") {
			return true
		}
	}
	return false
}

func orderingFor(t *testing.T, svc *palace.Service, queries []string) []string {
	t.Helper()
	var out []string
	for _, q := range queries {
		hits, err := svc.Search(context.Background(), "team-sweep", palace.SearchQuery{
			Query: q, Limit: 8, SkipTelemetry: true,
		})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		for _, h := range hits {
			out = append(out, h.Drawer.ID)
		}
		out = append(out, "|")
	}
	return out
}

// sweepFixture seeds a corpus large enough that ranking can differ between
// configurations. Below limit*hybridCandidateMultiplier every cell sees the same
// candidates and no knob can move anything — a sweep over such a corpus reports
// every knob inert and looks like a finding.
func sweepFixture(t *testing.T) (*palace.Service, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sweep.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Closed because the handle is the TEST's, not the process's. POSIX unlinks
	// an open file so this leaks invisibly here; Windows refuses, and t.TempDir
	// registers RemoveAll at call time, so every test using the helper fails in
	// cleanup with its assertions passing (#162). Cleanup is LIFO, so this runs
	// before TempDir's own.
	t.Cleanup(func() { _ = sqlDB.Close() })

	svc := palace.NewService(palace.NewRepo(gdb, gdb), sweepEmbedder{}, sqlitevec.New(gdb), sweepDim)

	// Deliberate lexical overlap between documents, so BM25 and vector disagree
	// and a lexical weight has something to change.
	texts := []string{
		"the retry budget is three attempts because a fourth exceeds the upstream timeout",
		"the upstream timeout is thirty seconds and is owned by another team",
		"retry backoff is exponential and capped at the retry budget",
		"the queue worker drains in batches and retries with backoff",
		"batch size is tuned to the upstream timeout rather than to throughput",
		"a fourth retry attempt was measured to exceed the timeout in production",
		"the timeout budget is split between connect and read",
		"connect timeouts are retried, read timeouts are not",
		"the worker retries on connect failure and gives up on read failure",
		"throughput is bounded by batch size and by the retry budget together",
		"deploys roll one node at a time to keep the queue drained",
		"a rolling deploy overlaps with the retry window and can double-count",
		"double counting is prevented by an idempotency key on each batch",
		"the idempotency key is derived from the batch contents",
		"batch contents are hashed before the key is derived",
		"hashing is stable across processes so the key is reproducible",
		"reproducible keys let a retried batch be recognised downstream",
		"downstream recognises a duplicate batch and drops it",
		"dropped duplicates are counted but not logged individually",
		"individual logging was removed because it dominated the log volume",
		"log volume is bounded by sampling rather than by level",
		"sampling keeps one in a hundred duplicate events",
		"one in a hundred was chosen to keep the signal visible at low cost",
		"cost here is log storage rather than compute",
	}
	for i, txt := range texts {
		if _, err := svc.Add(context.Background(), "team-sweep", palace.AddInput{
			Wing: "wing_sweep", Room: "decisions", Content: fmt.Sprintf("%s (note %d)", txt, i),
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	return svc, []string{
		"why is the retry capped",
		"what bounds throughput",
		"how are duplicate batches handled",
	}
}

const sweepDim = 8

// sweepEmbedder is deterministic and content-derived, so an ordering difference
// between cells comes from the ranking configuration and from nothing else.
type sweepEmbedder struct{}

func (sweepEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, s := range inputs {
		v := make([]float32, sweepDim)
		for j, r := range s {
			v[j%sweepDim] += float32(r%13) / 12
		}
		out[i] = v
	}
	return out, nil
}

func (e sweepEmbedder) EmbedOne(ctx context.Context, in string) ([]float32, error) {
	v, err := e.Embed(ctx, []string{in})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

// TestDiscoveredPairsAdmitTheirCondition closes the loop T2 opens: a pair the
// sweep discovers is a knob that silently does nothing under some mode, and the
// only fix an operator can act on is being told so where they read.
//
// The admission must name the gating knob GREPPABLY. That is deliberate
// strictness: prose counts here only when it is mechanically checkable, which is
// the same line this repository already holds for environment variables. An
// honest sentence phrased without the gating knob's name fails, and that is the
// trade — the alternative is a check that accepts text it cannot verify.
func TestDiscoveredPairsAdmitTheirCondition(t *testing.T) {
	pairs, _ := sweep(t)
	if len(pairs) == 0 {
		t.Fatal("the sweep discovered no pairs, so this check has nothing to enforce — " +
			"--bm25-weight under --fusion=rrf exists in the code and should have been found")
	}
	enforced := 0

	flags := serveFlags(config.Default())

	observed := 0
	for _, p := range pairs {
		// "--bm25-weight is inert when --fusion=rrf"
		knobName, gate := parsePair(t, p)
		// A pair the sweep OBSERVES is not yet a pair the code guarantees. The
		// predicate measures orderings on one fixture, so "K did not move the top
		// eight while D was set" also happens when D merely shrinks K's effect
		// below the resolution of this corpus.
		//
		// An observed pair must be CLASSIFIED, because the predicate alone cannot
		// tell "the code ignores K under D" from "D shrank K's effect below this
		// corpus's resolution". Logging the unclassified ones let a real pair ship
		// undocumented; failing on them forces the question to be answered once.
		why, confirmed := confirmedPairs[p]
		if !confirmed {
			t.Errorf("the sweep observed %q and nothing classifies it.\n"+
				"  Either confirm it from the code — name the parameter the selected path drops — "+
				"and add it to confirmedPairs, or establish that the fixture merely could not "+
				"separate the two and say so there. An observation nobody classified is how a real "+
				"inert knob ships undocumented.", p)
			continue
		}
		_ = why
		enforced++
		usage := flagUsage(t, flags, knobName)
		if usage == "" {
			t.Errorf("no Usage string found for %s", knobName)
			continue
		}
		if !mentions(usage, gate) {
			t.Errorf("%s does nothing when %s is set, and its --help says nothing about it:\n  %q\n"+
				"  An operator who sets it gets no behaviour change and no explanation. Naming the "+
				"gating knob is the whole remedy.", knobName, gate, usage)
		}
	}
	if enforced == 0 {
		t.Errorf("%d pair(s) observed and none code-confirmed — the only structurally confirmable "+
			"gate (--fusion) produced nothing, so this check enforced nothing at all", observed)
	}
}

// TestStartupDoesNotContradictItself: under rrf the wiring announces that the
// bm25 weight does not apply, and then announced the bm25 weight anyway. Two
// lines, adjacent, disagreeing — a reader believes whichever they read second.
func TestStartupDoesNotContradictItself(t *testing.T) {
	cfg := config.Default()
	cfg.Fusion = "rrf"
	cfg.BM25Weight = "auto-idf"

	_, lines := configureRanking(bareService(), cfg, noReranker)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "do not apply") {
		t.Fatalf("rrf did not announce that the bm25 weight is inert:\n%s", joined)
	}
	if strings.Contains(joined, "bm25 weight: auto (IDF-weighted coverage)") {
		t.Errorf("startup says the bm25 weight does not apply and then reports one:\n%s\n"+
			"A reader believes whichever line they read second.", joined)
	}
	if strings.Contains(joined, "lex-weight=auto") || strings.Contains(joined, "lex-weight=auto-idf") {
		t.Errorf("resolved profile still claims a lexical weight under rrf:\n%s", joined)
	}
	if !strings.Contains(joined, "lex-weight=n/a") {
		t.Errorf("resolved profile did not mark the lexical weight as n/a under rrf:\n%s", joined)
	}
}

// parsePair splits "--knob is inert when --gate=value" into its two flag names.
func parsePair(t *testing.T, pair string) (knob, gate string) {
	t.Helper()
	parts := strings.SplitN(pair, " is inert when ", 2)
	if len(parts) != 2 {
		t.Fatalf("unparseable pair %q", pair)
	}
	gate = parts[1]
	if i := strings.Index(gate, "="); i >= 0 {
		gate = gate[:i]
	}
	return parts[0], gate
}

// flagUsage returns a flag's Usage text from the flags the command actually
// builds, not from the source text of main.go.
//
// The string-scanning version this replaced was satisfied by dead text: a line
// reading `// Name: "bm25-weight", Usage: "unused --fusion"` anywhere earlier in
// the file passed the admission while the real --help stayed silent. Which is the
// same defect the admission exists to prevent, one level up — a promise that is
// present in the source and absent from the surface an operator reads.
func flagUsage(t *testing.T, flags []cli.Flag, flag string) string {
	t.Helper()
	name := strings.TrimPrefix(flag, "--")
	for _, f := range flags {
		for _, n := range f.Names() {
			if n != name {
				continue
			}
			d, ok := f.(cli.DocGenerationFlag)
			if !ok {
				t.Fatalf("--%s carries no usage text at all", name)
			}
			return d.GetUsage()
		}
	}
	return ""
}

// mentions accepts the flag spelling or the environment spelling, since an
// operator may know a knob by either.
func mentions(text, flag string) bool {
	name := strings.TrimPrefix(flag, "--")
	env := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	return strings.Contains(text, "--"+name) || strings.Contains(text, env)
}

// unobservableKnobs are the ranking settings the sweep cannot move, each with
// the reason it is silent. The list exists so that silence is DECLARED: a knob
// absent from both the sweep and this map fails TestEveryKnobIsSweptOrNamed
// rather than passing unnoticed, which is how a setting joins the wiring and
// nobody ever measures whether it does anything.
var unobservableKnobs = map[string]string{
	"RerankURL":              "selects a live cross-encoder service; with none reachable the sweep measures the absence, not the setting",
	"RerankTimeout":          "bounds a call the sweep never makes, so every value produces the same ordering in-process",
	"HTTPTimeout":            "bounds outbound calls to the vector and embedding backends, which the fixture serves locally",
	"MemoryEvidenceSelector": "changes cross-encoder documents only for long memories; the ordering sweep has no live reranker, while evidence_test observes those documents directly",
	"RetrieveK":              "widens the vector fetch; the sweep's 24-document fixture is already fully retrieved at limit×3, so every retrieve-k produces the same page. The eval's production retrieve-k arm and TestSearchRetrieveKWidensTheFetch observe the fetch itself",
}

// TestEveryKnobIsSweptOrNamed: every config field the ranking wiring READS is
// either swept for mode-scoping or listed as unobservable with a reason.
//
// The universe is read out of configureRanking's own body rather than declared
// beside it, because a list kept beside the truth is a thing somebody has to
// remember. A field that joins the wiring joins this check on the same commit.
//
// The mapping from knob to field is EXECUTED, not asserted: each knob's apply
// runs against a zero Config and the fields it moves are the fields it owns. A
// knob whose Usage claims one setting while its apply moves another cannot pass
// by naming itself correctly.
func TestEveryKnobIsSweptOrNamed(t *testing.T) {
	universe := fieldsReadBy(t, "main.go", "configureRanking")
	if len(universe) == 0 {
		t.Fatal("no cfg.* reads found in configureRanking — the extraction that this " +
			"check reads has moved, so it is measuring nothing")
	}

	covered := map[string]string{}
	for _, k := range sweptKnobs {
		for _, f := range fieldsMovedBy(k) {
			covered[f] = "swept as " + k.name
		}
	}
	for f, why := range unobservableKnobs {
		if why == "" {
			t.Errorf("%s is listed as unobservable with no reason; the list is a declaration, not a mute button", f)
		}
		if prior, dup := covered[f]; dup {
			t.Errorf("%s is both %s and listed as unobservable — one of the two is wrong", f, prior)
		}
		covered[f] = "declared unobservable"
	}

	for _, f := range universe {
		if _, ok := covered[f]; !ok {
			t.Errorf("configureRanking reads cfg.%s, but no knob sweeps it and nothing declares it "+
				"unobservable.\n  An operator can set it and nobody has measured whether it changes "+
				"anything. Add it to sweptKnobs, or to unobservableKnobs with the reason the sweep "+
				"is silent.", f)
		}
	}

	inUniverse := map[string]bool{}
	for _, f := range universe {
		inUniverse[f] = true
	}
	for f := range unobservableKnobs {
		if !inUniverse[f] {
			t.Errorf("%s is declared unobservable but the ranking wiring no longer reads it; "+
				"a stale excuse outlives the thing it excused", f)
		}
	}
}

// fieldsReadBy returns the config.Config field names a named function reads,
// parsed out of the AST rather than scanned out of the text.
//
// The string version this replaced ended the body at the first column-0 `}`,
// which a raw literal inside the function silently moves — and a truncated
// universe UNDER-reports, so the gate would go quiet exactly when a knob is
// added past the truncation point. A bound that truncates its own sweep is a
// defect this repository has shipped before.
//
// The config parameter is found by TYPE, not by the name `cfg`, so renaming it
// cannot make the scan read nothing and report a clean sweep.
func fieldsReadBy(t *testing.T, path, fn string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var decl *ast.FuncDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == fn {
			decl = f
			break
		}
	}
	if decl == nil {
		t.Fatalf("no func %s in %s — the extraction this check reads has moved", fn, path)
	}

	param := ""
	for _, f := range decl.Type.Params.List {
		sel, ok := f.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Config" {
			continue
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "config" && len(f.Names) > 0 {
			param = f.Names[0].Name
		}
	}
	if param == "" {
		t.Fatalf("%s takes no config.Config parameter, so this check has nothing to read", fn)
	}

	seen := map[string]bool{}
	var out []string
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != param || seen[sel.Sel.Name] {
			return true
		}
		seen[sel.Sel.Name] = true
		out = append(out, sel.Sel.Name)
		return true
	})
	sort.Strings(out)
	return out
}

// fieldsMovedBy runs a knob's apply over every value it sweeps and reports which
// config fields actually changed. Running it is the point: a knob is bound to the
// field it MOVES, not to the field its name suggests.
func fieldsMovedBy(k knob) []string {
	var zero config.Config
	seen := map[string]bool{}
	var out []string
	rz := reflect.ValueOf(zero)
	for _, v := range k.values {
		rv := reflect.ValueOf(k.apply(zero, v))
		for i := 0; i < rv.NumField(); i++ {
			name := rv.Type().Field(i).Name
			if seen[name] {
				continue
			}
			if !reflect.DeepEqual(rv.Field(i).Interface(), rz.Field(i).Interface()) {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}
