package mergejob

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// claimer is the slice of the repo the worker drains: claim the next queued job
// and record its outcome. *Repo satisfies it. Declared at the consumer so the
// worker is decoupled and fakeable, mirroring embedworker.
type claimer interface {
	ClaimNext(ctx context.Context) (Job, bool, error)
	MarkDone(ctx context.Context, id string, drawers, closets int64) error
	MarkFailed(ctx context.Context, id, msg string) error
}

// Merger is the palace work a job performs: fold the sources into the target, then
// rebuild the derived graph. *palace.Service satisfies it.
type Merger interface {
	MergeWing(ctx context.Context, teamID string, sources []string, target string) (palace.MergeWingResult, error)
	RecomputeGraph(ctx context.Context, teamID, wing string, prune bool) (palace.RecomputeResult, error)
}

// Tunables. A merge is bulk maintenance, not latency-sensitive, so the worker
// favours steady progress and backs off when the queue is empty or the backend errors.
const (
	defaultIdle    = 2 * time.Second  // sleep when the queue is empty
	defaultBackoff = 15 * time.Second // sleep after a claim/list error
)

// Worker drains the merge-job queue one job at a time. Serial execution is
// deliberate: a merge relabels rows and then rebuilds the whole team graph, so
// running two at once would only contend on the same tables for no throughput gain.
type Worker struct {
	jobs    claimer
	merger  Merger
	idle    time.Duration
	backoff time.Duration
	log     *log.Logger
}

// New builds a Worker with conservative defaults.
func New(jobs claimer, merger Merger, logger *log.Logger) *Worker {
	if logger == nil {
		logger = log.Default()
	}
	return &Worker{jobs: jobs, merger: merger, idle: defaultIdle, backoff: defaultBackoff, log: logger}
}

// Run drains the queue until ctx is cancelled. Meant to run in its own goroutine
// for the process lifetime; on a transient error it logs and backs off rather than
// dying, so a bad job or a downed backend only delays merges. The queue is durable,
// so a restart resumes any still-queued jobs.
func (w *Worker) Run(ctx context.Context) {
	w.log.Printf("merge worker: started")
	for {
		if ctx.Err() != nil {
			w.log.Printf("merge worker: stopping (%v)", ctx.Err())
			return
		}
		job, ok, err := w.jobs.ClaimNext(ctx)
		if err != nil {
			w.log.Printf("merge worker: claim: %v", err)
			if !sleep(ctx, w.backoff) {
				return
			}
			continue
		}
		if !ok {
			if !sleep(ctx, w.idle) { // nothing queued — idle
				return
			}
			continue
		}
		w.run(ctx, job)
		// Loop straight into the next job (no idle) to drain the queue.
	}
}

// run executes one claimed job: relabel, then rebuild the graph. The relabel is the
// durable outcome; the graph is derived. If the relabel fails the job failed
// outright. If the relabel succeeds but the rebuild fails, the job is marked failed
// with a message that names what DID land (the relabel) and how to finish the graph
// — the merge is not lost, only the cleanup is, and recompute_graph can redo it.
func (w *Worker) run(ctx context.Context, job Job) {
	w.log.Printf("merge worker: job %s — %v -> %s (team %s)", job.ID, job.Sources, job.Target, job.TeamID)
	res, err := w.merger.MergeWing(ctx, job.TeamID, job.Sources, job.Target)
	if err != nil {
		w.fail(ctx, job.ID, "merge failed: "+err.Error())
		return
	}
	// Full prune-rebuild: a merge empties the source wings, so rebuilding the whole
	// team graph regenerates the target's hallways AND drops the merged-away wings'
	// stale hallways (prune). It is the slow step the background job exists for.
	if _, err := w.merger.RecomputeGraph(ctx, job.TeamID, "", true); err != nil {
		w.fail(ctx, job.ID, fmt.Sprintf(
			"relabeled %d drawers / %d closets, but graph rebuild failed: %v — re-run recompute_graph",
			res.Drawers, res.Closets, err))
		return
	}
	if err := w.jobs.MarkDone(ctx, job.ID, res.Drawers, res.Closets); err != nil {
		w.log.Printf("merge worker: job %s done but mark failed: %v", job.ID, err)
	}
}

// fail records a job failure, logging if even that write fails.
func (w *Worker) fail(ctx context.Context, id, msg string) {
	w.log.Printf("merge worker: job %s failed: %s", id, msg)
	if err := w.jobs.MarkFailed(ctx, id, msg); err != nil {
		w.log.Printf("merge worker: job %s mark-failed write error: %v", id, err)
	}
}

// nowRFC3339 is the package's single source of timestamps, kept here so service
// and repo format times identically.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// sleep waits d or until ctx is cancelled, returning false if ctx ended.
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
