package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
)

// Issue #145. The findings this covers are both silent by construction: a pool
// that cannot be scored inside the timeout degrades every search to hybrid order
// and says so only in a server log, and a pool at or below the page size pays a
// cross-encoder to reorder the page it would have returned anyway.

// slowReranker answers after a delay proportional to the batch, so a test can
// exercise the arithmetic against a service that behaves like the measured one:
// ~0.5s per document on the llama.cpp deployment in the issue.
func slowReranker(t *testing.T, fixed, perDoc time.Duration) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		time.Sleep(fixed + perDoc*time.Duration(len(req.Texts)))
		out := make([]map[string]any, len(req.Texts))
		for i := range req.Texts {
			out[i] = map[string]any{"index": i, "score": 1.0 / float64(i+1)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv, srv.URL
}

// TestDoctorRerankReportsAPoolThatCannotFit is the finding, and it is the whole
// point of the check: this is the state an operator cannot currently observe
// before it bites.
func TestDoctorRerankReportsAPoolThatCannotFit(t *testing.T) {
	_, url := slowReranker(t, 10*time.Millisecond, 40*time.Millisecond)
	cfg := config.Default()
	cfg.RerankURL = url
	cfg.RerankTimeout = time.Second
	cfg.RerankPool = 50 // 50 × 40ms is two seconds; the budget is one

	var out bytes.Buffer
	err := doctorRerank(context.Background(), cfg, &out)
	if err == nil {
		t.Fatal("a pool that cannot be scored inside the timeout reported success; the exit code " +
			"is the whole verdict, and this state is invisible to the operator otherwise")
	}
	for _, want := range []string{"50", "hybrid order", "reranked=false"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not mention %q, so it does not say what will happen: %v",
				want, err)
		}
	}
	if !strings.Contains(out.String(), "largest pool that fits") {
		t.Errorf("the report does not name the pool that WOULD fit, which is the number an "+
			"operator needs in order to act:\n%s", out.String())
	}
}

// TestDoctorRerankPassesAPoolThatFits is the other side, and without it the check
// above passes on a gate that always fails.
func TestDoctorRerankPassesAPoolThatFits(t *testing.T) {
	_, url := slowReranker(t, 5*time.Millisecond, time.Millisecond)
	cfg := config.Default()
	cfg.RerankURL = url
	cfg.RerankTimeout = 2 * time.Second
	cfg.RerankPool = 10

	var out bytes.Buffer
	if err := doctorRerank(context.Background(), cfg, &out); err != nil {
		t.Fatalf("a pool of 10 at ~1ms per document was reported as unaffordable: %v\n%s",
			err, out.String())
	}
	if !strings.Contains(out.String(), "fits") {
		t.Errorf("the report does not say the pool fits:\n%s", out.String())
	}
}

// TestDoctorRerankSaysWhenThereIsNoRerankerToProbe pins the one case that must
// NOT be a finding. Running without a cross-encoder is a supported deployment,
// and a check that went red on it would be switched off — taking the verdict
// above with it, which is the failure mode ADR-056's unlabelled-anchor count
// already records.
func TestDoctorRerankSaysWhenThereIsNoRerankerToProbe(t *testing.T) {
	cfg := config.Default()
	cfg.RerankURL = ""

	var out bytes.Buffer
	if err := doctorRerank(context.Background(), cfg, &out); err != nil {
		t.Errorf("a deployment with no reranker was reported as a fault: %v", err)
	}
	if !strings.Contains(out.String(), "no reranker configured") {
		t.Errorf("the report is silent about there being nothing to probe, which is "+
			"indistinguishable from a probe that passed:\n%s", out.String())
	}
}

// TestDoctorRerankReportsTheReorderOnlyBoundary is the issue's second finding:
// when a request's limit reaches the pool, the cross-encoder reorders the page it
// would have returned anyway. It is reported at EVERY run rather than only when
// it bites, because the operator sets the pool once and the limit varies per
// call — a warning that waits for a bad call arrives after the latency is paid.
func TestDoctorRerankReportsTheReorderOnlyBoundary(t *testing.T) {
	_, url := slowReranker(t, time.Millisecond, time.Millisecond)
	cfg := config.Default()
	cfg.RerankURL = url
	cfg.RerankTimeout = 2 * time.Second
	cfg.RerankPool = 12

	var out bytes.Buffer
	if err := doctorRerank(context.Background(), cfg, &out); err != nil {
		t.Fatalf("probe: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "reorder-only above limit 12") {
		t.Errorf("the report does not name the limit above which reranking buys no recall:\n%s",
			out.String())
	}
}

// TestTheProbeSeparatesFixedCostFromPerDocumentCost pins the arithmetic the
// verdict rests on.
//
// ⚠ A SINGLE TIMING DIVIDED BY ITS BATCH SIZE IS WRONG IN A DIRECTION THAT
// MATTERS: it charges the whole per-call cost to every document, overstating the
// marginal cost and understating the affordable pool, so an operator is told to
// shrink a pool that was fine. With a fixed cost far larger than the marginal
// one, the two models disagree by an order of magnitude.
func TestTheProbeSeparatesFixedCostFromPerDocumentCost(t *testing.T) {
	_, url := slowReranker(t, 200*time.Millisecond, 2*time.Millisecond)
	cfg := config.Default()
	cfg.RerankURL = url
	cfg.RerankTimeout = time.Second
	cfg.RerankPool = 100 // (1s − 200ms) / 2ms = ~400, so this must FIT

	var out bytes.Buffer
	if err := doctorRerank(context.Background(), cfg, &out); err != nil {
		t.Errorf("a pool of 100 at 2ms per document behind a 200ms fixed cost was reported as "+
			"unaffordable: %v\n%s\n"+
			"  Dividing one wall time by its batch size gives ~200ms per document and rejects "+
			"any pool above 5, which is the single-point model this fit exists to avoid.",
			err, out.String())
	}
}

// TestDoctorRerankIsReachable covers the rung a behavioural test cannot: a flag
// that is declared, documented, and read inside a block nothing reaches. The same
// gap TestDoctorCorpusIsReachable exists for.
func TestDoctorRerankIsReachable(t *testing.T) {
	_, url := slowReranker(t, time.Millisecond, time.Millisecond)
	cfg, _ := eraseTestWorkspace(t)
	cfg.RerankURL = url

	var out bytes.Buffer
	cmd := rootCommand(cfg)
	cmd.Writer = &out
	err := cmd.Run(context.Background(), []string{
		"agentsmemory", "doctor", "--db", cfg.DBPath, "--rerank",
	})
	if err != nil {
		t.Fatalf("doctor --rerank through the real command tree: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "rerank: probing") {
		t.Errorf("--rerank produced no probe report, so the flag is parsed and the check is "+
			"not reached:\n%s", out.String())
	}
}

// TestABareDoctorStillRefusesAfterRerankWasAdded keeps the property adding a flag
// is most likely to break: zero checks must never look like a healthy palace.
func TestABareDoctorStillRefusesAfterRerankWasAdded(t *testing.T) {
	cfg, _ := eraseTestWorkspace(t)
	var out bytes.Buffer
	cmd := rootCommand(cfg)
	cmd.Writer = &out
	err := cmd.Run(context.Background(), []string{"agentsmemory", "doctor", "--db", cfg.DBPath})
	if err == nil {
		t.Fatal("a bare doctor exited 0")
	}
	if !strings.Contains(err.Error(), "--rerank") {
		t.Errorf("the refusal does not offer --rerank, so the check exists and nothing tells an "+
			"operator it is there: %v", err)
	}
	fmt.Fprint(&out, "")
}

// coldStartReranker is slowReranker with a one-off penalty on the FIRST request,
// which is what llama.cpp on CPU does: it loads and warms on first use.
func coldStartReranker(t *testing.T, warmUp, fixed, perDoc time.Duration) string {
	t.Helper()
	var first bool
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		cold := !first
		first = true
		mu.Unlock()
		d := fixed + perDoc*time.Duration(len(req.Texts))
		if cold {
			d += warmUp
		}
		time.Sleep(d)
		out := make([]map[string]any, len(req.Texts))
		for i := range req.Texts {
			out[i] = map[string]any{"index": i, "score": 1.0 / float64(i+1)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestAColdStartIsNotReportedAsAnUnaffordablePool is review of PR #324's finding.
//
// ⚠ IT FIRES IN THE DIRECTION THIS CHECK EXISTS TO PREVENT. The probe timed the
// small batch first, so a first-request warm-up landed entirely on the point
// setting the intercept. Measured by the reviewer with the same 200ms/2ms
// geometry as the two-point test: the 1-document call came back at 504ms and the
// 8-document call at 218ms, the slope guard skipped the fit, and an operator was
// told to cut a pool of 100 to 8 on a box that affords about four hundred.
//
// The remedy is one discarded call before the two timed ones.
func TestAColdStartIsNotReportedAsAnUnaffordablePool(t *testing.T) {
	url := coldStartReranker(t, 400*time.Millisecond, 20*time.Millisecond, 2*time.Millisecond)
	cfg := config.Default()
	cfg.RerankURL = url
	cfg.RerankTimeout = time.Second
	cfg.RerankPool = 100 // (1s − 20ms) / 2ms ≈ 490, so this fits comfortably

	var out bytes.Buffer
	if err := doctorRerank(context.Background(), cfg, &out); err != nil {
		t.Errorf("a cold start was reported as an unaffordable pool: %v\n%s\n"+
			"  The warm-up belongs to neither timed point. Charging it to the intercept is the "+
			"single-point model's failure through a different door — an operator told to shrink "+
			"a pool that was fine.", err, out.String())
	}
	// ⚠ NOT-FAILING IS NOT ENOUGH, and the first version of this test stopped
	// there. Without the warm-up call the cold point makes the small batch SLOWER
	// than the large one, the fit is skipped, and the run comes back INCONCLUSIVE —
	// which is also not an error, so the test passed with the remedy deleted. The
	// property is that a warm box is MEASURED, so the measurement is what to assert.
	if strings.Contains(out.String(), "INCONCLUSIVE") {
		t.Errorf("the probe could not fit a slope on a box that is merely cold:\n%s\n"+
			"  Abstaining is the right answer to noise and the wrong one to a warm-up, which is "+
			"a known cost with a known remedy.", out.String())
	}
	if !strings.Contains(out.String(), "pool 100 fits") {
		t.Errorf("the report does not confirm the configured pool fits:\n%s", out.String())
	}
}

// TestAnUnfittableProbeIsInconclusiveRatherThanAVerdict is the second half of the
// same finding.
//
// One branch was doing two jobs: "too fast to measure" and "the measurement
// broke" both resolved to the largest batch observed, and the verdict then FAILED
// THE RUN over it — printing `~0s fixed + ~0s per document`, a degenerate model on
// its face, and stating a conclusion with full confidence beneath it.
//
// A pre-flight that cries wolf on a healthy box is the one an operator disables,
// which is the argument the no-reranker branch already makes.
func TestAnUnfittableProbeIsInconclusiveRatherThanAVerdict(t *testing.T) {
	// ⚠ AND NOTHING ELSE NOW EXERCISES A FITTED-FROM-JITTER SLOPE. The instant
	// fixture was demonstrating that by accident — it is what produced 5,227 under
	// -race and 457,832 before the noise floor — and inverting it correctly stops
	// it doing so. The case is covered deliberately instead, by
	// TestAnAbsurdlyLargeFittedPoolIsNotNamed below, which is where a reader should
	// look for it rather than concluding it was never considered.
	//
	// ⚠ THE LARGE BATCH IS DELIBERATELY THE FAST ONE, and the first version of this
	// fixture answered "instantly" for every size instead. That relies on the two
	// calls differing by less than the noise floor, which held locally and did not
	// under -race: the detector slowed the second call past a millisecond, a slope
	// was fitted, and the gate reported a pool over a corpus it could not measure.
	// A test whose property depends on the machine being fast enough is a flake
	// with an argument. Inverting the cost makes "the larger batch came back no
	// slower" true by construction, which is the state under test.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Texts) <= 1 {
			time.Sleep(40 * time.Millisecond)
		} else {
			time.Sleep(2 * time.Millisecond)
		}
		out := make([]map[string]any, len(req.Texts))
		for i := range req.Texts {
			out[i] = map[string]any{"index": i, "score": 1.0}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.RerankURL = srv.URL
	cfg.RerankTimeout = time.Second
	cfg.RerankPool = 200

	var out bytes.Buffer
	if err := doctorRerank(context.Background(), cfg, &out); err != nil {
		t.Errorf("an unfittable probe failed the run: %v\n%s\n"+
			"  Saying \"I could not measure this\" is an answer; resolving it to a number and "+
			"exiting non-zero is a guess wearing a verdict.", err, out.String())
	}
	if !strings.Contains(out.String(), "INCONCLUSIVE") {
		t.Errorf("the report does not say the fit failed, so a reader takes the silence for a "+
			"pass:\n%s", out.String())
	}
	if strings.Contains(out.String(), "largest pool that fits") {
		t.Errorf("the report names an affordable pool it could not measure:\n%s", out.String())
	}
}

// TestAnAbsurdlyLargeFittedPoolIsNotNamed is the residual review of PR #324
// raised after that PR merged: minMeasurableSpread is an absolute floor and
// scheduling jitter is not, so a fast reranker on a busy host still fits a slope
// to noise and the check printed the result with a measurement's confidence.
//
// The verdict was never wrong — a pool anyone configures fits — but the number
// beside it was derived from jitter, which is the class this file already rejects
// twice. Capping is robust in both directions and needs no second sample.
func TestAnAbsurdlyLargeFittedPoolIsNotNamed(t *testing.T) {
	// A spread over the noise floor, and tiny per document: the geometry of a fast
	// cross-encoder, or of jitter on a busy box. Either way the fit is real
	// arithmetic over a difference nobody should act on.
	_, url := slowReranker(t, 2*time.Millisecond, 300*time.Microsecond)
	cfg := config.Default()
	cfg.RerankURL = url
	// ⚠ A WIDE BUDGET, BECAUSE THE WINDOW IS OTHERWISE NARROWER THAN THE JITTER.
	// The fitted pool must land above the ceiling, which at a 1s budget means a
	// spread inside roughly 1ms-7ms — and review of this PR measured 6 failures in
	// 200 concurrent -race runs on a 40-core box, where a nominal 2.1ms spread was
	// observed at 11ms and the fitted pool swung 5x. At 10s the same spread has
	// 70ms of room.
	//
	// That flake is evidence FOR what this test pins rather than against it: the
	// production number moved five-fold with machine load, which is precisely why
	// it should not be printed. The code was more right than the test.
	cfg.RerankTimeout = 10 * time.Second
	cfg.RerankPool = 50

	var out bytes.Buffer
	if err := doctorRerank(context.Background(), cfg, &out); err != nil {
		t.Fatalf("a pool of 50 against a fast reranker was reported as unaffordable: %v\n%s",
			err, out.String())
	}
	if !strings.Contains(out.String(), "not what limits you") {
		t.Errorf("the report names a fitted pool instead of saying the pool is not the "+
			"constraint:\n%s\n"+
			"  Past a thousand the figure is arithmetic over a spread near the noise floor, and "+
			"printing it spends the check's credibility on a number nobody acts on.", out.String())
	}
	if strings.Contains(out.String(), "largest pool that fits") {
		t.Errorf("the report still names the number:\n%s", out.String())
	}
}
