package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atvirokodosprendimai/agentsmemory/internal/skillset"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/starfederation/datastar-go/datastar"
)

// webSuperCaller adapts a signed-in superadmin to skillset.SuperHolder, so the
// skillset service authorizes the write at its single enforcement point. The web
// surface never decides authority itself: it passes who (for updated_by) and
// whether-superadmin, and the service gates. It mirrors the skill package's
// webSkillCaller — each bounded context owns its own boundary adapter.
type webSuperCaller struct {
	userID string
	super  bool
}

// User records who published the version (stored as updated_by).
func (c webSuperCaller) User() string { return c.userID }

// IsSuperAdmin is the gate the skillset service checks before any global write.
func (c webSuperCaller) IsSuperAdmin() bool { return c.super }

// skillsetSignals is the datastar payload for the playbook editor — one typed
// struct (per the datastar Go convention) rather than ad-hoc map parsing.
type skillsetSignals struct {
	SkillsetContent string `json:"skillsetContent"`
}

// maxSkillsetRequestBytes caps the editor POST body before parsing. It is a
// transport guard only — the authoritative content limit is enforced after parsing
// in skillset.Service.Set; the ceiling sits well above it because JSON-escaping a
// large body can expand it several-fold.
const maxSkillsetRequestBytes int64 = 8 << 20

// getSkillsetAdmin renders the global wakeup-playbook editor. The route is gated by
// requireSuperAdmin, so reaching it already proves authority; an unset playbook
// (fresh database before the seed runs, in theory) renders as an empty editor.
func (s *Server) getSkillsetAdmin(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	sk, _, err := s.skillsets.Get(r.Context())
	if err != nil {
		http.Error(w, "could not load the skillset", http.StatusInternalServerError)
		return
	}
	s.render(w, r, views.SkillsetAdminPage(views.SkillsetAdminData{
		UserEmail: u.Email,
		Content:   sk.Content,
		Version:   sk.Version,
		UpdatedBy: sk.UpdatedBy,
	}))
}

// postSkillset saves the playbook from the editor signal and streams back datastar
// fragments: a flash, the refreshed version meta, and a re-baselined original so
// the editor's unsaved-changes hint clears. The skillset.Service enforces the
// superadmin gate and validates the body; a refusal is a banner, not an HTTP error.
func (s *Server) postSkillset(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())

	// Read (and cap) the body BEFORE opening the SSE response — once the stream is
	// flushed the request body is no longer readable, so signals must be parsed first.
	r.Body = http.MaxBytesReader(w, r.Body, maxSkillsetRequestBytes)
	var sig skillsetSignals
	readErr := datastar.ReadSignals(r, &sig)

	sse := datastar.NewSSE(w, r)
	if readErr != nil {
		msg := "Could not read the playbook — please try again."
		var maxErr *http.MaxBytesError
		if errors.As(readErr, &maxErr) {
			msg = "That playbook is too large to save."
		}
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "error", Message: msg}))
		return
	}

	// Re-derive superadmin status from the session email (never trust a client
	// signal for authority); the service re-checks it regardless.
	caller := webSuperCaller{userID: u.ID, super: s.isSuperAdmin(u.Email)}
	sk, err := s.skillsets.Set(r.Context(), caller, sig.SkillsetContent)
	if err != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "error", Message: skillsetErrMsg(err)}))
		return
	}

	_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
		Kind:    "success",
		Message: "Saved the global skillset (v" + strconv.Itoa(sk.Version) + "). Every agent gets it on the next am_skillset call.",
	}))
	_ = sse.PatchElementTempl(views.SkillsetMeta(views.SkillsetAdminData{Version: sk.Version, UpdatedBy: sk.UpdatedBy}))
	// Re-baseline the frontend-only original so the "unsaved changes" hint and the
	// disabled-Save state reset to match what is now stored.
	_ = sse.MarshalAndPatchSignals(map[string]any{"_skillsetOriginal": sk.Content})
}

// skillsetErrMsg maps a skillset.Service error to a user-facing banner, keeping the
// raw error off the page.
func skillsetErrMsg(err error) string {
	switch {
	case errors.Is(err, skillset.ErrForbidden):
		return "You need platform superadmin access to edit the global skillset."
	case errors.Is(err, skillset.ErrInvalidContent):
		return "The playbook can't be empty."
	default:
		return "Could not save the playbook. Please try again."
	}
}
