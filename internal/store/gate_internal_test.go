package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// internalSoT and internalIndex are minimal fakes for the gate's UNEXPORTED
// machinery (afterWrite, countPairFor, the cached pair). The public-behaviour
// tests live in hybrid_gate_test.go with richer fakes; these exist so the
// cap/single-flight behaviour can be asserted from inside the package.
type internalSoT struct {
	mu            sync.Mutex
	count         int
	countHook     func() // called at the top of Count, without the lock
	countErr      error  // returned by Count instead of the count, when set
	panicCount    bool   // Count panics instead of returning, when set
	countCtxCheck bool   // Count honors ctx cancellation after the hook, like a real SoT
	countCalls    int
}

func (f *internalSoT) EnsureNamespace(context.Context, string, int) error { return nil }
func (f *internalSoT) Upsert(context.Context, string, []Point) error      { return nil }
func (f *internalSoT) Search(context.Context, string, []float32, int, Filter) (SearchResult, error) {
	return SearchResult{}, nil
}
func (f *internalSoT) Count(ctx context.Context, _ string) (int, error) {
	f.mu.Lock()
	f.countCalls++
	hook := f.countHook
	err := f.countErr
	panics := f.panicCount
	f.mu.Unlock()
	if panics {
		panic("count refresh panicked")
	}
	if hook != nil {
		hook()
	}
	if f.countCtxCheck {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
	}
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count, nil
}
func (f *internalSoT) Delete(context.Context, string, []string) error { return nil }
func (f *internalSoT) PointsByIDs(context.Context, string, []string) ([]Point, error) {
	return nil, nil
}
func (f *internalSoT) SetPayload(context.Context, string, []string, map[string]string) error {
	return nil
}
func (f *internalSoT) AllPoints(context.Context, string) ([]Point, error) { return nil, nil }
func (f *internalSoT) Namespaces(context.Context) ([]string, error)       { return nil, nil }

type internalIndex struct {
	mu          sync.Mutex
	count       int
	exactCalls  int
	approxCalls int
}

func (f *internalIndex) EnsureNamespace(context.Context, string, int) error { return nil }
func (f *internalIndex) Upsert(context.Context, string, []Point) error      { return nil }
func (f *internalIndex) Search(context.Context, string, []float32, int, Filter) (SearchResult, error) {
	return SearchResult{}, nil
}
func (f *internalIndex) Count(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exactCalls++
	return f.count, nil
}
func (f *internalIndex) ApproximateCount(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approxCalls++
	return f.count, nil
}
func (f *internalIndex) Delete(context.Context, string, []string) error { return nil }
func (f *internalIndex) PointsByIDs(context.Context, string, []string) ([]Point, error) {
	return nil, nil
}
func (f *internalIndex) SetPayload(context.Context, string, []string, map[string]string) error {
	return nil
}
func (f *internalIndex) AllPoints(context.Context, string) ([]Point, error) { return nil, nil }
func (f *internalIndex) Namespaces(context.Context) ([]string, error)       { return nil, nil }

// TestAfterWriteWatermarkIsExactAboveTheCap: the watermark is the reference a
// deficit is measured against, so a write must record it EXACTLY even above the
// cap. The approximate count is biased high on qdrant (deleted-but-not-yet-
// compacted points inflate it), and an inflated watermark makes a genuinely
// deficient sampled read look like a lagged one — the suppression in
// triggerRebuild then skips the very rebuild that is the mechanism's safety net
// (review round 2, R2-2).
func TestAfterWriteWatermarkIsExactAboveTheCap(t *testing.T) {
	sot := &internalSoT{count: ExactCountCap + 1}
	idx := &internalIndex{count: ExactCountCap + 1}
	h := NewHybridWithConfig(sot, idx, DefaultGateConfig())
	h.gate.mu.Lock()
	h.gate.pair["ns"] = countPair{expected: ExactCountCap + 1, indexed: ExactCountCap + 1, at: time.Now()}
	h.gate.mu.Unlock()

	h.afterWrite(context.Background(), "ns", true)

	if idx.approxCalls != 0 {
		t.Errorf("afterWrite used the approximate count %d time(s) above the cap; want 0", idx.approxCalls)
	}
	if idx.exactCalls == 0 {
		t.Error("afterWrite never read the exact index count for the watermark")
	}
	h.gate.mu.Lock()
	wm := h.gate.watermark["ns"]
	h.gate.mu.Unlock()
	if wm != ExactCountCap+1 {
		t.Errorf("watermark = %d; want the exact count %d", wm, ExactCountCap+1)
	}
}

// TestAfterWriteRecordsTheExactWatermark: below the cap the exact count is the
// only count there is; the write path must read it and record it as the
// watermark so the next sampled read measures against a real population.
func TestAfterWriteRecordsTheExactWatermark(t *testing.T) {
	sot := &internalSoT{count: 2}
	idx := &internalIndex{count: 2}
	h := NewHybridWithConfig(sot, idx, DefaultGateConfig())
	h.gate.mu.Lock()
	h.gate.pair["ns"] = countPair{expected: 2, indexed: 2, at: time.Now()}
	h.gate.mu.Unlock()

	h.afterWrite(context.Background(), "ns", true)

	if idx.approxCalls != 0 {
		t.Errorf("afterWrite used the approximate count %d time(s) below the cap; want 0", idx.approxCalls)
	}
	if idx.exactCalls == 0 {
		t.Error("afterWrite never read the exact index count for the watermark")
	}
	h.gate.mu.Lock()
	wm := h.gate.watermark["ns"]
	h.gate.mu.Unlock()
	if wm != 2 {
		t.Errorf("watermark = %d; want the exact count 2", wm)
	}
}

// TestCountPairRefreshIsSingleFlight: N concurrent queries on an expired pair
// must issue one count refresh, not N — the count pair is exactly the cost the
// cache exists to amortize, and a stampede at TTL expiry would pay it once per
// query.
func TestCountPairRefreshIsSingleFlight(t *testing.T) {
	sot := &internalSoT{count: 1}
	idx := &internalIndex{count: 1}
	h := NewHybridWithConfig(sot, idx, DefaultGateConfig())
	ttl := DefaultGateConfig().CountTTL
	h.gate.mu.Lock()
	h.gate.pair["ns"] = countPair{expected: 1, indexed: 1, at: time.Now().Add(-2 * ttl)}
	h.gate.mu.Unlock()

	// The leader's truth count blocks until release; the second goroutine must
	// be waiting on the in-flight refresh, not issuing its own count.
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	sot.countHook = func() {
		once.Do(func() { close(entered) })
		<-release
	}

	started := make(chan struct{}, 2)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			started <- struct{}{}
			_, errs[i] = h.countPairFor(context.Background(), "ns")
		}(i)
	}
	// Both callers are inside countPairFor before the leader is released, so
	// the second caller's DoChan joins the leader's still-live singleflight
	// call instead of running a second leader.
	<-started
	<-started
	<-entered // the leader is inside the truth count
	close(release)
	wg.Wait()
	if sot.countCalls != 1 {
		t.Errorf("concurrent refreshes ran the truth count %d time(s); want 1", sot.countCalls)
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
}

// TestCountRefreshErrorIsSharedByAllWaiters: one source-of-truth outage must
// read the same way from every goroutine. The hand-rolled single-flight slot
// returned a wrapped error to the leader but stored the unwrapped original for
// waiters, so a caller's message depended on which goroutine it happened to be
// (review round 2, R2-4). singleflight hands every waiter the leader's exact
// result; this test pins that the wrapper is part of it.
func TestCountRefreshErrorIsSharedByAllWaiters(t *testing.T) {
	started := make(chan struct{}, 2)
	entered := make(chan struct{})
	release := make(chan struct{})
	// Hold the leader inside Count so the second caller genuinely waits on the
	// refresh slot — with an instant error two callers just run two leaders.
	// The leader's count blocks until release; a caller descheduled between its
	// start and its DoChan can run as a second leader once the error path caches
	// nothing, so the signal close is Once — the error-sharing property holds in
	// both interleavings, and the hook must not panic on the second.
	var once sync.Once
	sot := &internalSoT{count: 1, countErr: errors.New("truth down"),
		countHook: func() { once.Do(func() { close(entered) }); <-release }}
	idx := &internalIndex{count: 1}
	h := NewHybridWithConfig(sot, idx, DefaultGateConfig())
	ttl := DefaultGateConfig().CountTTL
	h.gate.mu.Lock()
	h.gate.pair["ns"] = countPair{expected: 1, indexed: 1, at: time.Now().Add(-2 * ttl)}
	h.gate.mu.Unlock()

	const want = "count source of truth: truth down"
	msgs := make([]string, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			started <- struct{}{}
			_, err := h.countPairFor(context.Background(), "ns")
			if err != nil {
				msgs[i] = err.Error()
			}
		}(i)
	}
	// Both callers have entered countPairFor before the leader is released, so
	// the second caller's DoChan joins the leader's still-live singleflight
	// call — it can never become a second leader.
	<-started
	<-started
	<-entered
	close(release)
	wg.Wait()
	for i, m := range msgs {
		if m != want {
			t.Errorf("call %d saw %q; want %q — one outage must read the same way from every goroutine", i, m, want)
		}
	}
}

// TestCountRefreshPanicDoesNotWedgeWaiters: a panic inside a count refresh
// must not wedge every later query on the namespace. The hand-rolled slot
// closed its done channel on all three return paths but not on a panic — one
// panic left done permanently open and every subsequent query blocked until
// its own context died (review round 2, R2-5). The refresh must surface an
// error and leave the slot free for the next query.
func TestCountRefreshPanicDoesNotWedgeWaiters(t *testing.T) {
	sot := &internalSoT{count: 1, panicCount: true}
	idx := &internalIndex{count: 1}
	h := NewHybridWithConfig(sot, idx, DefaultGateConfig())
	ttl := DefaultGateConfig().CountTTL
	h.gate.mu.Lock()
	h.gate.pair["ns"] = countPair{expected: 1, indexed: 1, at: time.Now().Add(-2 * ttl)}
	h.gate.mu.Unlock()

	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = recover() }()
			_, _ = h.countPairFor(context.Background(), "ns")
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent refreshes never returned after a panic — waiters are wedged")
	}
}

// TestCountRefreshDetachesFromLeaderCancellation: the count refresh is shared
// work, not the leader's work. The singleflight closure captured whichever
// caller won the race, so a leader that disconnects mid-count (client
// disconnect, per-request timeout) made every waiter's recall fail with
// "count source of truth: context canceled" for a reason that had nothing to
// do with their request — precisely at the TTL-expiry stampede, when
// concurrency is highest (review round 3, R3-1). The count must run detached
// from the leader's lifetime: cancel the leader while it is inside the
// source-of-truth count, and the healthy waiter must still receive the pair.
func TestCountRefreshDetachesFromLeaderCancellation(t *testing.T) {
	sot := &internalSoT{count: 1, countCtxCheck: true}
	idx := &internalIndex{count: 1}
	h := NewHybridWithConfig(sot, idx, DefaultGateConfig())
	ttl := DefaultGateConfig().CountTTL
	h.gate.mu.Lock()
	h.gate.pair["ns"] = countPair{expected: 1, indexed: 1, at: time.Now().Add(-2 * ttl)}
	h.gate.mu.Unlock()

	// The leader's truth count blocks until release; the leader's context is
	// cancelled while it is inside that count, then the count returns.
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	sot.countHook = func() {
		once.Do(func() { close(entered) })
		<-release
	}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	started := make(chan struct{}, 2)
	var wg sync.WaitGroup
	var waiterErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		started <- struct{}{}
		_, _ = h.countPairFor(leaderCtx, "ns")
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		started <- struct{}{}
		_, waiterErr = h.countPairFor(context.Background(), "ns")
	}()
	<-started
	<-started
	<-entered // the leader is inside the truth count
	cancelLeader()
	close(release)
	wg.Wait()

	if waiterErr != nil {
		if strings.Contains(waiterErr.Error(), "context canceled") {
			t.Errorf("healthy waiter inherited the leader's cancellation: %v", waiterErr)
		} else {
			t.Errorf("healthy waiter failed for an unrelated reason: %v", waiterErr)
		}
	}
}
