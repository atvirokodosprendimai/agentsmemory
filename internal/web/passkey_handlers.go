package web

// WebAuthn/passkey handlers, driven the datastar way: each ceremony is a pair of
// datastar POSTs. begin runs the server side and *patches a signal* with the
// options (a data-effect on the page then invokes the tiny navigator.credentials
// bridge); finish reads the browser's response back *from a signal* and verifies
// it. The bridge JS only crosses the boundary datastar can't — it emits a custom
// event that a data-on handler turns into the finish POST. Options and responses
// ride signals; the opaque ceremony session rides a short-lived signed cookie.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/passkey"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/sessions"
	"github.com/starfederation/datastar-go/datastar"
)

// The passkey ceremony cookie holds the WebAuthn SessionData (challenge etc.)
// between begin and finish. It is separate from the auth and pending-2FA cookies,
// short-lived, and — like them — signed+sealed by gorilla, so the challenge the
// verification pins against cannot be tampered with. A user runs one ceremony at
// a time, so a single slot is enough.
const (
	pkSessionName   = "agentsmemory_pk"
	pkSessionKey    = "sess"
	pkSessionMaxAge = 5 * 60
)

// passkeySignals is the datastar payload from the finish steps: the browser's
// credential JSON (WebAuthn attestation on register, assertion on login) plus an
// optional user-chosen label for a newly registered passkey.
type passkeySignals struct {
	PkCred  json.RawMessage `json:"pkCred"`
	PkLabel string          `json:"pkLabel"`
}

func (s *Server) setPKSession(w http.ResponseWriter, r *http.Request, sessionJSON []byte) error {
	sess, _ := s.store.Get(r, pkSessionName)
	sess.Values[pkSessionKey] = string(sessionJSON)
	sess.Options = &sessions.Options{Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: pkSessionMaxAge}
	return sess.Save(r, w)
}

func (s *Server) pkSession(r *http.Request) ([]byte, bool) {
	sess, _ := s.store.Get(r, pkSessionName)
	v, ok := sess.Values[pkSessionKey].(string)
	return []byte(v), ok && v != ""
}

func (s *Server) clearPKSession(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.store.Get(r, pkSessionName)
	sess.Options = &sessions.Options{Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1}
	_ = sess.Save(r, w)
}

// displayNameFor is the human name shown by the authenticator during
// registration; it falls back to the email when no display name is set.
func displayNameFor(u tenant.User) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Email
}

// passkeyCardVM builds the account passkeys-card view model from the user's
// stored credentials, mapping the domain's CredentialInfo to the view-local
// PasskeyVM so the views package stays free of the passkey/webauthn dependency.
// A load error yields an empty list (the card still renders, minus any rows).
func (s *Server) passkeyCardVM(r *http.Request, userID string) views.PasskeysVM {
	list, _ := s.passkeys.Repo().List(r.Context(), userID)
	vms := make([]views.PasskeyVM, 0, len(list))
	for _, c := range list {
		vms = append(vms, views.PasskeyVM{
			ID: c.ID, Name: c.Name, Added: dateOnly(c.CreatedAt), LastUsed: dateOnly(c.LastUsedAt),
		})
	}
	return views.PasskeysVM{Passkeys: vms}
}

// --- passkey registration (account page) ---

// postPasskeyRegisterBegin starts enrolment for the logged-in user: it mints the
// creation options and patches them into $pkCreateOpts, which a data-effect on the
// account page feeds to the browser's credentials.create bridge. The ceremony
// session is stashed for the finish step.
func (s *Server) postPasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	sse := datastar.NewSSE(w, r)
	options, session, err := s.passkeys.BeginRegistration(r.Context(), u.ID, u.Email, displayNameFor(u))
	if err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("Couldn't start passkey setup. Please try again."))
		return
	}
	if err := s.setPKSession(w, r, session); err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("Couldn't start passkey setup. Please try again."))
		return
	}
	_ = sse.MarshalAndPatchSignals(map[string]any{"pkCreateOpts": json.RawMessage(options)})
}

// postPasskeyRegisterFinish verifies the attestation and stores the credential
// under the user with their chosen label, then re-renders the passkeys card.
func (s *Server) postPasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	var sig passkeySignals
	_ = datastar.ReadSignals(r, &sig)
	sse := datastar.NewSSE(w, r)

	session, ok := s.pkSession(r)
	if !ok {
		_ = sse.PatchElementTempl(views.PasskeyError("Your setup session expired. Please try again."))
		return
	}
	label := strings.TrimSpace(sig.PkLabel)
	if label == "" {
		label = "Passkey"
	}
	if err := s.passkeys.FinishRegistration(r.Context(), u.ID, u.Email, displayNameFor(u), label, session, sig.PkCred); err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("That passkey couldn't be registered. Please try again."))
		return
	}
	s.clearPKSession(w, r)
	_ = sse.PatchElementTempl(views.PasskeyCard(s.passkeyCardVM(r, u.ID)))
	_ = sse.MarshalAndPatchSignals(map[string]any{"pkLabel": ""}) // reset the label input
}

// postPasskeyDelete removes one of the user's registered passkeys (ownership is
// enforced in the repo) and re-renders the card.
func (s *Server) postPasskeyDelete(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	sse := datastar.NewSSE(w, r)
	if err := s.passkeys.Repo().Delete(r.Context(), u.ID, chi.URLParam(r, "id")); err != nil && !errors.Is(err, passkey.ErrNoCredential) {
		_ = sse.PatchElementTempl(views.PasskeyError("Couldn't remove that passkey. Please try again."))
		return
	}
	_ = sse.PatchElementTempl(views.PasskeyCard(s.passkeyCardVM(r, u.ID)))
}

// --- passwordless login (/login) ---

// postLoginPasskeyBegin starts a passwordless (discoverable) sign-in: no username,
// so the authenticator offers its resident credentials. The request options are
// patched into $pkGetOpts to trigger the credentials.get bridge on the login page.
func (s *Server) postLoginPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	options, session, err := s.passkeys.BeginDiscoverableLogin()
	if err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("Couldn't start passkey sign-in. Please try again."))
		return
	}
	if err := s.setPKSession(w, r, session); err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("Couldn't start passkey sign-in. Please try again."))
		return
	}
	_ = sse.MarshalAndPatchSignals(map[string]any{"pkGetOpts": json.RawMessage(options)})
}

// postLoginPasskeyFinish verifies a passwordless assertion, resolves the user it
// authenticated, and mints the real session — then redirects to the dashboard.
func (s *Server) postLoginPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	var sig passkeySignals
	_ = datastar.ReadSignals(r, &sig)
	sse := datastar.NewSSE(w, r)

	session, ok := s.pkSession(r)
	if !ok {
		_ = sse.PatchElementTempl(views.PasskeyError("Sign-in expired. Please try again."))
		return
	}
	userID, err := s.passkeys.FinishDiscoverableLogin(r.Context(), session, sig.PkCred)
	if err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("That passkey wasn't recognised. Please try again."))
		return
	}
	s.clearPKSession(w, r)
	if err := s.setSessionUser(w, r, userID); err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("Session error. Please try again."))
		return
	}
	_ = sse.Redirect("/dashboard")
}

// --- passkey as the second factor (/login/totp) ---

// postTOTPPasskeyBegin starts a passkey assertion for the user mid-login (the
// pending-2FA cookie names them), scoped to that user's own credentials. Without
// a pending marker there is nothing to authenticate, so it bounces to /login.
func (s *Server) postTOTPPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	uid, ok := s.pending2FAUserID(r)
	if !ok {
		_ = sse.Redirect("/login")
		return
	}
	options, session, err := s.passkeys.BeginLoginForUser(r.Context(), uid)
	if err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("No passkey is registered for this account. Use your code instead."))
		return
	}
	if err := s.setPKSession(w, r, session); err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("Couldn't start passkey sign-in. Please try again."))
		return
	}
	_ = sse.MarshalAndPatchSignals(map[string]any{"pkGetOpts": json.RawMessage(options)})
}

// postTOTPPasskeyFinish verifies the second-factor assertion for the pending user
// and promotes the pending marker to a real session.
func (s *Server) postTOTPPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	uid, ok := s.pending2FAUserID(r)
	if !ok {
		_ = sse.Redirect("/login")
		return
	}
	var sig passkeySignals
	_ = datastar.ReadSignals(r, &sig)
	session, ok := s.pkSession(r)
	if !ok {
		_ = sse.PatchElementTempl(views.PasskeyError("Sign-in expired. Please try again."))
		return
	}
	if err := s.passkeys.FinishLoginForUser(r.Context(), uid, session, sig.PkCred); err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("That passkey wasn't recognised. Please try again."))
		return
	}
	s.clearPKSession(w, r)
	s.clearPending2FA(w, r)
	if err := s.setSessionUser(w, r, uid); err != nil {
		_ = sse.PatchElementTempl(views.PasskeyError("Session error. Please try again."))
		return
	}
	_ = sse.Redirect("/dashboard")
}
