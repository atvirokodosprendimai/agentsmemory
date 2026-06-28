// Package views holds the templ templates and their view models for the
// dashboard. View models are plain, display-ready structs so the templates stay
// logic-free and the handlers do the shaping. Keeping them here (not in the web
// package) avoids an import cycle: web imports views, never the reverse.
package views

import (
	"encoding/json"
	"strconv"
)

// editExpr is the datastar expression a skill row's Edit button runs: it seeds
// the editor's name/description signals from the row, then fetches the body into
// $skillContent via the skill-body endpoint. The strings are JSON-encoded so any
// quote or backslash in a skill name embeds safely in the JS-like expression
// (and templ then HTML-escapes the whole attribute).
func editExpr(teamID string, sk SkillVM) string {
	return "$skillName = " + jsString(sk.Name) +
		"; $skillDescription = " + jsString(sk.Description) +
		"; @get(" + jsString("/projects/"+teamID+"/skill-body") + ")"
}

// jsString renders a Go string as a JSON string literal — safe to drop into a
// datastar expression as a quoted value.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// versionBadge renders a skill's version as a compact "v3" label.
func versionBadge(sk SkillVM) string { return "v" + strconv.Itoa(sk.Version) }

// skillMeta is the muted one-line provenance under a skill: when it was last
// saved (date only — the RFC3339 time is noise in a list). A skill with no
// timestamp (shouldn't happen post-save) reads as "New".
func skillMeta(sk SkillVM) string {
	when := sk.UpdatedAt
	if len(when) >= 10 {
		when = when[:10] // YYYY-MM-DD prefix of the RFC3339 stamp
	}
	if when == "" {
		return "New"
	}
	return "Updated " + when
}

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
	TeamID   string // workspace id; used to link to the project's skills page
	Name     string
	Slug     string
	PlanName string
	Endpoint string // MCP endpoint hint, e.g. /mcp
	Used     int    // metered requests this month
	Cap      int    // monthly cap (0 = unlimited)
	Pct      int    // Used/Cap as 0..100 for the usage bar
	// CanReveal is true when the viewer is an admin of this workspace, so the
	// card offers a Reveal control for the API key. Revealing a key grants full
	// access at the key owner's role, so it is restricted to admins (a member
	// could otherwise lift the admin's bearer and escalate).
	CanReveal bool
}

// KeyVM backs the API-key block on a project card. The block is a datastar morph
// target (id "key-<TeamID>") the reveal endpoint patches between masked and
// revealed states. Secret is populated only in the revealed state and is never
// part of the initial page render.
type KeyVM struct {
	TeamID        string
	CanReveal     bool // viewer is an admin: may reveal and rotate
	Revealed      bool
	Secret        string
	Rotated       bool   // the revealed secret is freshly rotated (old key revoked)
	ConfirmRotate bool   // show the destructive-rotate confirmation prompt
	Error         string // shown when a reveal can't be honored (e.g. a legacy key)
}

// copyExpr is the datastar expression the Copy button runs: write the revealed
// secret to the clipboard. The secret is JSON-encoded for safe embedding; it is
// already visible in the revealed block, so this exposes nothing new.
func copyExpr(secret string) string {
	return "navigator.clipboard.writeText(" + jsString(secret) + ")"
}

// MigrateCommand is the copy-paste shell command that streams a local mempalace
// into this project over /import. The token is a placeholder, never the real key:
// the secret is revealed only in the API-key block, so it never lands in page
// source or the clipboard from here. serverBase is this server's public origin.
func MigrateCommand(serverBase string) string {
	return "python mempalace_export.py --push \\\n" +
		"  --server " + serverBase + " \\\n" +
		"  --token YOUR_PROJECT_API_KEY"
}

// SkillVM is one centralised skill as shown on the project page — metadata only
// (no body), matching skill.Summary. The body is fetched on demand into the
// editor when a writer clicks Edit, so the list stays light.
type SkillVM struct {
	Name        string
	Description string
	Version     int    // bumped on every save; rendered as a "v3" badge
	UpdatedBy   string // user id of the last author
	UpdatedAt   string // RFC3339 timestamp of the last save
}

// ProjectDetailData backs the per-project skills page. CanWrite is the resolved
// role gate: the editor and per-skill Edit controls render only when the
// signed-in user is a writer or admin in this workspace. Members see a
// read-only list. The template never decides authority on its own — it trusts
// CanWrite, which the handler computes from the membership role.
type ProjectDetailData struct {
	UserEmail string
	Project   ProjectVM
	Skills    []SkillVM
	CanWrite  bool
	Flash     FlashVM
	// ServerBase is this server's public origin (scheme + host), used to render the
	// ready-to-run mempalace migration command with the correct /import endpoint.
	ServerBase string
}

// FlashVM is a transient banner (success or error) shown above the project list.
type FlashVM struct {
	Kind      string // "success" | "error" | "" (none)
	Message   string
	ClientKey string // one-time OAuth Client ID to reveal, when Kind == "success"
	Token     string // one-time API key / OAuth secret to reveal
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
