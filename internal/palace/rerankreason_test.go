package palace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/rerank/tei"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestTheReasonOnTheSpanIsTheReasonReturned: the word the span carries and the
// word the caller is given must be the same word.
//
// It is asserted as a PAIR, from a single invocation, rather than as two
// separate expectations. ADR-034 exists because a trace and a durable row can
// disagree about one recall; checking each half against a constant would let
// them drift apart while both tests still pass. Here they cannot, because the
// span and the return value come out of the same call.
func TestTheReasonOnTheSpanIsTheReasonReturned(t *testing.T) {
	for _, tc := range []struct {
		name string
		svc  func(t *testing.T) *Service
		want string
	}{
		{"a blown budget", func(t *testing.T) *Service {
			slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-time.After(2 * time.Second):
				case <-r.Context().Done():
				}
			}))
			t.Cleanup(slow.Close)
			// The real tei client against a server that sleeps: a fake returning
			// context.DeadlineExceeded would pass while production said "error",
			// because tei decides this from whether it reached the model at all.
			return newTestService(t).WithReranker(tei.New(slow.URL, 50*time.Millisecond), 2)
		}, telemetry.ReasonTimeout},

		{"a sick endpoint", func(t *testing.T) *Service {
			sick := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			}))
			t.Cleanup(sick.Close)
			return newTestService(t).WithReranker(tei.New(sick.URL, 30*time.Second), 2)
		}, telemetry.ReasonError},

		{"the cross-encoder returned the wrong number of scores", func(t *testing.T) *Service {
			return newTestService(t).WithReranker(&staticReranker{scores: []float64{1}}, 2)
		}, telemetry.ReasonScoreCount},

		{"no reranker configured", func(t *testing.T) *Service {
			return newTestService(t)
		}, telemetry.ReasonNoReranker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := tc.svc(t)
			got, onSpan := reasonAndSpan(t, svc, DefaultRerankWeight)
			if got != tc.want {
				t.Errorf("returned reason = %q, want %q", got, tc.want)
			}
			if onSpan != got {
				t.Errorf("the span says %q and the caller was told %q — a trace and a durable "+
					"row would disagree about one recall, which is the defect ADR-034 is for",
					onSpan, got)
			}
		})
	}
}

// TestWeightZeroReportsWeightZeroNotAnError separates the two bypasses that both
// mean "no cross-encoder score exists": deliberate (weight 0) and failed. The
// durable column is worth nothing if a disabled reranker and a broken one write
// the same value, which is exactly today's `reranked = 0`.
func TestWeightZeroReportsWeightZeroNotAnError(t *testing.T) {
	svc := newTestService(t).WithReranker(&staticReranker{scores: []float64{1, 2}}, 2)
	got, onSpan := reasonAndSpan(t, svc, 0)
	if got != telemetry.ReasonWeightZero {
		t.Errorf("reason at weight 0 = %q, want %q", got, telemetry.ReasonWeightZero)
	}
	if onSpan != got {
		t.Errorf("span %q vs returned %q", onSpan, got)
	}
}

// TestAServedRerankReturnsNoReason: the healthy path returns empty, so T2's
// column stays empty on rows where nothing was skipped. A key that is always
// present measures nothing.
func TestAServedRerankReturnsNoReason(t *testing.T) {
	svc := newTestService(t).WithReranker(&staticReranker{scores: []float64{1, 2}}, 2)
	got, _ := reasonAndSpan(t, svc, DefaultRerankWeight)
	if got != "" {
		t.Errorf("a rerank that RAN returned %q; the healthy path must be empty", got)
	}
}

// reasonAndSpan runs one applyRerankWith and returns (returned reason, the
// am.reason on the rerank span it emitted) — from the SAME call, so the two
// cannot be reconciled after the fact.
func reasonAndSpan(t *testing.T, svc *Service, weight float64) (returned, onSpan string) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	survivors := []SearchHit{
		{Drawer: Drawer{ID: "A", Content: "exact identifier match"}},
		{Drawer: Drawer{ID: "B", Content: "topically similar"}},
	}
	ranked := []HybridScore{{Index: 0, Fused: 1.0}, {Index: 1, Fused: 0.2}}

	ctx := telemetry.WithProvider(context.Background(), tp)
	_, _, returned = svc.applyRerankWith(ctx, "q", "q", nil, survivors, ranked, weight)

	for _, s := range sr.Ended() {
		if s.Name() == telemetry.StageRerank {
			return returned, spanAttrs(s)["am.reason"]
		}
	}
	t.Fatalf("no %s span was recorded", telemetry.StageRerank)
	return "", ""
}

var _ = errors.Is
