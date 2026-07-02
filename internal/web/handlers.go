package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/a-h/templ"
	"github.com/google/uuid"
	"github.com/starfederation/datastar-go/datastar"
)

// render writes a templ component as an HTML response.
func (s *Server) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = c.Render(r.Context(), w)
}

// --- auth pages (classic full-page POST forms) ---

// getLogin renders the sign-in page.
func (s *Server) getLogin(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sessionUserID(r); ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	s.render(w, r, views.LoginPage(views.AuthData{OAuthProviders: s.providers}))
}

// postLogin verifies credentials and starts a session.
func (s *Server) postLogin(w http.ResponseWriter, r *http.Request) {
	email := r.PostFormValue("email")
	u, err := s.tenants.Authenticate(r.Context(), email, r.PostFormValue("password"))
	if err != nil {
		// Same message whether the email is unknown or the password is wrong.
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, r, views.LoginPage(views.AuthData{
			Error: "Invalid email or password.", Email: email, OAuthProviders: s.providers,
		}))
		return
	}
	// When 2FA is on, the password is only the first factor: withhold the real
	// session, stash a short-lived pending marker, and send the user to the code
	// step. The account is not signed in until /login/totp verifies the code.
	if u.TOTPEnabled {
		if err := s.setPending2FA(w, r, u.ID); err != nil {
			http.Error(w, "session error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/login/totp", http.StatusSeeOther)
		return
	}
	if err := s.setSessionUser(w, r, u.ID); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// getRegister renders the account-creation page.
func (s *Server) getRegister(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sessionUserID(r); ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	s.render(w, r, views.RegisterPage(views.AuthData{OAuthProviders: s.providers}))
}

// postRegister creates a new account, signs the user in, and lands them on the
// dashboard where they can create their first free project.
func (s *Server) postRegister(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")

	fail := func(msg string) {
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, views.RegisterPage(views.AuthData{Error: msg, Email: email}))
	}
	if email == "" || !strings.Contains(email, "@") {
		fail("Enter a valid email address.")
		return
	}
	if len(password) < 8 {
		fail("Password must be at least 8 characters.")
		return
	}

	u, err := s.tenants.CreateUserWithPassword(r.Context(), email, password, "")
	if errors.Is(err, tenant.ErrEmailTaken) {
		fail("That email is already registered.")
		return
	}
	if err != nil {
		fail("Could not create your account. Please try again.")
		return
	}
	if err := s.setSessionUser(w, r, u.ID); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// postLogout ends the session.
func (s *Server) postLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- dashboard ---

// getDashboard lists the user's projects with their plan and monthly usage.
func (s *Server) getDashboard(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	projects, err := s.projectsForUser(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "could not load projects", http.StatusInternalServerError)
		return
	}
	s.render(w, r, views.DashboardPage(views.DashboardData{
		UserEmail: u.Email, Projects: projects, OAuthProviders: s.providers,
		IsSuperAdmin: s.isSuperAdmin(u.Email),
	}))
}

// createProjectSignals is the datastar signal payload for the create action.
type createProjectSignals struct {
	ProjectName string `json:"projectName"`
}

// postCreateProject provisions a new free project for the user and streams back
// datastar fragments: the one-time API key (in #flash) and the refreshed
// project list (#projects). It also clears the input signal.
func (s *Server) postCreateProject(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())

	var sig createProjectSignals
	_ = datastar.ReadSignals(r, &sig) // empty name handled below
	name := strings.TrimSpace(sig.ProjectName)

	sse := datastar.NewSSE(w, r)
	if name == "" {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
			Kind: "error", Message: "Please enter a project name.",
		}))
		return
	}

	// "Free project" = a personal-kind workspace on the Free plan (10k req/mo).
	slug := slugify(name) + "-" + uuid.NewString()[:6]
	_, cred, err := s.tenants.CreateWorkspaceForUser(r.Context(), u.ID, name, slug, "personal", "plan_personal")
	if err != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
			Kind: "error", Message: "Could not create the project. Please try again.",
		}))
		return
	}

	projects, err := s.projectsForUser(r.Context(), u.ID)
	if err != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
			Kind: "error", Message: "Project created, but the list failed to refresh — reload the page.",
		}))
		return
	}

	// The credential is shown once: client_key is the OAuth Client ID, the token is
	// the secret (also usable directly as a Bearer). The flash renders the one-paste
	// install command with the token, so the user can wire Claude to the new
	// workspace before the secret scrolls away.
	_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
		Kind:      "success",
		Message:   "Project \"" + name + "\" created.",
		ClientKey: cred.ClientKey,
		Token:     cred.Secret,
	}))
	_ = sse.PatchElementTempl(views.ProjectsList(projects))
	_ = sse.MarshalAndPatchSignals(map[string]any{"projectName": ""})
}

// projectsForUser builds the display models for a user's projects: plan name and
// the current month's usage (used / cap and a 0..100 bar percentage).
func (s *Server) projectsForUser(ctx context.Context, userID string) ([]views.ProjectVM, error) {
	teams, err := s.tenants.ListWorkspacesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]views.ProjectVM, 0, len(teams))
	for _, t := range teams {
		planName := "—"
		planCode := ""
		if p, err := s.tenants.PlanForTeam(ctx, t.ID); err == nil {
			planName = p.Name
			planCode = p.Code
		}
		st, err := s.usage.Snapshot(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		pct := 0
		if st.Cap > 0 {
			pct = st.Used * 100 / st.Cap
			if pct > 100 {
				pct = 100
			}
		}
		// Keys are per-member: this card shows the signed-in member's OWN key, so
		// every member may reveal/rotate it (revealing your own credential is no
		// escalation). CanReveal is therefore always true here — the viewer is, by
		// construction, a member of every team ListWorkspacesForUser returned. Role
		// still gates the billing controls below. A lookup error fails closed.
		role, _ := s.tenants.MembershipRole(ctx, userID, t.ID)
		// Upgrade is offered to an admin of a free-tier (or planless) workspace,
		// and only when Stripe is configured — otherwise the button would lead
		// nowhere. A non-free plan already has its tier; no upgrade prompt.
		onFree := planCode == "personal" || planCode == ""
		// A comped/unlimited plan is granted by an operator (the set-plan CLI), not
		// bought: there is no provider subscription behind it, so it offers neither an
		// upgrade (it is already uncapped) nor a Manage-portal path (there is no
		// customer to open a portal for). Excluding it keeps a broken "Manage
		// subscription" button off the card for an operator-comped workspace.
		isComped := planCode == "unlimited"
		isAdmin := role == tenant.RoleAdmin
		// Upgrade is offered on a free plan; Manage on a paid one. Both require an admin
		// and configured billing, and they are mutually exclusive by the onFree split.
		canUpgrade := s.billing.Enabled() && isAdmin && onFree
		canManage := s.billing.Enabled() && isAdmin && !onFree && !isComped
		out = append(out, views.ProjectVM{
			TeamID: t.ID, Name: t.Name, Slug: t.Slug, PlanName: planName, Endpoint: "/mcp",
			Used: st.Used, Cap: st.Cap, Pct: pct,
			CanReveal: true, CanUpgrade: canUpgrade, CanManage: canManage,
		})
	}
	return out, nil
}

// slugify turns a project name into a url-safe slug (lowercase, alphanumerics,
// single dashes). A random suffix is added by the caller for uniqueness.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "project"
	}
	return out
}
