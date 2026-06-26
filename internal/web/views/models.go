// Package views holds the templ templates and their view models for the
// dashboard. View models are plain, display-ready structs so the templates stay
// logic-free and the handlers do the shaping. Keeping them here (not in the web
// package) avoids an import cycle: web imports views, never the reverse.
package views

import "strconv"

// capLabel renders a project's monthly cap, showing ∞ for an unlimited plan.
func capLabel(p ProjectVM) string {
	if p.Cap <= 0 {
		return "∞"
	}
	return strconv.Itoa(p.Cap)
}

// barClass adds a warning tint to the usage bar once a project is near its cap.
func barClass(p ProjectVM) string {
	if p.Pct >= 80 {
		return "bar warn"
	}
	return "bar"
}

// ProjectVM is one project (workspace) as shown on the dashboard.
type ProjectVM struct {
	Name     string
	Slug     string
	PlanName string
	Endpoint string // MCP endpoint hint, e.g. /mcp
	Used     int    // metered requests this month
	Cap      int    // monthly cap (0 = unlimited)
	Pct      int    // Used/Cap as 0..100 for the usage bar
	// TokenOnce is the freshly minted API key, set ONLY on the card just
	// created so it can be revealed once; empty on every other render.
	TokenOnce string
}

// FlashVM is a transient banner (success or error) shown above the project list.
type FlashVM struct {
	Kind    string // "success" | "error" | "" (none)
	Message string
	Token   string // one-time API key to reveal, when Kind == "success"
}

// DashboardData is everything the dashboard page needs.
type DashboardData struct {
	UserEmail string
	Projects  []ProjectVM
	Flash     FlashVM
	// OAuthProviders lists configured social providers (e.g. "google") so the
	// login page can offer them; empty until keys are configured.
	OAuthProviders []string
}

// AuthData backs the login and register pages.
type AuthData struct {
	Error          string
	Email          string // preserved on error so the user need not retype
	OAuthProviders []string
}
