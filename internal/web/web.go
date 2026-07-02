// Package web is the dashboard: registration, login, and project management,
// rendered with templ and made interactive with datastar. It is a separate
// surface from the MCP server (which agents talk to); humans use this to create
// projects and read their API keys and usage. Auth is local email+password by
// default, with goth wired for social logins when provider keys are configured.
package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/billing"
	"github.com/atvirokodosprendimai/agentsmemory/internal/dataexport"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mergejob"
	"github.com/atvirokodosprendimai/agentsmemory/internal/share"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skillset"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/sessions"
)

// Cookie/session constants. The session stores only the user id; everything else
// is re-read from the database each request so revocation is immediate.
const (
	sessionName    = "agentsmemory_session"
	sessionUserKey = "uid"

	// pending2FA is a separate, short-lived cookie holding the id of a user who
	// passed the password check but still owes a TOTP code. It is deliberately NOT
	// the real session: until the second factor lands, the browser holds no
	// authenticated session at all. Storing it in a gorilla session means it is
	// signed and sealed by the same key as the main session — so it needs no
	// hand-rolled HMAC, the one weakness a raw pending cookie would carry.
	pending2FAName        = "agentsmemory_2fa"
	pending2FAUserKey     = "uid"
	pending2FAAttemptsKey = "attempts"
	pending2FAMaxAgeSec   = 5 * 60 // the code must be entered within five minutes
	maxTOTPLoginAttempts  = 5      // wrong codes tolerated before the pending state is dropped
)

// ctxKey is an unexported context key type for the authenticated user.
type ctxKey struct{}

var userCtxKey = ctxKey{}

// Server holds the dashboard's dependencies.
type Server struct {
	tenants   *tenant.Repo
	usage     *usage.Service
	skills    *skill.Service       // centralised-skill use-cases, shared with the MCP server
	skillsets *skillset.Service    // the global wakeup-playbook use-cases (am_skillset)
	shares    *share.Service       // cross-workspace wing-share handshake (consent flow)
	merges    *mergejob.Service    // background wing-merge queue (enqueue/list/detect)
	billing   *billing.Service     // Stripe upgrade-to-Pro; inert until configured
	exporter  *dataexport.Exporter // per-workspace SQLite data export (BDAR right of access)
	store     sessions.Store
	providers []string // configured OAuth providers; empty until keys are set
	// superAdmins is the platform-superadmin allowlist as a set, keyed by
	// normalized email. Membership grants the one cross-tenant power the dashboard
	// exposes: editing the global skillset. It is process config (SUPERADMIN_EMAILS)
	// resolved once at construction, not a per-request lookup.
	superAdmins map[string]struct{}
}

// New builds the dashboard server. sessionKey signs/encrypts the session cookie;
// it must be stable across restarts (else sessions are invalidated on deploy).
// skills is the same service the MCP server exposes as list_skills/update_skill,
// reused here so the web editor and the agent tools share one code path; skillsets
// backs the superadmin-only global wakeup-playbook editor. superAdmins is the
// SUPERADMIN_EMAILS allowlist that gates that editor. exporter builds the
// per-workspace SQLite download that satisfies a user's BDAR right of access.
func New(tenants *tenant.Repo, usageSvc *usage.Service, skills *skill.Service, skillsets *skillset.Service, shares *share.Service, merges *mergejob.Service, billingSvc *billing.Service, exporter *dataexport.Exporter, superAdmins []string, sessionKey []byte) *Server {
	store := sessions.NewCookieStore(sessionKey)
	store.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 60 * 60, // one week
	}
	s := &Server{tenants: tenants, usage: usageSvc, skills: skills, skillsets: skillsets, shares: shares, merges: merges, billing: billingSvc, exporter: exporter, store: store, superAdmins: superAdminSet(superAdmins)}
	s.providers = registerOAuth(store) // gated: returns nil when no keys set
	// Stamp the asset cache-buster from the embedded stylesheet's content hash so
	// templates render <link …/app.css?v=hash>; this changes only when the CSS does.
	views.AssetVersion = assetVersion()
	return s
}

// superAdminSet turns the allowlist slice into a lookup set keyed by normalized
// email, so isSuperAdmin is an O(1) check on every gated request.
func superAdminSet(emails []string) map[string]struct{} {
	set := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			set[e] = struct{}{}
		}
	}
	return set
}

// isSuperAdmin reports whether an email is on the platform-superadmin allowlist.
// The comparison is on the same normalized footing the set was built with, so
// case/whitespace differences never decide authority.
func (s *Server) isSuperAdmin(email string) bool {
	_, ok := s.superAdmins[strings.ToLower(strings.TrimSpace(email))]
	return ok
}

// Routes mounts the dashboard routes onto r.
func (s *Server) Routes(r chi.Router) {
	// Embedded static assets (the stylesheet) served read-only, with cache
	// headers keyed to the ?v=<hash> cache-buster (see staticAssets).
	r.Handle("/static/*", staticAssets())

	r.Get("/", s.handleRoot)
	// Public, unauthenticated install guide as raw Markdown, written for an agent to
	// fetch and follow (see guide.go). It needs no session, so it sits outside the
	// authenticated group alongside the landing page.
	r.Get("/claude-guide", s.handleClaudeGuide)
	r.Get("/register", s.getRegister)
	r.Post("/register", s.postRegister)
	r.Get("/login", s.getLogin)
	r.Post("/login", s.postLogin)
	// The second-factor step of a password login. It is public (no session exists
	// yet — only the short-lived pending-2FA cookie), and reachable only after
	// postLogin sets that cookie; without it both handlers bounce to /login.
	r.Get("/login/totp", s.getLoginTOTP)
	r.Post("/login/totp", s.postLoginTOTP)
	r.Post("/logout", s.postLogout)

	// Authenticated area.
	r.Group(func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/dashboard", s.getDashboard)

		// Account/security page. Enrolling, enabling and disabling 2FA are datastar
		// actions that re-render the two-factor card fragment; the login-time TOTP
		// step lives in the public group above.
		r.Get("/account", s.getAccount)
		r.Post("/account/totp/setup", s.postTOTPSetup)
		r.Post("/account/totp/enable", s.postTOTPEnable)
		r.Post("/account/totp/disable", s.postTOTPDisable)

		r.Post("/projects", s.postCreateProject)
		// Project-scoped skill management. {teamID} is membership-checked in each
		// handler (see Server.membership) — a logged-in user can only reach a
		// project they belong to.
		r.Get("/projects/{teamID}", s.getProject)
		r.Get("/projects/{teamID}/key", s.getProjectKey)
		r.Post("/projects/{teamID}/key/rotate", s.postRotateKey)
		r.Post("/projects/{teamID}/skills", s.postSkill)
		r.Get("/projects/{teamID}/skill-body", s.getSkillBody)

		// BDAR/GDPR right of access: download this workspace's data as a portable
		// SQLite file. Membership-gated like every project route; the archive is
		// scoped to this team plus the requester's own identity rows. A plain GET
		// (not a datastar action) so the browser downloads the file directly.
		r.Get("/projects/{teamID}/export", s.getExport)

		// Upgrade to Pro via Stripe hosted checkout. POST starts a checkout session
		// and redirects the browser to Stripe (admin-gated; the workspace must be on
		// the free plan). Stripe returns the user to the success/cancel GETs, which
		// re-render the project page — the actual plan flip is webhook-driven, never
		// trusted from the return redirect.
		r.Post("/projects/{teamID}/upgrade", s.postUpgrade)
		r.Get("/projects/{teamID}/billing/success", s.getBillingSuccess)
		r.Get("/projects/{teamID}/billing/cancel", s.getBillingCancel)

		// Cross-workspace wing sharing. POST /share files a pending request from
		// this (source) workspace; accept/decline resolve a request addressed to
		// this (destination) workspace. All three membership-check {teamID}; the
		// share service layers the writer/admin gates and binds each request to its
		// destination, so the slug box can never silently copy across tenants.
		r.Post("/projects/{teamID}/share", s.postShareRequest)
		r.Post("/projects/{teamID}/share/{reqID}/accept", s.postShareAccept)
		r.Post("/projects/{teamID}/share/{reqID}/decline", s.postShareDecline)

		// Wing merge (background job): POST enqueues a merge of two of this
		// workspace's wings; GET refreshes the jobs panel (the status poller). Both
		// membership-check {teamID}; the merge service layers the writer/admin gate.
		r.Post("/projects/{teamID}/merges", s.postMergeRequest)
		r.Get("/projects/{teamID}/merges", s.getMerges)

		// Platform-superadmin area: editing the GLOBAL am_skillset playbook every
		// tenant shares. Nested inside requireUser and further gated by
		// requireSuperAdmin, so a signed-in non-superadmin gets 404 (the surface is
		// invisible to them) and the skillset service re-checks on write regardless.
		r.Group(func(r chi.Router) {
			r.Use(s.requireSuperAdmin)
			r.Get("/superadmin/skillset", s.getSkillsetAdmin)
			r.Post("/superadmin/skillset", s.postSkillset)
		})
	})

	s.oauthRoutes(r) // gated: no-op when no providers configured
}

// handleRoot serves the public marketing landing page at "/" to everyone —
// logged-out visitors and signed-in users alike. It deliberately does NOT bounce
// signed-in users to /dashboard: "/" is the product's front door (SEO/GEO entry
// for "agent memory") and the header logo points here, so a signed-in user must
// be able to land on it. The page adapts via SignedIn: when set it offers a
// route back to the dashboard instead of the sign-in/register CTAs.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	_, signedIn := s.sessionUserID(r)
	s.render(w, r, views.LandingPage(views.LandingData{
		HasOAuth: len(s.providers) > 0, // mention social login when wired
		SignedIn: signedIn,
	}))
}

// --- session helpers ---

// setSessionUser persists the authenticated user id in the session cookie.
func (s *Server) setSessionUser(w http.ResponseWriter, r *http.Request, userID string) error {
	sess, _ := s.store.Get(r, sessionName)
	sess.Values[sessionUserKey] = userID
	return sess.Save(r, w)
}

// sessionUserID returns the user id stored in the session, if any.
func (s *Server) sessionUserID(r *http.Request) (string, bool) {
	sess, _ := s.store.Get(r, sessionName)
	id, ok := sess.Values[sessionUserKey].(string)
	return id, ok && id != ""
}

// clearSession deletes the session cookie (sign-out).
func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.store.Get(r, sessionName)
	sess.Options.MaxAge = -1
	_ = sess.Save(r, w)
}

// pending2FAOptions is the cookie policy for the pending-2FA session: same
// hardening as the main session but a five-minute lifetime, so an abandoned
// half-login can't linger.
func pending2FAOptions() *sessions.Options {
	return &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   pending2FAMaxAgeSec,
	}
}

// setPending2FA records that userID passed the password check but still owes a
// TOTP code, and resets the wrong-code counter. It replaces (never extends) the
// real session — the caller must not have opened one.
func (s *Server) setPending2FA(w http.ResponseWriter, r *http.Request, userID string) error {
	sess, _ := s.store.Get(r, pending2FAName)
	sess.Values[pending2FAUserKey] = userID
	sess.Values[pending2FAAttemptsKey] = 0
	sess.Options = pending2FAOptions()
	return sess.Save(r, w)
}

// pending2FAUserID returns the id of the user mid-2FA, if the pending cookie is
// present and valid. Absence means the challenge page has nothing to verify and
// must bounce back to /login.
func (s *Server) pending2FAUserID(r *http.Request) (string, bool) {
	sess, _ := s.store.Get(r, pending2FAName)
	id, ok := sess.Values[pending2FAUserKey].(string)
	return id, ok && id != ""
}

// bumpPending2FAAttempts increments and returns the wrong-code count on the
// pending cookie; at maxTOTPLoginAttempts the caller drops the pending state so a
// casual guesser must re-pass the password to continue.
//
// This is best-effort, not a hard limit: the count lives in the client-held
// (signed+sealed) cookie, so a determined attacker who ignores our Set-Cookie —
// or simply re-POSTs /login for a fresh pending state — is not stopped by it.
// That is by design here: this stateless service (see main.go) intentionally
// defers real brute-force throttling to the edge (WAF/gateway), and the password
// login has no in-app throttle either. A server-side per-user limiter would be
// the app-layer fix, at the cost of the stateless property and a lockout-DoS
// tradeoff — a deliberate decision left to the operator.
func (s *Server) bumpPending2FAAttempts(w http.ResponseWriter, r *http.Request) int {
	sess, _ := s.store.Get(r, pending2FAName)
	n, _ := sess.Values[pending2FAAttemptsKey].(int)
	n++
	sess.Values[pending2FAAttemptsKey] = n
	sess.Options = pending2FAOptions()
	_ = sess.Save(r, w)
	return n
}

// clearPending2FA deletes the pending-2FA cookie — on success (a real session
// takes over), on giving up, or when the attempt limit is hit.
func (s *Server) clearPending2FA(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.store.Get(r, pending2FAName)
	sess.Options = pending2FAOptions()
	sess.Options.MaxAge = -1
	_ = sess.Save(r, w)
}

// requireUser is middleware that loads the session user into the request context
// or redirects to the login page. Loading from the DB each request means a
// deleted user is logged out immediately.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.sessionUserID(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		u, err := s.tenants.GetUserByID(r.Context(), id)
		if err != nil {
			s.clearSession(w, r)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireSuperAdmin is middleware that admits only platform superadmins. It runs
// inside requireUser (so the user is already on the context) and returns 404 — not
// 403 — for everyone else, so the superadmin surface never even confirms it exists
// to a non-superadmin. It is the route-level half of a defense-in-depth pair: the
// skillset service still enforces the gate on every write.
func (s *Server) requireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := userFrom(r.Context())
		if !ok || !s.isSuperAdmin(u.Email) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// userFrom returns the authenticated user placed on the context by requireUser.
func userFrom(ctx context.Context) (tenant.User, bool) {
	u, ok := ctx.Value(userCtxKey).(tenant.User)
	return u, ok
}
