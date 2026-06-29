package mergejob

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// fakeMerger records the merge + recompute calls and can be made to fail either.
type fakeMerger struct {
	mergeRes   palace.MergeWingResult
	mergeErr   error
	recompErr  error
	mergeCall  *mergeArgs
	recompCall *recompArgs
}

type mergeArgs struct {
	team    string
	sources []string
	target  string
}
type recompArgs struct {
	team  string
	wing  string
	prune bool
}

func (m *fakeMerger) MergeWing(_ context.Context, team string, sources []string, target string) (palace.MergeWingResult, error) {
	m.mergeCall = &mergeArgs{team, sources, target}
	if m.mergeErr != nil {
		return palace.MergeWingResult{}, m.mergeErr
	}
	return m.mergeRes, nil
}

func (m *fakeMerger) RecomputeGraph(_ context.Context, team, wing string, prune bool) (palace.RecomputeResult, error) {
	m.recompCall = &recompArgs{team, wing, prune}
	if m.recompErr != nil {
		return palace.RecomputeResult{}, m.recompErr
	}
	return palace.RecomputeResult{}, nil
}

func quietWorker(repo claimer, m Merger) *Worker {
	return New(repo, m, log.New(io.Discard, "", 0))
}

func job() Job {
	return Job{ID: "j1", TeamID: "t1", Sources: []string{"wing_research"}, Target: "research", Status: string(StatusRunning)}
}

// A successful run relabels, rebuilds the graph (full prune), and marks done with
// the relabel counts.
func TestWorkerRunHappyPath(t *testing.T) {
	repo := &fakeRepo{}
	m := &fakeMerger{mergeRes: palace.MergeWingResult{Drawers: 5, Closets: 2}}
	w := quietWorker(repo, m)

	w.run(ctx(), job())

	if m.mergeCall == nil || m.mergeCall.target != "research" || m.mergeCall.sources[0] != "wing_research" {
		t.Fatalf("merge not called correctly: %+v", m.mergeCall)
	}
	if m.recompCall == nil || m.recompCall.wing != "" || !m.recompCall.prune {
		t.Fatalf("recompute should be full (wing empty) + prune: %+v", m.recompCall)
	}
	if repo.doneID != "j1" || repo.doneDraw != 5 || repo.doneClose != 2 {
		t.Fatalf("done not recorded: id=%s draw=%d close=%d", repo.doneID, repo.doneDraw, repo.doneClose)
	}
	if repo.failID != "" {
		t.Fatalf("should not be marked failed")
	}
}

// A merge error fails the job and never touches the graph.
func TestWorkerRunMergeError(t *testing.T) {
	repo := &fakeRepo{}
	m := &fakeMerger{mergeErr: errors.New("db locked")}
	w := quietWorker(repo, m)

	w.run(ctx(), job())

	if m.recompCall != nil {
		t.Fatalf("recompute must not run after a merge error")
	}
	if repo.failID != "j1" || !strings.Contains(repo.failMsg, "merge failed") {
		t.Fatalf("expected merge-failed mark, got id=%s msg=%q", repo.failID, repo.failMsg)
	}
	if repo.doneID != "" {
		t.Fatalf("should not be marked done")
	}
}

// On startup the worker reclaims jobs a prior process left mid-flight: a 'running'
// row is reset to 'queued' so the durable queue actually resumes it.
func TestWorkerReclaimsOrphansOnStart(t *testing.T) {
	repo := &fakeRepo{queue: []Job{{ID: "j9", Status: string(StatusRunning)}}}
	w := quietWorker(repo, &fakeMerger{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: Run reclaims orphans, then exits the loop at once
	w.Run(ctx)
	if repo.released != 1 {
		t.Fatalf("want 1 reclaimed orphan, got %d", repo.released)
	}
	if repo.queue[0].Status != string(StatusQueued) {
		t.Fatalf("orphaned running job should be reset to queued, got %q", repo.queue[0].Status)
	}
}

// If the relabel succeeds but the graph rebuild fails, the job is marked failed
// with a message that names the relabel that DID land.
func TestWorkerRunRecomputeError(t *testing.T) {
	repo := &fakeRepo{}
	m := &fakeMerger{mergeRes: palace.MergeWingResult{Drawers: 7, Closets: 1}, recompErr: errors.New("graph boom")}
	w := quietWorker(repo, m)

	w.run(ctx(), job())

	if repo.failID != "j1" {
		t.Fatalf("expected job marked failed")
	}
	if !strings.Contains(repo.failMsg, "relabeled 7 drawers") || !strings.Contains(repo.failMsg, "graph rebuild failed") {
		t.Fatalf("failure message should name the landed relabel + the rebuild failure, got %q", repo.failMsg)
	}
	if repo.doneID != "" {
		t.Fatalf("should not be marked done when the graph rebuild failed")
	}
}
