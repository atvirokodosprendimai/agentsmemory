package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/urfave/cli/v3"
)

// evalProject parses args through the real eval flag set and returns the
// workspace slug runEval would resolve against, so this pins the actual flag
// wiring rather than a re-implementation of it.
func evalProject(t *testing.T, args ...string) string {
	t.Helper()
	cmd := evalCommand(config.Default())
	var got string
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		got = c.String("project")
		return nil
	}
	if err := cmd.Run(context.Background(), append([]string{"eval"}, args...)); err != nil {
		t.Fatalf("run: %v", err)
	}
	return got
}

// TestEvalNamesItsWorkspace is a regression test for eval being the one
// subcommand that could not measure a multi-tenant palace. It resolved through
// EnsureLocalWorkspace, whose contract is "exactly one workspace exists and it is
// slugged local" — so a database holding any other workspace (the seeded demo
// team is enough) failed with ErrForeignWorkspace before a single drawer was
// read, and no flag or environment variable could reach the real corpus.
//
// That guard exists to stop an UNAUTHENTICATED /mcp serving a workspace it did
// not provision. eval is a read-only CLI against a database file the caller
// already possesses, which is the same trust model as `wing export` and
// `inspect` — both of which name their workspace by slug.
func TestEvalNamesItsWorkspace(t *testing.T) {
	if got := evalProject(t, "--project", "acme"); got != "acme" {
		t.Errorf("--project acme resolved to %q, want acme", got)
	}
}

// TestEvalDefaultsToTheLocalWorkspace keeps the self-hoster's zero-typing path:
// naming a workspace must be what a multi-tenant operator does, never a new step
// for someone running `--local`.
func TestEvalDefaultsToTheLocalWorkspace(t *testing.T) {
	if got := evalProject(t); got != tenant.LocalSlug {
		t.Errorf("default project = %q, want %q", got, tenant.LocalSlug)
	}
}

// evalGen parses args through the real eval flag set and reports where the
// question generator would point, plus the model it would ask for.
func evalGen(t *testing.T, args ...string) (url, model string) {
	t.Helper()
	cmd := evalCommand(config.Default())
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		url, model = genURL(c), c.String("gen-model")
		return nil
	}
	if err := cmd.Run(context.Background(), append([]string{"eval"}, args...)); err != nil {
		t.Fatalf("run: %v", err)
	}
	return url, model
}

// TestEvalGeneratorFollowsTheEmbedderByDefault pins the single-machine path: one
// Ollama, nothing to configure. --gen-url exists to SEPARATE the two, so leaving
// it unset must not require setting it.
func TestEvalGeneratorFollowsTheEmbedderByDefault(t *testing.T) {
	url, model := evalGen(t, "--ollama-url", "http://box:11434")
	if url != "http://box:11434" {
		t.Errorf("gen url = %q, want it to follow --ollama-url", url)
	}
	if model != "qwen2.5-coder:7b" {
		t.Errorf("gen model = %q, want the documented default", model)
	}
}

// TestEvalGeneratorCanLeaveTheEmbedderBehind is the point of --gen-url: sending a
// one-off burst of question generation to a bigger or hosted model must not drag
// the embedder along with it, because the vectors stay where the data is.
func TestEvalGeneratorCanLeaveTheEmbedderBehind(t *testing.T) {
	url, model := evalGen(t,
		"--ollama-url", "http://localhost:11434",
		"--gen-url", "https://ollama.com",
		"--gen-model", "qwen3-coder:480b-cloud",
	)
	if url != "https://ollama.com" {
		t.Errorf("gen url = %q, want the override to win over --ollama-url", url)
	}
	if model != "qwen3-coder:480b-cloud" {
		t.Errorf("gen model = %q, want the override", model)
	}
}

// TestEvalGeneratorSendsItsBearerToken covers the reason --gen-api-key exists:
// hosted Ollama rejects an unauthenticated call, and a local one ignores the
// header, so sending it whenever it is set is both necessary and harmless.
func TestEvalGeneratorSendsItsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"response":"why did the deploy fail?"}`))
	}))
	defer srv.Close()

	gen := &questionGen{
		url:    srv.URL,
		model:  "m",
		apiKey: "secret-token",
		prompt: "%s",
		http:   srv.Client(),
	}
	if _, err := gen.ask(context.Background(), "a note"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}

	// And absent when unset: a local Ollama needs no credential, and inventing an
	// empty Bearer header is the kind of thing a strict proxy rejects.
	gen.apiKey = ""
	if _, err := gen.ask(context.Background(), "a note"); err != nil {
		t.Fatalf("ask without key: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q with no key set, want it absent", gotAuth)
	}
}

// closetReport is a two-category synthetic run: enough to render the block
// without standing up a palace, an embedder and a generator model.
func closetReport() palace.EvalReport {
	return palace.EvalReport{Details: []palace.EvalCaseResult{
		{Query: "paraphrase one", Category: palace.CatSingle, PoolRank: 3,
			Ranks: map[palace.EvalArm]int{palace.ArmHybrid: 4, palace.ArmHybridCloset: 1}},
		{Query: "paraphrase two", Category: palace.CatSingle, PoolRank: 2,
			Ranks: map[palace.EvalArm]int{palace.ArmHybrid: 1, palace.ArmHybridCloset: 3}},
		{Query: "paraphrase unreachable", Category: palace.CatSingle, PoolRank: 0,
			Ranks: map[palace.EvalArm]int{palace.ArmHybrid: 0, palace.ArmHybridCloset: 0}},
		{Query: "real one", Category: palace.CatReal, PoolRank: 1,
			Ranks: map[palace.EvalArm]int{palace.ArmHybrid: 1, palace.ArmHybridCloset: 4}},
	}}
}

// TestEvalPrintsPreselectedClosetDelta pins that the block says what it is.
//
// A reader who sees a delta next to an interval will assume it was chosen the
// same way the rest of the table's verdicts were — against a best arm picked
// from the same data. This one was named before the run, and the caption has to
// say so, or the number gets quoted with the wrong weight behind it. The
// exclusions are printed for the same reason: a delta over an unstated subset
// is a number nobody can check.
func TestEvalPrintsPreselectedClosetDelta(t *testing.T) {
	var buf strings.Builder
	printClosetBlock(&buf, closetReport())
	got := buf.String()

	for _, want := range []string{
		"hybrid+closet", "hybrid",
		string(palace.CatSingle), string(palace.CatReal),
		"preselected",
		"admitted", "unreachable", "moved",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the closet block never mentions %q — a reader cannot tell what went into the number\n%s", want, got)
		}
	}
	// The counts live in columns, so check the row's fields rather than looking
	// for the word next to the number.
	var row []string
	for _, line := range strings.Split(got, "\n") {
		if f := strings.Fields(line); len(f) > 3 && f[0] == string(palace.CatSingle) {
			row = f
		}
	}
	if row == nil {
		t.Fatalf("no %s row in the block:\n%s", palace.CatSingle, got)
	}
	if row[1] != "2" {
		t.Errorf("%s row reports %s admitted, want 2:\n%s", palace.CatSingle, row[1], got)
	}
	if row[2] != "1" {
		t.Errorf("%s row reports %s unreachable, want 1 — the excluded case is invisible:\n%s", palace.CatSingle, row[2], got)
	}
}

// TestRunRecordCarriesProvenanceAndNoCaseText pins both halves of the cells
// file: it records what produced the numbers, and it records nothing that came
// out of the palace.
//
// The evidence directory is committed to a public repository while the palace it
// was measured on is private. A run record that carries queries or drawer ids
// cannot be committed at all, and a run record that does not say which commit,
// which closet scale and which ranking config produced it cannot be compared
// with the next one — which is how two runs of "the same" eval have already
// disagreed for reasons invisible afterwards.
func TestRunRecordCarriesProvenanceAndNoCaseText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.cells.json")
	meta := caseFileMeta{Generator: "qwen2.5-coder:7b", Style: "paraphrase", Wing: "wing_acme", Corpus: 4120}

	if err := writeCells(path, closetReport(), meta, cellsConfig{
		Pool: 50, Cases: 4, ClosetScale: 0, BM25Weight: "0.40",
		RerankConfigured: true, RerankWeight: 0.5, RerankPool: 50,
	}); err != nil {
		t.Fatalf("writeCells: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("the cells file is not valid JSON: %v", err)
	}
	for _, key := range []string{"commit", "dirty", "style", "wing", "generator", "corpus_drawers", "cases", "pool", "closet_scale", "bm25_weight", "rerank_configured", "rerank_weight", "rerank_pool", "ranking", "case_set_id", "case_set_origin", "cells"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("the run record has no %q; two runs of this eval cannot be compared without it", key)
		}
	}
	if cells, ok := rec["cells"].([]any); !ok || len(cells) == 0 {
		t.Error("the run record carries no cells; the evidence directory would hold a file with no evidence in it")
	}

	// Nothing from the palace may appear anywhere in the file.
	text := string(raw)
	for _, leaked := range []string{"paraphrase one", "paraphrase two", "real one", "paraphrase unreachable"} {
		if strings.Contains(text, leaked) {
			t.Errorf("the run record contains the case query %q; this file is committed and the palace is not", leaked)
		}
	}
}

// TestReadCasesKeepsProvenance pins that replaying a case file does not lose the
// record of how its questions were written.
//
// readCases skipped the meta line entirely, so a replayed run knew its own
// --style flag and nothing about the generator that actually produced the
// questions. Two machines running "the same" eval with different generators have
// already disagreed for exactly that reason.
func TestReadCasesKeepsProvenance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cases.jsonl")
	want := caseFileMeta{Generator: "qwen2.5-coder:7b", Style: "temporal", Wing: "wing_acme", Corpus: 4120, Created: "2026-08-20T00:00:00Z"}
	if err := writeCases(path, []palace.EvalCase{{Query: "q", Expect: "d1"}}, want); err != nil {
		t.Fatalf("writeCases: %v", err)
	}

	cases, got, err := readCasesWithMeta(path)
	if err != nil {
		t.Fatalf("readCasesWithMeta: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("read %d cases, want 1 — the meta line must not be read as a case", len(cases))
	}
	if got.Generator != want.Generator || got.Style != want.Style || got.Corpus != want.Corpus {
		t.Errorf("provenance lost on replay: got %+v, want generator/style/corpus from %+v", got, want)
	}
}

// judgeSaying stands in for the local model: it replies with the same verdict to
// every prompt, or fails, so verifyPair can be exercised without one.
func judgeSaying(t *testing.T, reply string, fail bool) *questionGen {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "judge is down", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":` + strconvQuote(reply) + `}`))
	}))
	t.Cleanup(srv.Close)
	return &questionGen{url: srv.URL, model: "test", prompt: evalPromptPairCheck, http: srv.Client()}
}

func strconvQuote(s string) string { return `"` + s + `"` }

// TestPairVerifiedRejectsDrift pins that a pair the judge does not confirm is
// DROPPED rather than kept.
//
// OlderNeighbor's filters say what a pair is not — not itself, not the same
// source, not newer — and the distance ceiling says the two are close. Neither
// says the older note records an EARLIER STATE of the same fact rather than a
// different fact that happens to be nearby. Only a judge can say that, and a
// pair it declines is not a temporal case: scoring it would measure the ranker
// against a supersession that never happened.
func TestPairVerifiedRejectsDrift(t *testing.T) {
	ctx := context.Background()
	newer := palace.Drawer{Content: "The retention window is ninety days.", ContentDate: "2026-01-01"}
	older := palace.Drawer{Content: "Kubernetes schedules pods using taints.", ContentDate: "2024-01-01"}

	ok, err := verifyPair(ctx, newer, older, judgeSaying(t, "NO — different subjects", false))
	if err != nil {
		t.Fatalf("verifyPair: %v", err)
	}
	if ok {
		t.Error("a pair the judge declined was accepted — the ranker would then be scored against " +
			"a supersession that never happened")
	}

	ok, err = verifyPair(ctx, newer, older, judgeSaying(t, "YES", false))
	if err != nil {
		t.Fatalf("verifyPair: %v", err)
	}
	if !ok {
		t.Error("a pair the judge confirmed was rejected")
	}
}

// TestPairVerifiedJudgeErrorDropsPair pins that a judge that cannot answer drops
// the pair rather than keeping it.
//
// The failure mode this avoids is the one verifyAbsent already learned: an
// unreachable checker that silently returns "fine" fills the case file with
// unverified cases that look identical to verified ones. Unknown is not
// confirmed.
func TestPairVerifiedJudgeErrorDropsPair(t *testing.T) {
	ctx := context.Background()
	newer := palace.Drawer{Content: "a", ContentDate: "2026-01-01"}
	older := palace.Drawer{Content: "b", ContentDate: "2024-01-01"}

	ok, err := verifyPair(ctx, newer, older, judgeSaying(t, "", true))
	if err == nil {
		t.Error("a judge that could not answer returned no error — the caller cannot tell an " +
			"unverified pair from a confirmed one")
	}
	if ok {
		t.Error("a pair was accepted while its check failed")
	}
}

// TestPairVerifiedMetaSurvivesRead pins that how a case file's pairs were made
// survives a replay: how many candidates were considered, how many the judge
// confirmed, and which judge it was.
//
// Without it a replayed temporal run cannot tell a file whose pairs were verified
// from one generated before verification existed, and the two produce different
// numbers from the same command.
func TestPairVerifiedMetaSurvivesRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pairs.jsonl")
	want := caseFileMeta{
		Generator: "qwen2.5-coder:7b", Style: "temporal", Wing: "wing_acme",
		PairCandidates: 40, VerifiedPairs: 11, Judge: "qwen2.5-coder:7b",
	}
	if err := writeCases(path, []palace.EvalCase{{Query: "q", Expect: "d1", Distractor: "d0"}}, want); err != nil {
		t.Fatalf("writeCases: %v", err)
	}
	_, got, err := readCasesWithMeta(path)
	if err != nil {
		t.Fatalf("readCasesWithMeta: %v", err)
	}
	if got.PairCandidates != want.PairCandidates || got.VerifiedPairs != want.VerifiedPairs || got.Judge != want.Judge {
		t.Errorf("pair provenance lost on replay: got %+v, want candidates=%d verified=%d judge=%q",
			got, want.PairCandidates, want.VerifiedPairs, want.Judge)
	}
}

// TestSupersessionGateRefusesUnhardenedCases pins that the gate refuses rather
// than answers when its evidence is not what it requires.
//
// Three refusals, each naming its own cause, because "the gate said no" is
// useless if the operator cannot tell a thin corpus from a broken setup. The
// floor is on pairs that are BOTH judge-verified and non-vacuous in THIS run —
// never the generation-time verified_pairs integer, which knows nothing about
// the pool this run used and so counts cases no arm could have ranked.
func TestSupersessionGateRefusesUnhardenedCases(t *testing.T) {
	cell := palace.SupersessionCell{Scope: palace.ScopePool, Cases: 40, StaleAbove: 30}

	t.Run("too few usable pairs", func(t *testing.T) {
		thin := palace.SupersessionCell{Scope: palace.ScopePool, Cases: 4, StaleAbove: 3}
		if err := supersessionGateReady(thin, caseFileMeta{VerifiedPairs: 40}, defaultEvalPool); err == nil {
			t.Error("the gate answered on 4 usable pairs — the floor is on pairs verified AND " +
				"non-vacuous in this run, not on what the generator once wrote")
		} else if !strings.Contains(err.Error(), "--pool") && !strings.Contains(err.Error(), "corpus") {
			t.Errorf("the refusal must point at the corpus or the pool, not at the bar: %v", err)
		}
	})

	t.Run("case file was never hardened", func(t *testing.T) {
		if err := supersessionGateReady(cell, caseFileMeta{}, defaultEvalPool); err == nil {
			t.Error("the gate answered on a case file with no verification record")
		} else if !strings.Contains(err.Error(), "--style temporal") {
			// The task said to name --verify-pairs. That flag does not exist:
			// T2 wired pair verification unconditionally into temporal
			// generation, so there is nothing to opt into and naming a flag that
			// is not there is the defect this branch spent itself closing. The
			// refusal names what actually fixes it — regenerating the file.
			t.Errorf("the refusal must name what actually fixes it: %v", err)
		}
	})

	t.Run("hardened and sufficient", func(t *testing.T) {
		if err := supersessionGateReady(cell, caseFileMeta{VerifiedPairs: 40, Judge: "qwen"}, defaultEvalPool); err != nil {
			t.Errorf("a hardened file with enough usable pairs must be accepted: %v", err)
		}
	})
}

// TestSupersessionGateIgnoresPageScopedArms pins that the gate reads the arm it
// was pre-registered against and refuses when that arm is absent.
//
// Substituting the nearest available arm is exactly the selection this task
// exists to remove: a degraded run drops the reranked arms, and gating whatever
// is left answers a different question under the same name.
func TestSupersessionGateIgnoresPageScopedArms(t *testing.T) {
	report := palace.EvalReport{Arms: []palace.EvalMetrics{
		{Arm: palace.ArmProduction, Supersession: palace.SupersessionCell{Scope: palace.ScopePage, Cases: 40, StaleAbove: 0}},
		{Arm: palace.ArmHybrid, Supersession: palace.SupersessionCell{Scope: palace.ScopePool, Cases: 40, StaleAbove: 1}},
	}}
	want := palace.SupersessionGatedArm()
	if _, err := gatedArmCell(report, want); err == nil {
		t.Error("the gate accepted a report without its pre-registered arm — a page-scoped or " +
			"merely-available arm answers a different question under the same name")
	}
	if _, err := gatedArmCell(report, ""); err == nil {
		t.Error("the gate answered for a served ranking no arm reconstructs — naming the nearest arm " +
			"is how it came to judge a configuration nobody ran")
	}
	report.Arms = append(report.Arms, palace.EvalMetrics{
		Arm: want, Supersession: palace.SupersessionCell{Scope: palace.ScopePool, Cases: 40, StaleAbove: 30}})
	got, err := gatedArmCell(report, want)
	if err != nil {
		t.Fatalf("the gated arm is present and was refused: %v", err)
	}
	if got.StaleAbove != 30 {
		t.Errorf("the gate read the wrong arm: StaleAbove=%d, want 30 (the pre-registered arm's)", got.StaleAbove)
	}
}

// TestCaseSetIDIsContentDerived pins the identity of a run's questions.
//
// Four n=30 runs were taken without --cases. Each sampled its own drawers and
// generated its own questions, and each table labelled a BEST arm with nothing
// saying the questions had changed between them. The labels moved, and were read
// — in this repository — as three tables agreeing on a configuration. They were
// three samples.
//
// The id has to be derived from the CONTENT of the case set, not from the file,
// its path or the order the cases happen to sit in: a file hash makes replaying
// a saved run produce a different id from the run that wrote it, which trains
// people to ignore the field.
func TestCaseSetIDIsContentDerived(t *testing.T) {
	set := []palace.EvalCase{
		{Query: "what is the rerank pool", Expect: "d1", Wing: "wing_acme", Category: palace.CatSingle},
		{Query: "which fusion ships by default", Expect: "d2", ExpectAny: []string{"d2", "d7"}},
		{Query: "nothing knows this", Category: palace.CatAbsent},
	}
	id := palace.CaseSetID(set)
	if id == "" {
		t.Fatal("a case set has no id at all")
	}

	shuffled := []palace.EvalCase{set[2], set[0], set[1]}
	if got := palace.CaseSetID(shuffled); got != id {
		t.Errorf("reordering one case set changed its id: %q vs %q — two orderings of the same questions are the same questions", got, id)
	}

	// nil and empty must canonicalise identically: ExpectAny is `json:",omitempty"`,
	// so an empty slice is written as absent and read back nil. Without this,
	// every replay of a saved file produces a different id from the run that
	// wrote it.
	empty := append([]palace.EvalCase(nil), set...)
	empty[0].ExpectAny = []string{}
	if got := palace.CaseSetID(empty); got != id {
		t.Errorf("an empty ExpectAny hashed differently from a nil one (%q vs %q) — every replayed run would disagree with the run that saved it", got, id)
	}

	changed := append([]palace.EvalCase(nil), set...)
	changed[0].Query = "what is the rerank pool?"
	if got := palace.CaseSetID(changed); got == id {
		t.Error("changing a question did not change the id — the id would say two different case sets are one")
	}

	// The invariant that actually breaks silently: save, replay, recompute.
	dir := t.TempDir()
	path := filepath.Join(dir, "cases.jsonl")
	if err := writeCases(path, set, caseFileMeta{Generator: "g", Style: "paraphrase", Wing: "wing_acme"}); err != nil {
		t.Fatalf("writeCases: %v", err)
	}
	back, _, err := readCasesWithMeta(path)
	if err != nil {
		t.Fatalf("readCasesWithMeta: %v", err)
	}
	if got := palace.CaseSetID(back); got != id {
		t.Errorf("replaying a saved case file produced id %q, but the run that wrote it had %q", got, id)
	}
}

// TestGeneratedRunSaysSoBesideBest pins the caveat to where the reader is.
//
// A BEST computed over questions nobody else will ever see is a claim about one
// sample and reads as a claim about the system. The label is what gets quoted,
// so the case set it is best over is printed on that line — a header the reader
// scrolled past does not carry it.
func TestGeneratedRunSaysSoBesideBest(t *testing.T) {
	report := palace.EvalReport{
		CaseSetID:     "cs-deadbeef01",
		CaseSetOrigin: palace.CaseSetGenerated,
		Arms: []palace.EvalMetrics{
			{Arm: palace.ArmHybrid, MRR: 0.42, Ranks: []int{1, 2, 3}},
			{Arm: palace.ArmRRF, MRR: 0.61, Ranks: []int{1, 1, 2}},
		},
	}
	var buf strings.Builder
	printEvalTable(&buf, report)

	var bestLine string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "BEST") {
			bestLine = line
			break
		}
	}
	if bestLine == "" {
		t.Fatalf("the table printed no BEST row, so this test asserts on nothing:\n%s", buf.String())
	}
	for _, want := range []string{palace.CaseSetGenerated, "cs-deadbeef01"} {
		if !strings.Contains(bestLine, want) {
			t.Errorf("the BEST line does not carry %q: %q\nA winner over questions nobody else has reads as a winner over the system.", want, bestLine)
		}
	}

	// A replayed run names its case set too, but must not be labelled generated.
	report.CaseSetOrigin = palace.CaseSetReplayed
	buf.Reset()
	printEvalTable(&buf, report)
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "BEST") && strings.Contains(line, palace.CaseSetGenerated) {
			t.Errorf("a replayed run's BEST line claims the questions were generated: %q", line)
		}
	}
}

// TestRunRecordCarriesTheCaseSet pins that the id reaches disk, not only the
// terminal, and that the record and the table cannot disagree about it.
func TestRunRecordCarriesTheCaseSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.cells.json")
	report := closetReport()
	report.CaseSetID = "cs-0123456789ab"
	report.CaseSetOrigin = palace.CaseSetReplayed

	if err := writeCells(path, report, caseFileMeta{Generator: "g", Style: "paraphrase", Wing: "wing_acme"}, cellsConfig{
		Pool: 50, Cases: 4, Ranking: "fusion=rrf lex-weight=auto lex-norm=page-max closet-boost=0.00 rerank=off unit=chunk evidence=lexical",
	}); err != nil {
		t.Fatalf("writeCells: %v", err)
	}
	var rec map[string]any
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("the cells file is not valid JSON: %v", err)
	}
	if got := rec["case_set_id"]; got != report.CaseSetID {
		t.Errorf("the run record says case_set_id %v, the table says %q — the two cannot be allowed to disagree", got, report.CaseSetID)
	}
	if got := rec["case_set_origin"]; got != report.CaseSetOrigin {
		t.Errorf("the run record says case_set_origin %v, want %q", got, report.CaseSetOrigin)
	}
}

// TestRunRecordNamesTheRankingItMeasured pins the record against the defect a
// reviewer found: the run record carried the closet scale, the BM25 weight and
// the rerank settings, and named neither the fusion nor the lexical normaliser.
// Two runs at the same commit with rrf against linear fusion, or page-max
// against saturating normalisation, produced different numbers and identical
// records.
//
// The resolved profile is recorded rather than the requested config, because a
// setting that failed to resolve — a reranker that was configured and did not
// come up — is exactly the case where the two differ and only the resolved one
// describes what ranked.
func TestRunRecordNamesTheRankingItMeasured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.cells.json")
	ranking := "fusion=rrf lex-weight=auto-idf lex-norm=page-max closet-boost=0.00 rerank=on(pool=10,weight=0.50) unit=chunk evidence=lexical"
	if err := writeCells(path, closetReport(), caseFileMeta{Generator: "g"}, cellsConfig{Pool: 50, Cases: 4, Ranking: ranking}); err != nil {
		t.Fatalf("writeCells: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("the cells file is not valid JSON: %v", err)
	}
	if got := rec["ranking"]; got != ranking {
		t.Errorf("the run record says ranking %v, want %q", got, ranking)
	}

	// Rung 2: the field exists AND the eval populates it from the service that
	// actually ranked. A record field nothing assigns is this project's most
	// expensive recurring defect.
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "server", "eval.go"))
	if err != nil {
		t.Fatalf("read eval.go: %v", err)
	}
	if !regexp.MustCompile(`Ranking:\s*[A-Za-z0-9_.]*RankingProfile\(\)`).Match(src) {
		t.Error("cmd/server/eval.go never assigns cellsConfig.Ranking from RankingProfile() — " +
			"the field would be written empty on every real run while this test's own call passes it by hand")
	}
}

// TestSupersessionGateJudgesTheArmItWasGiven drives the REAL printing path with
// two different served arms and requires two different verdict headers.
//
// The service-aware selector already existed and was already tested — and had no
// production caller at all: every call site read the package-global, so a
// deployment without a reranker was refused as degraded and a linear deployment
// with one was silently judged as rrf+rerank. A test that calls the selector and
// checks its answer passes identically in both worlds. This one asserts the
// CALL, which is the only difference between them.
func TestSupersessionGateJudgesTheArmItWasGiven(t *testing.T) {
	cell := palace.SupersessionCell{Scope: palace.ScopePool, Cases: 40, StaleAbove: 2}
	meta := caseFileMeta{VerifiedPairs: 40, Judge: "qwen"}

	for _, arm := range []palace.EvalArm{palace.ArmRRF, palace.ArmRRFReranked} {
		report := palace.EvalReport{Arms: []palace.EvalMetrics{
			{Arm: palace.ArmRRF, Ranks: []int{1, 1, 2}, Supersession: cell},
			{Arm: palace.ArmRRFReranked, Ranks: []int{1, 2, 1}, Supersession: cell},
		}}
		var buf strings.Builder
		if err := printSupersessionGate(&buf, report, meta, arm, defaultEvalPool); err != nil {
			t.Fatalf("arm %q: %v", arm, err)
		}
		if !strings.Contains(buf.String(), string(arm)) {
			t.Errorf("the gate was given arm %q and its verdict never names it:\n%s", arm, buf.String())
		}
		// ArmRRF is a prefix of ArmRRFReranked, so naming the wrong one is only
		// detectable in the direction that is not a substring.
		if arm == palace.ArmRRF && strings.Contains(buf.String(), string(palace.ArmRRFReranked)) {
			t.Errorf("the gate was given %q and reported %q — it is reading a global, not its argument:\n%s",
				arm, palace.ArmRRFReranked, buf.String())
		}
	}

	var buf strings.Builder
	err := printSupersessionGate(&buf, palace.EvalReport{}, meta, "", defaultEvalPool)
	if err == nil {
		t.Error("the gate produced a verdict for a served ranking no arm reconstructs")
	}
}

// TestAbsentPromptKeepsIdentifiers pins that the negative generator writes HARD
// negatives, not easy ones.
//
// A negative that drops the note's identifiers, file names and flags is a
// question about a different vocabulary, and a retrieval system separates it from
// an answerable one on surface overlap alone. The calibration curve fitted on
// those negatives then reports a separation that will not survive contact with a
// real near-miss — a question about a SIBLING project, phrased in the same words,
// naming the same files, answered by nothing here.
//
// Measured on this corpus: pages whose wrong content is off-topic separate at
// 0.994 AUC while on-topic ones separate at 0.831. Generating the easy half and
// calibrating on it is how a gate ships that cannot do its job.
func TestAbsentPromptKeepsIdentifiers(t *testing.T) {
	low := strings.ToLower(evalPromptAbsent)
	for _, banned := range []string{
		"do not reuse the note's distinctive identifiers",
		"do not reuse the note's distinctive nouns",
	} {
		if strings.Contains(low, strings.ToLower(banned)) {
			t.Errorf("the absent prompt still instructs %q, which manufactures easy negatives", banned)
		}
	}
	if !strings.Contains(low, "keep") {
		t.Error("the absent prompt does not tell the generator to KEEP the note's identifiers, " +
			"so nothing makes the negative hard")
	}
}

// TestAbsentCaseOutcomeDropsOnVerifierError pins that a case whose absence could
// not be CHECKED is dropped, not kept.
//
// This is the rule the temporal path already enforces and this one does not: an
// unreachable checker returns an error, and keeping the case anyway writes a row
// into the case file that is indistinguishable from a verified one. Every
// downstream number then treats an unchecked assumption as a measurement.
//
// The three outcomes are tested together because the bug is not "error handling
// is missing" — it is that two of the three were handled and the third fell
// through to keep.
func TestAbsentCaseOutcomeDropsOnVerifierError(t *testing.T) {
	if keep, _ := absentCaseOutcome("", nil); !keep {
		t.Error("a question nothing answers is a valid absent case and must be kept")
	}
	if keep, _ := absentCaseOutcome("drawer-7", nil); keep {
		t.Error("a question a memory ANSWERS is not absent and must be rejected")
	}
	keep, reason := absentCaseOutcome("", errors.New("checker unreachable"))
	if keep {
		t.Error("a case whose absence check FAILED was kept — the case file cannot then " +
			"distinguish a verified absence from an unchecked one")
	}
	if !strings.Contains(strings.ToLower(reason), "check") {
		t.Errorf("the drop reason %q does not say the CHECK failed, so a reader cannot tell it "+
			"from a case rejected for being answerable", reason)
	}
}

// TestContrastiveSeparationNamesTheBestSignal pins that the eval reports WHICH
// signal a confidence gate should be built on, rather than leaving the reader to
// compare medians by eye.
//
// The existing report already ends by saying the distance distributions overlap
// and "a confidence gate needs a different signal" — and then does not say which.
// The answer is measurable from the same run: rank every available signal by how
// well it actually separates answerable from unanswerable, and print the winner.
//
// AUC is used rather than a median gap because it is threshold-free. Two signals
// can show the same median difference while one of them orders the cases far
// better, and the gate's question is about ordering.
func TestContrastiveSeparationNamesTheBestSignal(t *testing.T) {
	// A report where the SHAPE separates perfectly and the LEVEL does not: every
	// absent case has a small gap and every answerable one a large gap, while the
	// top rerank score is deliberately uninformative.
	var details []palace.EvalCaseResult
	for i := 0; i < 6; i++ {
		details = append(details, palace.EvalCaseResult{
			Category: palace.CatSingle, Population: palace.PopReachable,
			RerankScored: true, TopRerank: 5.0, TopGap: 3.0 + float64(i)*0.1,
		})
		details = append(details, palace.EvalCaseResult{
			Category: palace.CatAbsent, Population: palace.PopAbsent,
			RerankScored: true, TopRerank: 5.0, TopGap: 0.1 + float64(i)*0.1,
		})
	}
	var buf strings.Builder
	printContrastiveSeparation(&buf, palace.EvalReport{Details: details})
	got := buf.String()

	if !strings.Contains(got, "top_gap") {
		t.Errorf("the report does not mention top_gap at all:\n%s", got)
	}
	// The level carries no signal here (every case scores 5.0), so naming it as
	// the best would mean the ranking is not measuring separation.
	if strings.Contains(got, "best separating signal: top_rerank") {
		t.Errorf("named the constant signal as the best separator:\n%s", got)
	}
	if !strings.Contains(got, "best separating signal: top_gap") {
		t.Errorf("did not name top_gap as the best separator despite perfect separation:\n%s", got)
	}
	if !strings.Contains(got, "1.00") {
		t.Errorf("a perfectly separating signal should report AUC 1.00:\n%s", got)
	}
}

// TestContrastiveSeparationStaysSilentWithoutBothPopulations pins that the report
// says nothing rather than something wrong when a run has only one kind of case.
//
// An AUC needs both classes. Computed over one, it is not a weak number — it is
// undefined, and printing a number there would be the confident-value-on-no-
// evidence failure this repository keeps finding.
func TestContrastiveSeparationStaysSilentWithoutBothPopulations(t *testing.T) {
	var details []palace.EvalCaseResult
	for i := 0; i < 4; i++ {
		details = append(details, palace.EvalCaseResult{
			Category: palace.CatSingle, Population: palace.PopReachable,
			RerankScored: true, TopGap: float64(i),
		})
	}
	var buf strings.Builder
	printContrastiveSeparation(&buf, palace.EvalReport{Details: details})
	if got := buf.String(); got != "" {
		t.Errorf("reported a separation with no unanswerable cases to separate from:\n%s", got)
	}
}

// TestContrastiveSeparationReadsTheDistanceSignals pins that the distance shapes
// are actually READ by the report, in the configuration where they are the only
// signals available.
//
// This test exists because a mutant found its absence: wiring dist_gap to a
// constant zero broke nothing, since the only other test supplies reranked cases
// and expects a rerank signal to win. The distance signals could have been
// declared, listed, and never consulted — the exact shape of defect this
// repository keeps finding, reproduced inside the check meant to prevent it.
//
// No case here is reranked, which is the DEFAULT configuration: if the report only
// worked with a cross-encoder configured, it would print nothing on an ordinary
// run and "prints nothing" is indistinguishable from "there is no signal".
func TestContrastiveSeparationReadsTheDistanceSignals(t *testing.T) {
	var details []palace.EvalCaseResult
	for i := 0; i < 6; i++ {
		details = append(details, palace.EvalCaseResult{
			Category: palace.CatSingle, Population: palace.PopReachable,
			RerankScored: false, DistGap: 0.40 + float64(i)*0.01,
		})
		details = append(details, palace.EvalCaseResult{
			Category: palace.CatAbsent, Population: palace.PopAbsent,
			RerankScored: false, DistGap: 0.01 + float64(i)*0.01,
		})
	}
	var buf strings.Builder
	printContrastiveSeparation(&buf, palace.EvalReport{Details: details})
	got := buf.String()

	if got == "" {
		t.Fatal("the report printed nothing on an un-reranked run — the distance shapes are " +
			"the only signals available there, and they exist on every page")
	}
	if !strings.Contains(got, "dist_gap") {
		t.Errorf("dist_gap is not listed:\n%s", got)
	}
	if strings.Contains(got, "top_rerank") {
		t.Errorf("listed a cross-encoder signal on a run where nothing was reranked — it would "+
			"score a flat zero and sit in the ranking as a dead entry:\n%s", got)
	}
	if !strings.Contains(got, "best separating signal: dist_gap") {
		t.Errorf("dist_gap separates perfectly here and was not named best — the report is not "+
			"reading the field:\n%s", got)
	}
	if !strings.Contains(got, "1.00") {
		t.Errorf("a perfectly separating signal should report AUC 1.00:\n%s", got)
	}
}

// TestCalibrateRefusesUnverifiedCases pins that a threshold can only be derived
// from cases whose absence was actually CHECKED.
//
// Two ways a set fails to qualify, and both must block the file rather than the
// report. An absent case with no verification provenance was never confirmed
// absent — some other memory may answer it perfectly, and calibrating on it tunes
// the gate to refuse questions the palace can in fact answer. A set generated by
// --style absent-easy was confirmed, but against negatives whose identifiers were
// stripped: measured on this corpus those sit twice as far from answerable
// questions as the hard ones, so a threshold fitted to them is fitted to a gap
// that will not be there in production.
//
// The distinction that matters: the curve still PRINTS. T3 has to compare the two
// regimes side by side, and refusing to show the numbers would prevent exactly the
// comparison the ADR needs. What is withheld is the shipped artefact.
func TestCalibrateRefusesUnverifiedCases(t *testing.T) {
	verified := palace.EvalCase{
		Query: "q", Category: palace.CatAbsent,
		AbsentVerification: &palace.AbsentVerification{Checker: "m", Depth: 20, At: "2026-08-21T00:00:00Z"},
	}
	answerable := palace.EvalCase{Query: "a", Expect: "d1", Category: palace.CatSingle}

	t.Run("a verified hard set qualifies", func(t *testing.T) {
		ok, why := calibrationEligible([]palace.EvalCase{answerable, verified}, "absent")
		if !ok {
			t.Errorf("a fully verified set was refused: %s", why)
		}
	})

	t.Run("an unverified absent case blocks the file", func(t *testing.T) {
		bare := palace.EvalCase{Query: "q2", Category: palace.CatAbsent}
		ok, why := calibrationEligible([]palace.EvalCase{answerable, verified, bare}, "absent")
		if ok {
			t.Error("a set holding an absent case with no verification provenance produced a " +
				"calibration; that case may be answered by a memory nobody checked for")
		}
		if !strings.Contains(strings.ToLower(why), "verif") {
			t.Errorf("the refusal %q does not say the case was unverified, so a reader cannot "+
				"tell it from the easy-negative refusal", why)
		}
	})

	t.Run("an easy-negative set blocks the file", func(t *testing.T) {
		ok, why := calibrationEligible([]palace.EvalCase{answerable, verified}, "absent-easy")
		if ok {
			t.Error("a set generated with --style absent-easy produced a calibration; its " +
				"negatives are separable on vocabulary alone")
		}
		if !strings.Contains(strings.ToLower(why), "easy") {
			t.Errorf("the refusal %q does not name the style", why)
		}
	})

	t.Run("merge order cannot hide an easy-negative file", func(t *testing.T) {
		// A merged case set carries ONE metadata block — the last file with a
		// generator wins — so a style read from it is order-dependent. Merging
		// easy negatives BEFORE an answerable file would report the answerable
		// file's style and slip the easy set through. Every style present must be
		// considered, not the one that happened to be read last.
		for _, order := range [][]string{
			{"absent-easy", "paraphrase"},
			{"paraphrase", "absent-easy"},
		} {
			ok, why := calibrationEligible([]palace.EvalCase{answerable, verified}, order...)
			if ok {
				t.Errorf("order %v produced a calibration despite an easy-negative file", order)
			}
			if !strings.Contains(strings.ToLower(why), "easy") {
				t.Errorf("order %v: refusal %q does not name the style", order, why)
			}
		}
	})

	t.Run("a set with no absent cases at all cannot calibrate", func(t *testing.T) {
		if ok, _ := calibrationEligible([]palace.EvalCase{answerable}, "paraphrase"); ok {
			t.Error("a set with nothing to refuse produced a calibration; there is no separation " +
				"to fit a threshold to")
		}
	})
}

// TestTemporalCaseCarriesItsDistractor pins the SELECTION step, not the metric.
//
// Every consumer of a temporal case reads Distractor — SupersessionCell,
// StaleAboveRate, the gate's vacuity rule — and every one of them is covered by a
// test that builds the case BY HAND. That is precisely what let the generator
// omit the field: each component passed while nothing asserted that the thing
// which BUILDS a case fills it in. EvalCase.Distractor is `json:",omitempty"`, so
// the omission left no trace in the file either — one run recorded
// verified_pairs=5 over a file carrying zero pairs, and the gate could only refuse.
//
// Delete `Distractor: older.ID` from temporalCase and both halves below go red.
func TestTemporalCaseCarriesItsDistractor(t *testing.T) {
	newer := palace.Drawer{ID: "drawer-new"}
	older := palace.Drawer{ID: "drawer-old"}

	got := temporalCase("what is the current retention window?", "wing_acme", newer, older)
	if got.Distractor != older.ID {
		t.Fatalf("temporal case dropped its distractor: got %q, want %q — the supersession metric scores where the SUPERSEDED version landed, so a case without one measures nothing",
			got.Distractor, older.ID)
	}
	if got.Expect != newer.ID {
		t.Fatalf("temporal case gold = %q, want the NEWER drawer %q", got.Expect, newer.ID)
	}
	if got.Category != palace.CatTemporal {
		t.Fatalf("temporal case category = %q, want %q", got.Category, palace.CatTemporal)
	}

	// The persistence half. `omitempty` means an unset distractor vanishes from
	// the case file without a word, so a generated file can look well formed and
	// still be unusable on replay. Assert it survives the round trip the gate
	// actually depends on.
	path := filepath.Join(t.TempDir(), "temporal.jsonl")
	meta := caseFileMeta{Style: "temporal", PairCandidates: 1, VerifiedPairs: 1, Judge: "judge-model"}
	if err := writeCases(path, []palace.EvalCase{got}, meta); err != nil {
		t.Fatalf("writeCases: %v", err)
	}
	back, _, err := readCasesWithMeta(path)
	if err != nil {
		t.Fatalf("readCasesWithMeta: %v", err)
	}
	if len(back) != 1 {
		t.Fatalf("round trip returned %d case(s), want 1", len(back))
	}
	if back[0].Distractor != older.ID {
		t.Fatalf("distractor lost on the write/read round trip: got %q, want %q", back[0].Distractor, older.ID)
	}
}

// TestSupersessionRefusalsNameTheirOwnCause pins the four refusal states apart.
//
// They used to be two, and the collapse cost an operator a run: a file with five
// verified pairs was reported as "0 pair(s) present" because the number came
// from the run's scoreable cells while the sentence was about the file's
// verification record. Each state names a different action — harden the file,
// load the right cases, widen the pool, grow the corpus — so a refusal that
// cannot be told from its neighbour sends the reader to fix the wrong thing.
func TestSupersessionRefusalsNameTheirOwnCause(t *testing.T) {
	// Deliberately not defaultEvalPool: every message below must carry the pool
	// the RUN used, and a test written at the default cannot tell the two apart.
	const pool = 128

	t.Run("no verification record, and the count is labelled as the run's", func(t *testing.T) {
		scored := palace.SupersessionCell{Scope: palace.ScopePool, Cases: 3, Vacuous: 2}
		err := supersessionGateReady(scored, caseFileMeta{}, pool)
		if err == nil {
			t.Fatal("the gate answered on a case file with no verification record")
		}
		if !strings.Contains(err.Error(), "--style temporal") {
			t.Errorf("the refusal must name what fixes it: %v", err)
		}
		// The run scored 5; the file's record is what is absent. Printing "5"
		// unlabelled next to a sentence about the record is the original defect.
		if !strings.Contains(err.Error(), "this run scored 5 pair(s)") {
			t.Errorf("the run's own count must be present and LABELLED as the run's, so it cannot be "+
				"read as the file's verification record: %v", err)
		}
	})

	t.Run("record present but the run scored nothing", func(t *testing.T) {
		none := palace.SupersessionCell{Scope: palace.ScopePool}
		err := supersessionGateReady(none, caseFileMeta{VerifiedPairs: 40, Judge: "qwen"}, pool)
		if err == nil {
			t.Fatal("the gate answered on a run that scored no pairs at all")
		}
		if strings.Contains(err.Error(), "vacuous") {
			t.Errorf("nothing was scored, so nothing was vacuous — vacuity is a verdict about a pair "+
				"that entered the pool, and reporting it here sends the operator to raise --pool when "+
				"the cases never arrived: %v", err)
		}
	})

	t.Run("every pair vacuous names the run's pool, not the default", func(t *testing.T) {
		allVacuous := palace.SupersessionCell{Scope: palace.ScopePool, Cases: 0, Vacuous: 7}
		err := supersessionGateReady(allVacuous, caseFileMeta{VerifiedPairs: 40, Judge: "qwen"}, pool)
		if err == nil {
			t.Fatal("the gate answered with every pair vacuous — no arm could have ranked anything")
		}
		if !strings.Contains(err.Error(), "--pool 128") {
			t.Errorf("vacuity is a property OF a pool, so the refusal must name the pool the run "+
				"used: %v", err)
		}
		if strings.Contains(err.Error(), "--pool 50") {
			t.Errorf("the refusal named the DEFAULT pool on a run that passed --pool 128, and then "+
				"advised raising a number the operator had already raised: %v", err)
		}
	})

	t.Run("below the floor names the run's pool too", func(t *testing.T) {
		thin := palace.SupersessionCell{Scope: palace.ScopePool, Cases: 4, StaleAbove: 3, Vacuous: 1}
		err := supersessionGateReady(thin, caseFileMeta{VerifiedPairs: 40, Judge: "qwen"}, pool)
		if err == nil {
			t.Fatal("the gate answered on 4 usable pairs")
		}
		if !strings.Contains(err.Error(), "--pool 128") {
			t.Errorf("the floor refusal must name the run's pool: %v", err)
		}
	})
}

// TestSupersessionVerdictNamesTheRunsPool pins that the pool travels into the
// PASSING path as well. A verdict is quoted long after the run, and "at --pool
// 50" printed under a run at 128 makes the number unreproducible.
func TestSupersessionVerdictNamesTheRunsPool(t *testing.T) {
	const pool = 128
	cell := palace.SupersessionCell{Scope: palace.ScopePool, Cases: 40, StaleAbove: 30}
	report := palace.EvalReport{Arms: []palace.EvalMetrics{
		{Arm: palace.ArmRRFReranked, Ranks: []int{1, 2, 1}, Supersession: cell},
	}}
	var buf strings.Builder
	if err := printSupersessionGate(&buf, report, caseFileMeta{VerifiedPairs: 40, Judge: "qwen"},
		palace.ArmRRFReranked, pool); err != nil {
		t.Fatalf("the gate refused a hardened, sufficient run: %v", err)
	}
	if !strings.Contains(buf.String(), "--pool 128") {
		t.Errorf("the verdict does not name the pool the run used:\n%s", buf.String())
	}
}

// TestProductionRetrieveKMatchesEvalPool keeps the retrieve-k arm's floor and
// the eval --pool default as one number. The arm exists to measure "same
// retrieve as the ablation pool, default page"; if the two 50s drift, the row
// answers a question nobody asked.
func TestProductionRetrieveKMatchesEvalPool(t *testing.T) {
	if palace.ProductionRetrieveK != defaultEvalPool {
		t.Errorf("ProductionRetrieveK=%d, defaultEvalPool=%d — the retrieve-k arm and --pool must stay the same width",
			palace.ProductionRetrieveK, defaultEvalPool)
	}
}

// TestClosetStatusReachesTheTable: the status is PRINTED, not merely computed.
//
// This is the rung the palace tests cannot cover. `ClosetDelta` returning the
// right status changes nothing on its own — the closet row is read off the
// printed table, and a status computed and dropped before the writer leaves
// every reader exactly where seven previous tables left them, looking at
// `Δ +0.000` with no way to tell an unrun experiment from a null. It is the same
// defect one level down from the one the task fixes.
func TestClosetStatusReachesTheTable(t *testing.T) {
	det := func(q string, rank int) palace.EvalCaseResult {
		return palace.EvalCaseResult{
			Query: q, Category: "single-hop", PoolRank: rank,
			Ranks: map[palace.EvalArm]int{palace.ArmHybridCloset: rank, palace.ArmHybrid: rank},
		}
	}
	report := palace.EvalReport{
		Closets: 0,
		Details: []palace.EvalCaseResult{det("a", 1), det("b", 2)},
	}

	var buf bytes.Buffer
	printClosetBlock(&buf, report)
	got := buf.String()

	// The status must be on the ROW, not merely somewhere in the block. Asserting
	// `strings.Contains(got, …)` is what this test did first and a mutant walked
	// straight through it: blanking the status COLUMN left the explanatory line
	// under the table still naming the status, so the fence passed with the
	// mechanism broken. A reader scanning the table by column sees the column.
	var row string
	for _, line := range strings.Split(got, "\n") {
		if f := strings.Fields(line); len(f) > 3 && f[0] == "single-hop" {
			row = strings.TrimSpace(line)
		}
	}
	if row == "" {
		t.Fatalf("no single-hop row in the block:\n%s", got)
	}
	if !strings.HasSuffix(row, string(palace.ClosetNotMeasured)) {
		t.Errorf("the closet ROW does not end with its status, so a reader scanning the table "+
			"still sees a delta of zero from an experiment that never ran:\n  %s", row)
	}
	// Naming the status without naming the absent input tells a reader that
	// something is wrong and not what — which sends them to the ranking code, where
	// the number came from and the problem is not.
	//
	// It asserts the CELL's own missing-input sentence, not the word "closet": the
	// block's header is "closet prior", so a substring check for that word passes
	// on output carrying no explanation at all and could never fail.
	want := palace.ClosetDelta(report, "single-hop").Missing
	if want == "" {
		t.Fatal("the cell names no missing input, so this test cannot check that it was printed")
	}
	if !strings.Contains(got, want) {
		t.Errorf("the printed block does not carry the cell's missing input %q:\n%s", want, got)
	}
}
