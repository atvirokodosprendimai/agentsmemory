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
)

// ctxKey is an unexported context key type for the authenticated user.
type ctxKey struct{}

var userCtxKey = ctxKey{}

// Server holds the dashboard's dependencies.
type Server struct {
	tenants   *tenant.Repo
	usage     *usage.Service
	skills    *skill.Service    // centralised-skill use-cases, shared with the MCP server
	skillsets *skillset.Service // the global wakeup-playbook use-cases (am_skillset)
	shares    *share.Service    // cross-workspace wing-share handshake (consent flow)
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
// SUPERADMIN_EMAILS allowlist that gates that editor.
func New(tenants *tenant.Repo, usageSvc *usage.Service, skills *skill.Service, skillsets *skillset.Service, shares *share.Service, superAdmins []string, sessionKey []byte) *Server {
	store := sessions.NewCookieStore(sessionKey)
	store.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 60 * 60, // one week
	}
	s := &Server{tenants: tenants, usage: usageSvc, skills: skills, skillsets: skillsets, shares: shares, store: store, superAdmins: superAdminSet(superAdmins)}
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
	r.Get("/register", s.getRegister)
	r.Post("/register", s.postRegister)
	r.Get("/login", s.getLogin)
	r.Post("/login", s.postLogin)
	r.Post("/logout", s.postLogout)

	// Authenticated area.
	r.Group(func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/dashboard", s.getDashboard)
		r.Post("/projects", s.postCreateProject)
		// Project-scoped skill management. {teamID} is membership-checked in each
		// handler (see Server.membership) — a logged-in user can only reach a
		// project they belong to.
		r.Get("/projects/{teamID}", s.getProject)
		r.Get("/projects/{teamID}/key", s.getProjectKey)
		r.Post("/projects/{teamID}/key/rotate", s.postRotateKey)
		r.Post("/projects/{teamID}/skills", s.postSkill)
		r.Get("/projects/{teamID}/skill-body", s.getSkillBody)

		// Cross-workspace wing sharing. POST /share files a pending request from
		// this (source) workspace; accept/decline resolve a request addressed to
		// this (destination) workspace. All three membership-check {teamID}; the
		// share service layers the writer/admin gates and binds each request to its
		// destination, so the slug box can never silently copy across tenants.
		r.Post("/projects/{teamID}/share", s.postShareRequest)
		r.Post("/projects/{teamID}/share/{reqID}/accept", s.postShareAccept)
		r.Post("/projects/{teamID}/share/{reqID}/decline", s.postShareDecline)

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

// handleRoot sends signed-in users straight to the dashboard and serves the
// public marketing landing page to everyone else. The landing page is the
// product's front door (SEO/GEO entry for "agent memory"), so logged-out
// visitors get content here rather than an immediate redirect to /login.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sessionUserID(r); ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	s.render(w, r, views.LandingPage(views.LandingData{
		HasOAuth: len(s.providers) > 0, // mention social login when wired
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
