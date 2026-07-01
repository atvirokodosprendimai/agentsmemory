package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/facebook"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
)

// registerOAuth wires goth social providers from environment variables and
// returns the names of those configured (empty when none). Local email+password
// stays the default; social login is purely additive — a provider only appears
// when its KEY/SECRET are present, so "later add google/facebook/github" is just
// setting env vars. Callback base is PUBLIC_BASE_URL (default localhost).
func registerOAuth(store sessions.Store) []string {
	gothic.Store = store

	base := os.Getenv("PUBLIC_BASE_URL")
	if base == "" {
		base = "http://localhost:8080"
	}

	var enabled []string
	var providers []goth.Provider

	if id := os.Getenv("GOOGLE_KEY"); id != "" {
		providers = append(providers, google.New(id, os.Getenv("GOOGLE_SECRET"), base+"/auth/google/callback", "email", "profile"))
		enabled = append(enabled, "google")
	}
	if id := os.Getenv("GITHUB_KEY"); id != "" {
		providers = append(providers, github.New(id, os.Getenv("GITHUB_SECRET"), base+"/auth/github/callback", "user:email"))
		enabled = append(enabled, "github")
	}
	if id := os.Getenv("FACEBOOK_KEY"); id != "" {
		providers = append(providers, facebook.New(id, os.Getenv("FACEBOOK_SECRET"), base+"/auth/facebook/callback", "email"))
		enabled = append(enabled, "facebook")
	}
	if len(providers) > 0 {
		goth.UseProviders(providers...)
	}
	return enabled
}

// oauthRoutes mounts the social login start/callback routes, but only when at
// least one provider is configured.
func (s *Server) oauthRoutes(r chi.Router) {
	if len(s.providers) == 0 {
		return
	}
	r.Get("/auth/{provider}", s.oauthStart)
	r.Get("/auth/{provider}/callback", s.oauthCallback)
}

// withProvider copies the chi {provider} URL segment into the "provider" query
// parameter, which is where gothic looks to choose the provider.
func withProvider(r *http.Request) *http.Request {
	q := r.URL.Query()
	q.Set("provider", chi.URLParam(r, "provider"))
	r.URL.RawQuery = q.Encode()
	return r
}

// oauthStart begins (or, if already authenticated, completes) the OAuth flow.
func (s *Server) oauthStart(w http.ResponseWriter, r *http.Request) {
	r = withProvider(r)
	if gu, err := gothic.CompleteUserAuth(w, r); err == nil {
		s.finishOAuth(w, r, gu)
		return
	}
	gothic.BeginAuthHandler(w, r)
}

// oauthCallback completes the provider redirect and signs the user in.
func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	r = withProvider(r)
	gu, err := gothic.CompleteUserAuth(w, r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.finishOAuth(w, r, gu)
}

// finishOAuth upserts the local user for the provider's email and opens a
// session. An email-less provider account cannot be linked, so it is rejected.
func (s *Server) finishOAuth(w http.ResponseWriter, r *http.Request, gu goth.User) {
	email := gu.Email
	// GitHub's OAuth /user endpoint (which goth reads) returns the account's
	// *public* profile email — often blank, or a different alias than the address
	// the user actually signs in with elsewhere. goth only falls back to
	// /user/emails (the primary *verified* address) when that public email is
	// empty, so a user who has set a public email gets linked by the wrong
	// address. Resolve the primary verified email ourselves and prefer it, so
	// account linking keys on the identity the user really owns. On any failure we
	// fall back to goth's email rather than break the sign-in.
	if gu.Provider == "github" {
		if primary := githubPrimaryEmail(r.Context(), gu.AccessToken); primary != "" {
			email = primary
		}
	}
	if email == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	name := gu.Name
	if name == "" {
		name = gu.NickName
	}
	u, err := s.tenants.UpsertOAuthUser(r.Context(), email, name)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = s.setSessionUser(w, r, u.ID)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// githubPrimaryEmail resolves the account's primary, verified email via the
// GitHub /user/emails API using the OAuth access token. It exists because
// GitHub's profile endpoint only exposes the (optional) public email, while the
// address a user is known by is their primary verified one — see finishOAuth.
// The "user:email" scope requested in registerOAuth is what authorizes this
// call. It returns "" on any error (network, non-200, no primary+verified entry)
// so the caller can fall back to goth's email rather than fail the login; the
// request is bounded by a short timeout to keep the sign-in path responsive.
func githubPrimaryEmail(ctx context.Context, accessToken string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// per_page=100 (GitHub's max) fetches every address in one page: the default
	// page size is 30, and the primary could otherwise sit on a later page and be
	// missed. No realistic account has >100 emails, so a pagination loop is
	// unnecessary.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails?per_page=100", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	// GitHub returns every address on the account; the one we want is flagged both
	// primary and verified. Verified matters for security: an unverified address
	// must never key account linking.
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return ""
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email
		}
	}
	return ""
}
