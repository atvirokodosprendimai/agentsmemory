package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mergejob"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/starfederation/datastar-go/datastar"
)

// mergeSignals is the datastar payload for the merge picker: the source wing to
// fold and the target wing to fold it into.
type mergeSignals struct {
	MergeSource string `json:"mergeSource"`
	MergeTarget string `json:"mergeTarget"`
}

// maxMergeRequestBytes caps the merge form's POST body — two short wing names.
const maxMergeRequestBytes int64 = 64 << 10

// recentMergeJobs bounds the jobs panel; a workspace rarely runs many merges and
// older ones are noise once resolved.
const recentMergeJobs = 8

// postMergeRequest queues a background wing merge for this workspace. It does not
// merge in-request — the worker does — so on success it confirms, clears the form,
// and refreshes the jobs panel (which then polls until the worker finishes).
// teamID is membership-checked; the service layers the writer/admin gate.
func (s *Server) postMergeRequest(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxMergeRequestBytes)
	var sig mergeSignals
	readErr := datastar.ReadSignals(r, &sig)

	sse := datastar.NewSSE(w, r)
	if readErr != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
			Kind: "error", Message: "Could not read the merge request — please try again.",
		}))
		return
	}

	job, err := s.merges.Enqueue(r.Context(), u.ID, teamID, sig.MergeSource, sig.MergeTarget)
	if err != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "error", Message: mergeErrMsg(err)}))
		return
	}

	_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
		Kind:    "success",
		Message: "Queued: merging \"" + strings.Join(job.Sources, ", ") + "\" into \"" + job.Target + "\". It runs in the background.",
	}))
	_ = sse.MarshalAndPatchSignals(map[string]any{"mergeSource": "", "mergeTarget": ""})
	data := s.buildMergeData(r.Context(), u, teamID, role)
	_ = sse.PatchElementTempl(views.MergeSuggestions(data))
	_ = sse.PatchElementTempl(views.MergeJobs(data))
}

// getMerges refreshes the jobs panel — the datastar poller calls it while a job is
// queued/running, and the refreshed fragment stops polling once none are active.
func (s *Server) getMerges(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	sse := datastar.NewSSE(w, r)
	// Refresh both dynamic parts: the suggestions (a finished merge removes its
	// pair) and the jobs panel (status). The poller lives in MergeJobs, so jobs is
	// patched last to keep the interval element present until nothing is active.
	data := s.buildMergeData(r.Context(), u, teamID, role)
	_ = sse.PatchElementTempl(views.MergeSuggestions(data))
	_ = sse.PatchElementTempl(views.MergeJobs(data))
}

// buildMergeData shapes the merge section: the wings the viewer can pick, the
// auto-detected duplicate pairs, and the recent jobs (with an Active flag that
// drives the poller). Computed from the role the caller already resolved; reused
// by the project page render and the enqueue/poll fragment refreshes. A per-part
// lookup failure degrades that part to empty rather than failing the page.
func (s *Server) buildMergeData(ctx context.Context, u tenant.User, teamID string, role tenant.Role) views.MergeData {
	d := views.MergeData{
		TeamID:    teamID,
		CanManage: role == tenant.RoleWriter || role == tenant.RoleAdmin,
	}
	if !d.CanManage {
		return d
	}
	if names, err := s.merges.WingNames(ctx, teamID); err == nil {
		d.Wings = names
	}
	if dups, err := s.merges.Duplicates(ctx, teamID); err == nil {
		for _, p := range dups {
			d.Duplicates = append(d.Duplicates, views.MergeDupVM{
				Source: p.Source, Target: p.Target,
				SourceDrawers: p.SourceDrawers, TargetDrawers: p.TargetDrawers,
			})
		}
	}
	if jobs, err := s.merges.ListForTeam(ctx, teamID, recentMergeJobs); err == nil {
		for _, j := range jobs {
			vm := views.MergeJobVM{
				ID: j.ID, Sources: strings.Join(j.Sources, ", "), Target: j.Target,
				Status: j.Status, When: dateOnly(j.CreatedAt),
			}
			switch mergejob.Status(j.Status) {
			case mergejob.StatusDone:
				vm.Summary = strconv.FormatInt(j.Drawers, 10) + " drawers, " + strconv.FormatInt(j.Closets, 10) + " closets"
			case mergejob.StatusFailed:
				vm.Summary = j.Error
			case mergejob.StatusQueued, mergejob.StatusRunning:
				d.Active = true
			}
			d.Jobs = append(d.Jobs, vm)
		}
	}
	return d
}

// dateOnly trims an RFC3339 timestamp to its YYYY-MM-DD prefix for list display.
func dateOnly(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

// mergeErrMsg turns a mergejob.Service error into a user-facing banner.
func mergeErrMsg(err error) string {
	switch {
	case errors.Is(err, mergejob.ErrForbidden):
		return "You need the writer or admin role to merge wings."
	case errors.Is(err, mergejob.ErrSameWing):
		return "Source and target are the same wing — pick two different wings."
	case errors.Is(err, mergejob.ErrWingNotFound):
		return "Pick a source wing that exists in this workspace."
	case errors.Is(err, mergejob.ErrInvalidName):
		return "That wing name isn't valid (letters, numbers, dashes, dots; no slashes)."
	default:
		return "Could not queue the merge. Please try again."
	}
}
