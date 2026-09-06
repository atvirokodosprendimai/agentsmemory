package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
