// Package embedworker drains the palace's background embedding queue: the rows a
// migration import ABSORBED (text written, vector deferred — see palace.Absorb*)
// are embedded here, off the request path, so a large import never blocks on the
// embedder or trips a CDN/proxy read timeout. It is a single long-lived goroutine
// started at boot; the queue is durable (the embedded_at column), so a restart
// just resumes where it left off.
package embedworker

import (
	"context"
	"log"
	"time"
)

// Service is the slice of *palace.Service the worker needs. Declaring it at the
// consumer (Go's "accept interfaces" guidance) keeps the worker decoupled from the
// concrete service and trivially fakeable in tests.
type Service interface {
	// TeamsWithPending lists tenants that have any row awaiting embedding.
	TeamsWithPending(ctx context.Context, limit int) ([]string, error)
	// EmbedPendingForTeam indexes up to batch drawers + batch closets for one
	// tenant, returning how many rows it embedded (0 = that tenant is caught up).
	EmbedPendingForTeam(ctx context.Context, teamID string, batch int) (int, error)
}

// Tunables. Defaults are deliberately conservative: a migration is bulk, not
// latency-sensitive, so the worker favours steady, fair progress over speed.
const (
	defaultBatch   = 64               // rows per tenant per cycle
	defaultIdle    = 5 * time.Second  // sleep when the queue is empty
	defaultBackoff = 15 * time.Second // sleep after an error (e.g. embedder down)
)

// Worker repeatedly drains pending embeddings, round-robining tenants so one large
// migration cannot starve another's.
type Worker struct {
	svc     Service
	batch   int
	idle    time.Duration
	backoff time.Duration
	log     *log.Logger
}

// New builds a Worker. A non-positive batch or idle falls back to the defaults.
func New(svc Service, batch int, idle time.Duration, logger *log.Logger) *Worker {
	if batch <= 0 {
		batch = defaultBatch
	}
	if idle <= 0 {
		idle = defaultIdle
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Worker{svc: svc, batch: batch, idle: idle, backoff: defaultBackoff, log: logger}
}

// Run drains the queue until ctx is cancelled. It is meant to run in its own
// goroutine for the process lifetime; on ctx cancel it returns promptly. It never
// panics on a transient backend error — it logs and backs off — so a downed
// embedder only delays indexing rather than killing the worker.
func (w *Worker) Run(ctx context.Context) {
	w.log.Printf("embed worker: started (batch=%d, idle=%s)", w.batch, w.idle)
	for {
		if ctx.Err() != nil {
			w.log.Printf("embed worker: stopping (%v)", ctx.Err())
			return
		}
		// Unbounded scan: only tenants that currently have pending rows come back
		// (the partial index keeps it cheap), and that set is small — so every
		// pending tenant is always scheduled, never starved behind a cap.
		teams, err := w.svc.TeamsWithPending(ctx, 0)
		if err != nil {
			w.log.Printf("embed worker: list pending teams: %v", err)
			if !sleep(ctx, w.backoff) {
				return
			}
			continue
		}
		if len(teams) == 0 {
			if !sleep(ctx, w.idle) {
				return
			}
			continue
		}
		did, failed := 0, false
		for _, t := range teams {
			if ctx.Err() != nil {
				return
			}
			n, err := w.svc.EmbedPendingForTeam(ctx, t, w.batch)
			did += n
			if err != nil {
				failed = true
				// Move on to the next tenant; the rows stay pending and retry next
				// cycle. We back off after the round (below) regardless of progress.
				w.log.Printf("embed worker: team %s: %v", t, err)
			}
		}
		// Sleep only when the round indexed NOTHING. A round with did>0 did real
		// embedding work (each call costs embed latency, so this is not a tight spin
		// — it is paced by the embedder) and loops straight into the next batch to
		// drain fast. An empty round idles; an empty round caused by errors backs off
		// longer so a wholly-down embedder is not retried tightly. A failing tenant
		// alongside a progressing one keeps did>0, so its rows simply retry next
		// cycle without throttling the healthy tenant.
		if did == 0 {
			wait := w.idle
			if failed {
				wait = w.backoff
			}
			if !sleep(ctx, wait) {
				return
			}
		}
	}
}

// sleep waits d or until ctx is cancelled, returning false if ctx ended (so the
// caller stops). Using a context-aware wait keeps shutdown prompt even mid-idle.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
