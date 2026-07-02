package web

// This file holds the TOTP two-factor surfaces: the login second-factor step
// (classic full-page POST, matching the rest of the auth flow) and — added in a
// later step — the datastar-driven enrol/enable/disable controls on the account
// page. Login stays a plain form POST because it must survive redirects and work
// before any session (and thus any datastar wiring) exists.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image/png"
	"net/http"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/pquerna/otp"
	"github.com/starfederation/datastar-go/datastar"
)

// totpSignals is the datastar payload for the account 2FA controls. Enable reads
// totpCode; disable reads disableCode — two distinct signals so the two inputs
// never share state even though only one is on the page at a time.
type totpSignals struct {
	TOTPCode    string `json:"totpCode"`
	DisableCode string `json:"disableCode"`
}

// getLoginTOTP renders the code-entry page for a login mid-second-factor. With no
// pending marker there is nothing to verify, so it returns to the password step.
func (s *Server) getLoginTOTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.pending2FAUserID(r); !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, views.TOTPChallengePage(views.TOTPChallengeData{}))
}

// postLoginTOTP verifies the submitted code (a live TOTP code or a one-time
// recovery code) against the pending user and, on success, promotes the pending
// marker to a real session. Wrong codes are counted; once the limit is hit the
// pending state is dropped so the attacker must re-pass the password to keep
// guessing. The success and failure messages never reveal which code kind was
// tried, matching the login form's non-enumeration stance.
func (s *Server) postLoginTOTP(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.pending2FAUserID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	code := r.PostFormValue("code")
	if err := s.tenants.VerifyTOTPLogin(r.Context(), uid, code); err != nil {
		// An unexpected error (DB down, or a user whose 2FA vanished) is not the
		// user's fault — drop the half-login and send them back to the start.
		if !errors.Is(err, tenant.ErrTOTPInvalidCode) {
			s.clearPending2FA(w, r)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		// Wrong code: count it, and cut the session off once the limit is reached.
		if s.bumpPending2FAAttempts(w, r) >= maxTOTPLoginAttempts {
			s.clearPending2FA(w, r)
			s.render(w, r, views.LoginPage(views.AuthData{
				Error:          "Too many incorrect codes. Please sign in again.",
				OAuthProviders: s.providers,
			}))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, r, views.TOTPChallengePage(views.TOTPChallengeData{
			Error: "That code didn't match. Try again, or use a recovery code.",
		}))
		return
	}

	// Verified: retire the pending marker and open the real session.
	s.clearPending2FA(w, r)
	if err := s.setSessionUser(w, r, uid); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// --- account page: enrol / enable / disable (datastar) ---

// getAccount renders the security page with the current 2FA state.
func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	s.render(w, r, views.AccountPage(views.AccountData{
		UserEmail: u.Email,
		TwoFactor: views.TwoFactorVM{Enabled: u.TOTPEnabled},
	}))
}

// postTOTPSetup begins (or restarts) enrolment: it mints a pending secret and
// streams back the card in its enrolling state — a QR of the otpauth URL plus the
// key for manual entry. It is a no-op re-render when 2FA is already on, so a
// double-submit can't strand an enabled account back in setup.
func (s *Server) postTOTPSetup(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	sse := datastar.NewSSE(w, r)

	if u.TOTPEnabled {
		_ = sse.PatchElementTempl(views.TwoFactorCard(views.TwoFactorVM{Enabled: true}))
		return
	}

	secret, url, err := s.tenants.BeginTOTPEnrollment(r.Context(), u.ID)
	if err != nil {
		_ = sse.PatchElementTempl(views.TOTPError("Couldn't start setup. Please try again."))
		return
	}
	qr, err := qrDataURI(url)
	if err != nil {
		_ = sse.PatchElementTempl(views.TOTPError("Couldn't render the QR code. Please try again."))
		return
	}
	_ = sse.PatchElementTempl(views.TwoFactorCard(views.TwoFactorVM{
		Setup: true, QRDataURI: qr, Secret: secret,
	}))
}

// postTOTPEnable confirms the first code against the pending secret. On success it
// swaps the card to its enabled state with the one-time recovery codes; on a bad
// code it patches only the inline error, leaving the QR and field the user is
// working through untouched.
func (s *Server) postTOTPEnable(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	var sig totpSignals
	_ = datastar.ReadSignals(r, &sig)
	sse := datastar.NewSSE(w, r)

	codes, err := s.tenants.ConfirmTOTP(r.Context(), u.ID, sig.TOTPCode)
	if err != nil {
		msg := "That code didn't match — check your authenticator's clock and try again."
		if errors.Is(err, tenant.ErrTOTPNotPending) {
			msg = "Your setup session expired. Start the setup again."
		}
		_ = sse.PatchElementTempl(views.TOTPError(msg))
		return
	}
	_ = sse.PatchElementTempl(views.TwoFactorCard(views.TwoFactorVM{
		Enabled: true, RecoveryCodes: codes,
	}))
}

// postTOTPDisable turns 2FA off after the user proves control with a current code
// or a recovery code. A wrong code patches the inline error and leaves 2FA on.
func (s *Server) postTOTPDisable(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	var sig totpSignals
	_ = datastar.ReadSignals(r, &sig)
	sse := datastar.NewSSE(w, r)

	if !u.TOTPEnabled {
		_ = sse.PatchElementTempl(views.TwoFactorCard(views.TwoFactorVM{Enabled: false}))
		return
	}
	if err := s.tenants.DisableTOTP(r.Context(), u.ID, sig.DisableCode); err != nil {
		_ = sse.PatchElementTempl(views.TOTPError("That code didn't match. Enter a current authenticator or recovery code to turn 2FA off."))
		return
	}
	_ = sse.PatchElementTempl(views.TwoFactorCard(views.TwoFactorVM{Enabled: false}))
}

// qrDataURI renders an otpauth:// URL as a PNG QR code, base64-encoded into a
// data: URI so the setup fragment carries the image inline (no extra request, no
// temp file, and the secret never touches a URL the browser could log or cache).
func qrDataURI(otpauthURL string) (string, error) {
	key, err := otp.NewKeyFromURL(otpauthURL)
	if err != nil {
		return "", err
	}
	img, err := key.Image(220, 220)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
