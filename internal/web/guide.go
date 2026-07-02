package web

import (
	_ "embed"
	"net/http"
	"strings"
)

// claudeGuide is the agent-facing install guide served at /claude-guide as raw
// Markdown. It is written for Claude (or any coding agent) to self-install the
// kit: ask the user for their workspace token, then run the download +
// `install --global --token` commands. Shipping it as plain Markdown (not a styled
// page) keeps it clean for an agent to fetch and parse.
//
//go:embed claude-guide.md
var claudeGuide string

// guideBaseURLPlaceholder marks where the guide's "sign in at <url>" step should
// carry this server's public origin. It is substituted per request so the link
// points at whatever host the request arrived through (localhost in dev, the real
// domain in production) — the same reasoning as the migration command in skills.go.
const guideBaseURLPlaceholder = "{{BASE_URL}}"

// handleClaudeGuide serves the install guide as raw Markdown at /claude-guide. It
// is public (no auth) and deliberately not a templ/HTML page: an agent curls it
// and reads the commands directly, so HTML chrome would only add noise. The only
// dynamic part is the dashboard URL, filled in from the request.
func (s *Server) handleClaudeGuide(w http.ResponseWriter, r *http.Request) {
	body := strings.ReplaceAll(claudeGuide, guideBaseURLPlaceholder, requestBaseURL(r))
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(body))
}
