package embedworker

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

// fakeSvc is an in-memory pending queue: a team->remaining map the worker drains.
type fakeSvc struct {
	mu       sync.Mutex
	pending  map[string]int
	embedded int
	failOnce bool // simulate a transient embedder error on the first embed call
}

func (f *fakeSvc) TeamsWithPending(_ context.Context, _ int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var teams []string
	for team, n := range f.pending {
		if n > 0 {
			teams = append(teams, team)
		}
	}
	return teams, nil
}

func (f *fakeSvc) EmbedPendingForTeam(_ context.Context, team string, batch int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOnce {
		f.failOnce = false
		return 0, errors.New("embedder down")
	}
	n := f.pending[team]
	if n <= 0 {
		return 0, nil
	}
	if n > batch {
		n = batch
	}
	f.pending[team] -= n
	f.embedded += n
	return n, nil
}

func (f *fakeSvc) remaining() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.pending {
		total += n
	}
	return total
}

// TestWorkerDrainsRoundRobin proves the worker indexes every pending row across
// multiple tenants and recovers from a transient embed failure rather than dying.
func TestWorkerDrainsRoundRobin(t *testing.T) {
	f := &fakeSvc{pending: map[string]int{"team-a": 130, "team-b": 65}, failOnce: true}
	// Tiny idle so the post-drain loop doesn't slow the test; quiet logger.
	w := New(f, 50, 5*time.Millisecond, log.New(io.Discard, "", 0))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for f.remaining() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("queue not drained, %d remaining", f.remaining())
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()

	if f.embedded != 195 {
		t.Errorf("embedded = %d, want 195 (130 + 65)", f.embedded)
	}
}

// TestWorkerStopsOnContextCancel proves Run returns promptly when ctx is cancelled
// (so server shutdown isn't blocked) even with nothing to do.
func TestWorkerStopsOnContextCancel(t *testing.T) {
	f := &fakeSvc{pending: map[string]int{}}
	w := New(f, 0, time.Hour, log.New(io.Discard, "", 0)) // long idle: only ctx can wake it

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s of ctx cancel")
	}
}
