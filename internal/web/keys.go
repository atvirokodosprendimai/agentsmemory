package web

import (
	"errors"
	"net/http"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/starfederation/datastar-go/datastar"
)

// getProjectKey reveals or re-masks a project's API key, streaming back the
// patched KeyBlock fragment. It is membership-gated like every project-scoped
// handler, with one extra rule: revealing requires the ADMIN role. A bearer
// token resolves to its owner's role, so showing the admin-minted key to a
// lower-privileged member would let them escalate — the role gate the skill
// editor enforces would be moot once they hold the key. So reveal is admin-only,
// enforced here regardless of whether the UI offered the control.
//
// ?reveal=1 decrypts and shows the secret; ?reveal=0 (or anything else) returns
// the masked state, so Hide is the same endpoint with no decryption.
func (s *Server) getProjectKey(w http.ResponseWriter, r *http.Request) {
	_, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	admin := role == tenant.RoleAdmin
	sse := datastar.NewSSE(w, r)
	q := r.URL.Query()

	// Rotate confirmation prompt (admin only). Rotation is destructive, so it is
	// confirmed before the POST that performs it; a non-admin falls through to the
	// masked state below.
	if q.Get("confirm") == "rotate" && admin {
		_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{TeamID: teamID, CanReveal: true, ConfirmRotate: true}))
		return
	}

	// Re-mask path (also Cancel from the confirm prompt): no decryption.
	if q.Get("reveal") != "1" {
		_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{TeamID: teamID, CanReveal: admin}))
		return
	}

	// Reveal path is admin-only.
	if !admin {
		_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{
			TeamID: teamID, CanReveal: false,
			Error: "Only a workspace admin can reveal the API key.",
		}))
		return
	}

	secret, err := s.tenants.RevealToken(r.Context(), teamID)
	if err != nil {
		// ErrTokenUnavailable covers a legacy key (minted before reveal), a
		// rotated server key, or reveal being disabled — all read the same to the
		// user: it can't be shown, only rotated. A real DB error is treated the
		// same here rather than leaking a 500 into the fragment.
		msg := "Could not reveal this key. It may predate the reveal feature — rotate it to get a viewable key."
		if !errors.Is(err, tenant.ErrTokenUnavailable) {
			msg = "Could not reveal this key right now. Please try again."
		}
		_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{TeamID: teamID, CanReveal: admin, Error: msg}))
		return
	}

	// The revealed token is a bearer /mcp accepts directly; the KeyBlock renders the
	// one-paste install command beside it (the install command embeds the token via
	// an env var and needs no server origin).
	_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{
		TeamID: teamID, CanReveal: admin, Revealed: true, Secret: secret,
	}))
}

// postRotateKey rotates a project's API key: it revokes the current key and mints
// a fresh, revealable one, streaming the new secret back (shown once, in the
// revealed state). Admin-only like reveal — rotation is a credential-management
// action that invalidates the old bearer, so a member must never trigger it. This
// is the recovery path the reveal-unavailable error points at.
func (s *Server) postRotateKey(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	sse := datastar.NewSSE(w, r)
	if role != tenant.RoleAdmin {
		_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{
			TeamID: teamID, CanReveal: false,
			Error: "Only a workspace admin can rotate the API key.",
		}))
		return
	}
	cred, err := s.tenants.RotateKey(r.Context(), teamID, u.ID)
	if err != nil {
		_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{
			TeamID: teamID, CanReveal: true,
			Error: "Could not rotate the key right now. Please try again.",
		}))
		return
	}
	// Show the new secret immediately (it is the only time it is shown) with the
	// rotated note and the install command, so the admin re-wires Claude to the
	// fresh key before navigating away.
	_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{
		TeamID: teamID, CanReveal: true, Revealed: true, Rotated: true, Secret: cred.Secret,
	}))
}
