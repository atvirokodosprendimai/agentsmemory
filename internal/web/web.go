// Package web is the dashboard: registration, login, and project management,
// rendered with templ and made interactive with datastar. It is a separate
// surface from the MCP server (which agents talk to); humans use this to create
// projects and read their API keys and usage. Auth is local email+password by
// default, with goth wired for social logins when provider keys are configured.
package web

import (
	"context"
	"net/http"

	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
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
	skills    *skill.Service // centralised-skill use-cases, shared with the MCP server
	store     sessions.Store
	providers []string // configured OAuth providers; empty until keys are set
}

// New builds the dashboard server. sessionKey signs/encrypts the session cookie;
// it must be stable across restarts (else sessions are invalidated on deploy).
// skills is the same service the MCP server exposes as list_skills/update_skill,
// reused here so the web editor and the agent tools share one code path.
func New(tenants *tenant.Repo, usageSvc *usage.Service, skills *skill.Service, sessionKey []byte) *Server {
	store := sessions.NewCookieStore(sessionKey)
	store.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 60 * 60, // one week
	}
	s := &Server{tenants: tenants, usage: usageSvc, skills: skills, store: store}
	s.providers = registerOAuth(store) // gated: returns nil when no keys set
	return s
}

// Routes mounts the dashboard routes onto r.
func (s *Server) Routes(r chi.Router) {
	// Embedded static assets (the stylesheet) served read-only.
	r.Handle("/static/*", http.StripPrefix("/", http.FileServer(http.FS(staticFS))))

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

// userFrom returns the authenticated user placed on the context by requireUser.
func userFrom(ctx context.Context) (tenant.User, bool) {
	u, ok := ctx.Value(userCtxKey).(tenant.User)
	return u, ok
}
