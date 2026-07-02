package web

import (
	"errors"
	"net/http"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/starfederation/datastar-go/datastar"
)

// getProjectKey reveals or re-masks the signed-in member's OWN API key for a
// project, streaming back the patched KeyBlock fragment. Keys are per-member:
// each member has their own key and reveals/rotates only that one (scoped to
// (team, user) in the tenant layer). Revealing your own credential grants nothing
// you don't already hold, so — unlike the old team-wide key — no admin role is
// required here; membership is the whole gate. Managing OTHER members (and thereby
// revoking their keys) lives in the admin-only Members section instead.
//
// ?reveal=1 decrypts and shows the secret; ?reveal=0 (or anything else) returns
// the masked state, so Hide is the same endpoint with no decryption.
func (s *Server) getProjectKey(w http.ResponseWriter, r *http.Request) {
	u, teamID, _, ok := s.membership(w, r)
	if !ok {
		return
	}
	sse := datastar.NewSSE(w, r)
	q := r.URL.Query()

	// Rotate confirmation prompt. Rotation is destructive (it revokes the member's
	// current key), so it is confirmed before the POST that performs it.
	if q.Get("confirm") == "rotate" {
		_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{TeamID: teamID, CanReveal: true, ConfirmRotate: true}))
		return
	}

	// Re-mask path (also Cancel from the confirm prompt): no decryption.
	if q.Get("reveal") != "1" {
		_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{TeamID: teamID, CanReveal: true}))
		return
	}

	secret, err := s.tenants.RevealToken(r.Context(), teamID, u.ID)
	if err != nil {
		// ErrTokenUnavailable covers a legacy key (minted before reveal), a rotated
		// server key, or reveal being disabled — all read the same to the member: it
		// can't be shown, only rotated. A real DB error is treated the same here
		// rather than leaking a 500 into the fragment.
		msg := "Could not reveal your key. It may predate the reveal feature — rotate it to get a viewable one."
		if !errors.Is(err, tenant.ErrTokenUnavailable) {
			msg = "Could not reveal your key right now. Please try again."
		}
		_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{TeamID: teamID, CanReveal: true, Error: msg}))
		return
	}

	// The revealed token is a bearer /mcp accepts directly; the KeyBlock renders the
	// one-paste install command beside it (the install command embeds the token via
	// an env var and needs no server origin).
	_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{
		TeamID: teamID, CanReveal: true, Revealed: true, Secret: secret,
	}))
}

// postRotateKey rotates the signed-in member's OWN API key: it revokes their
// current key and mints a fresh, revealable one, streaming the new secret back
// (shown once, in the revealed state). It is scoped to (team, user) in the tenant
// layer, so one member's rotation never touches another member's key — which is
// why any member may rotate their own without an admin role. This is also the
// recovery path the reveal-unavailable error points at.
func (s *Server) postRotateKey(w http.ResponseWriter, r *http.Request) {
	u, teamID, _, ok := s.membership(w, r)
	if !ok {
		return
	}
	sse := datastar.NewSSE(w, r)
	cred, err := s.tenants.RotateKey(r.Context(), teamID, u.ID)
	if err != nil {
		_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{
			TeamID: teamID, CanReveal: true,
			Error: "Could not rotate your key right now. Please try again.",
		}))
		return
	}
	// Show the new secret immediately (it is the only time it is shown) with the
	// rotated note and the install command, so the member re-wires Claude to the
	// fresh key before navigating away.
	_ = sse.PatchElementTempl(views.KeyBlock(views.KeyVM{
		TeamID: teamID, CanReveal: true, Revealed: true, Rotated: true, Secret: cred.Secret,
	}))
}
