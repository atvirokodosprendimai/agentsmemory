package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/atvirokodosprendimai/agentsmemory/internal/share"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/go-chi/chi/v5"
	"github.com/starfederation/datastar-go/datastar"
)

// shareSignals is the datastar payload for the push form: the wing to share and
// the destination workspace's slug. One typed struct per command, per the
// datastar Go convention.
type shareSignals struct {
	ShareWing string `json:"shareWing"`
	ShareSlug string `json:"shareSlug"`
}

// maxShareRequestBytes caps the push form's POST body. The payload is two short
// strings (a wing name and a slug), so this ceiling is small — just enough to
// bound buffering of an authenticated-but-untrusted body before it is parsed.
const maxShareRequestBytes int64 = 64 << 10

// postShareRequest files a pending request to copy one of THIS workspace's wings
// into another (named by slug). It copies nothing — the destination admin's
// Accept does — so on success it just confirms and clears the form. teamID is the
// source and is membership-checked; the share service enforces the writer/admin
// gate on top.
func (s *Server) postShareRequest(w http.ResponseWriter, r *http.Request) {
	u, teamID, _, ok := s.membership(w, r)
	if !ok {
		return
	}

	// Parse (and cap) the body before opening the SSE stream — once the stream is
	// flushed the request body is no longer readable.
	r.Body = http.MaxBytesReader(w, r.Body, maxShareRequestBytes)
	var sig shareSignals
	readErr := datastar.ReadSignals(r, &sig)

	sse := datastar.NewSSE(w, r)
	if readErr != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
			Kind: "error", Message: "Could not read the share request — please try again.",
		}))
		return
	}

	req, err := s.shares.Request(r.Context(), u.ID, teamID, sig.ShareSlug, sig.ShareWing)
	switch {
	case err == nil:
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
			Kind: "success",
			Message: "Requested to share \"" + req.Wing + "\" with workspace \"" + sig.ShareSlug +
				"\". An admin there must accept before it copies.",
		}))
		// Clear the form so a second share starts blank.
		_ = sse.MarshalAndPatchSignals(map[string]any{"shareWing": "", "shareSlug": ""})
	case errors.Is(err, share.ErrAlreadyPending):
		// Not an error: the desired state (a pending request) already holds. Say so
		// plainly and clear the form, so a double-submit reads as success.
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
			Kind:    "success",
			Message: "A request to share \"" + sig.ShareWing + "\" with \"" + sig.ShareSlug + "\" is already awaiting approval.",
		}))
		_ = sse.MarshalAndPatchSignals(map[string]any{"shareWing": "", "shareSlug": ""})
	default:
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "error", Message: shareErrMsg(err)}))
	}
}

// postShareAccept is the destination admin's consent: it runs the copy and
// refreshes the inbox. teamID (the destination) is membership-checked here; the
// share service additionally requires the admin role and binds the request to
// this team, so an admin can only accept requests aimed at their own workspace.
func (s *Server) postShareAccept(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	reqID := chi.URLParam(r, "reqID")

	sse := datastar.NewSSE(w, r)
	res, req, err := s.shares.Accept(r.Context(), u.ID, teamID, reqID)
	if err != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "error", Message: shareErrMsg(err)}))
		return
	}

	msg := "Accepted — copied wing \"" + req.Wing + "\" (" +
		strconv.Itoa(res.Drawers) + " drawers, " + strconv.Itoa(res.Closets) + " closets) into this workspace."
	if res.Skipped > 0 {
		// Skipped = source records still pending embedding; they were not copied.
		// Surface it so the admin knows the request is now resolved — they can ask
		// the source to re-share once the source has finished indexing.
		msg += " " + strconv.Itoa(res.Skipped) + " record(s) were skipped (not yet indexed at the source)."
	}
	_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "success", Message: msg}))
	_ = sse.PatchElementTempl(views.IncomingShares(s.buildShareData(r.Context(), u, teamID, role)))
}

// postShareDecline rejects a pending request without copying. Same destination
// admin gate and team-binding as accept.
func (s *Server) postShareDecline(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	reqID := chi.URLParam(r, "reqID")

	sse := datastar.NewSSE(w, r)
	req, err := s.shares.Decline(r.Context(), u.ID, teamID, reqID)
	if err != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "error", Message: shareErrMsg(err)}))
		return
	}
	_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
		Kind: "success", Message: "Declined the request to share \"" + req.Wing + "\".",
	}))
	_ = sse.PatchElementTempl(views.IncomingShares(s.buildShareData(r.Context(), u, teamID, role)))
}

// buildShareData shapes the share section for a workspace: the wings the viewer
// may push out (writer/admin) and the pending requests they may resolve (admin).
// It is computed from the membership role the caller already resolved, and reused
// by the project page render and the accept/decline fragment refresh. Per-row
// lookups that fail are skipped rather than failing the whole section — a missing
// source team or user leaves that label blank, never a broken page.
func (s *Server) buildShareData(ctx context.Context, u tenant.User, teamID string, role tenant.Role) views.ShareData {
	d := views.ShareData{
		TeamID:   teamID,
		CanShare: role == tenant.RoleWriter || role == tenant.RoleAdmin,
		IsAdmin:  role == tenant.RoleAdmin,
	}
	if d.CanShare {
		if wings, err := s.shares.SourceWings(ctx, u.ID, teamID); err == nil {
			for _, wg := range wings {
				d.Wings = append(d.Wings, views.ShareWingVM{Name: wg.Wing, Drawers: wg.Drawers, Rooms: wg.Rooms})
			}
		}
	}
	if d.IsAdmin {
		if reqs, err := s.shares.Incoming(ctx, teamID); err == nil {
			for _, req := range reqs {
				vm := views.ShareReqVM{ID: req.ID, Wing: req.Wing, CreatedAt: req.CreatedAt}
				if from, err := s.tenants.TeamByID(ctx, req.FromTeamID); err == nil {
					vm.FromName, vm.FromSlug = from.Name, from.Slug
				}
				if ru, err := s.tenants.GetUserByID(ctx, req.RequestedBy); err == nil {
					vm.Requester = ru.Email
				}
				d.Incoming = append(d.Incoming, vm)
			}
		}
	}
	return d
}

// shareErrMsg turns a share.Service error into a user-facing banner, keeping the
// raw error off the page. Forbidden, not-found and same-workspace all stay
// deliberately non-specific about what exists, to avoid leaking tenant structure.
func shareErrMsg(err error) string {
	switch {
	case errors.Is(err, share.ErrForbidden):
		return "You don't have permission for that action in this workspace."
	case errors.Is(err, share.ErrSlugNotFound):
		return "No workspace found with that slug. Check it with the destination and try again."
	case errors.Is(err, share.ErrSameWorkspace):
		return "That's this workspace — choose a different destination."
	case errors.Is(err, share.ErrWingNotFound):
		return "Pick a wing that exists in this workspace."
	case errors.Is(err, share.ErrInvalidInput):
		return "Pick a wing and enter a destination slug."
	case errors.Is(err, share.ErrNotFound):
		return "That share request no longer exists."
	case errors.Is(err, share.ErrNotPending):
		return "That request has already been resolved."
	default:
		return "Something went wrong. Please try again."
	}
}
