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

// LandingData backs the public marketing landing page served at "/" for
// logged-out visitors. The page is otherwise static.
type LandingData struct {
	// HasOAuth reports whether any social login provider is configured, so the
	// hero/CTA copy can mention it; purely cosmetic, the buttons live on /register.
	HasOAuth bool
}

// Brand and canonical-site constants. The marketing brand is "AI Agent Memory"
// (matching the aiagentmemory.dev domain and the primary SEO target); the Go
// package and repo remain "agentsmemory". Canonical/OG URLs use the fixed
// production origin so search engines and AI answer engines index one canonical
// page regardless of how a given request reached the server (localhost, proxy…).
const (
	siteName = "AI Agent Memory"
	siteURL  = "https://aiagentmemory.dev"
	repoURL  = "https://github.com/atvirokodosprendimai/agentsmemory"
)

// --- landing-page content (single source for the page AND its JSON-LD) ---
// These small value types and data funcs keep the landing template logic-free
// and avoid duplicating copy between the visible sections and the structured
// data: the FAQ list, for instance, renders the on-page accordion and the
// schema.org FAQPage from one slice.

// navItem is one in-page anchor in the landing nav.
type navItem struct{ Href, Label string }

// statItem is one figure in the hero stat strip.
type statItem struct{ Value, Label string }

// concept is one node of the memory-palace data model (wing/room/drawer/...).
type concept struct{ Term, Gloss string }

// step is one numbered stage in the "how it works" flow.
type step struct{ Title, Body string }

// feature is one capability card. Tag is a short brass eyebrow (a card-catalog
// category) used instead of a decorative icon, in keeping with the archive theme.
type feature struct{ Tag, Title, Body string }

// plan is one pricing tier. Featured raises one card as the recommended choice.
type plan struct {
	Name, Price, Period, Tagline string
	Points                       []string
	Featured                     bool
}

// faqItem is one question/answer, rendered both as an accordion row and as a
// schema.org Question in the JSON-LD so AI answer engines can cite it.
type faqItem struct{ Q, A string }

// landingNav lists the section anchors shown in the primary nav.
func landingNav() []navItem {
	return []navItem{
		{"#what", "What it is"},
		{"#model", "Data model"},
		{"#features", "Features"},
		{"#pricing", "Pricing"},
		{"#faq", "FAQ"},
	}
}

// landingStats are the at-a-glance figures under the hero.
func landingStats() []statItem {
	return []statItem{
		{"36 / 37", "MCP tools shipped"},
		{"3-way", "hybrid recall: vector · BM25 · closet"},
		{"per-team", "isolated vector store"},
		{"$0", "to start — 10k requests / month"},
	}
}

// landingConcepts is the memory-palace data model, mirrored from the README.
func landingConcepts() []concept {
	return []concept{
		{"Wing", "A project or context namespace — one isolated workspace per team."},
		{"Room", "An aspect within a wing, like backend or decisions."},
		{"Drawer", "One verbatim memory chunk plus rich metadata. Never summarised."},
		{"Closet", "A topic and quote pointer index that boosts ranking — never a gate."},
		{"Hallway", "A within-wing link between entities that co-occur in drawers."},
		{"Tunnel", "A cross-wing link — authored, or auto-derived from a shared topic."},
		{"Knowledge graph", "Temporal subject→predicate→object facts with validity windows."},
	}
}

// landingSteps is the request flow shown in "how it works".
func landingSteps() []step {
	return []step{
		{"Connect over MCP", "Point any MCP client — Claude, your own agent — at POST /mcp with an Authorization: Bearer token."},
		{"Resolve the tenant", "The token becomes a workspace in exactly one place. Every tool reads that tenant off the context and fails closed without it."},
		{"File and recall", "Write verbatim drawers that get embedded and indexed, then recall them with hybrid search across the whole team's memory."},
		{"Stay isolated", "SQLite is the relational source of truth; Qdrant holds per-tenant vectors, rebuildable from it. The transport is stateless, so it scales out."},
	}
}

// landingFeatureList are the capability cards.
func landingFeatureList() []feature {
	return []feature{
		{"Recall", "Hybrid semantic search", "Vector similarity, BM25 lexical match and a closet boost, fused into one ranking — so agents recall by meaning and by exact term."},
		{"Isolation", "Memory that can't leak", "Every workspace gets its own Qdrant collection, named by a hash of the team id. A missing filter can't cross tenants — the data isn't even colocated."},
		{"Skills", "Centralised, versioned skills", "One shared source of truth for prompts and skills. Agents pull the latest with am_load_skill instead of copy-pasting local files."},
		{"Diary", "An append-only agent diary", "A timestamped journal per agent. Sessions thread across time, so the next run reads what the last one learned."},
		{"Knowledge", "Temporal knowledge graph", "Subject→predicate→object facts with validity windows, queryable as-of any point in time. Know what was true then, not just now."},
		{"Mining", "Idempotent mining pipeline", "am_mine turns raw text into chunked, embedded drawers plus a closet index — keyed by source, so re-running finishes rather than duplicates."},
		{"Graph", "A navigable memory graph", "Hallways link co-occurring entities; tunnels bridge wings. Traverse the graph to surface context a flat search would miss."},
		{"Migrate", "Bring your mempalace", "A read-only exporter streams an existing local mempalace into your workspace over /import — re-embedded server-side, graph rebuilt, fully idempotent."},
	}
}

// landingPlans are the pricing tiers, matching the plans catalog.
func landingPlans() []plan {
	return []plan{
		{
			Name: "Personal", Price: "$0", Period: "forever",
			Tagline: "For solo agents and side projects.",
			Points:  []string{"10,000 requests / month", "Unlimited drawers & diary", "Hybrid search + knowledge graph", "Centralised skills"},
		},
		{
			Name: "Enterprise", Price: "$50", Period: "/ month",
			Tagline: "For teams sharing memory across agents.", Featured: true,
			Points: []string{"Everything in Personal", "Multiple workspaces & members", "Role-gated shared skills", "Per-team isolated vector store"},
		},
	}
}

// landingFAQ is the question/answer set, keyworded around agent memory. It feeds
// both the on-page accordion and the schema.org FAQPage in the JSON-LD.
func landingFAQ() []faqItem {
	return []faqItem{
		{
			"What is AI agent memory?",
			"AI agent memory is persistent, long-term storage that lets an AI agent remember context across sessions — past decisions, facts and learnings — instead of starting cold every run. AI Agent Memory provides it as a remote MCP server: agents file verbatim drawers of memory and recall them later with semantic search.",
		},
		{
			"What is an MCP memory server?",
			"An MCP (Model Context Protocol) memory server exposes memory operations — write, search, recall — as tools any MCP-compatible agent can call over HTTP. agentsmemory speaks stateless Streamable HTTP MCP, so Claude and other agents read and write memory with a bearer token.",
		},
		{
			"How do AI agents remember things long-term?",
			"They externalise memory to a store outside the model's context window. agentsmemory embeds each memory with the bge-m3 model and indexes it in Qdrant, then ranks recall with a hybrid of vector similarity, BM25 and a closet boost — so agents retrieve the most relevant past context on demand.",
		},
		{
			"Is my agent's memory isolated from other teams?",
			"Yes. Each workspace gets its own physically separate Qdrant collection, named by a hash of the team id. There is no shared collection to mis-filter, so memory cannot leak across tenants.",
		},
		{
			"Can I migrate an existing memory palace?",
			"Yes. A read-only exporter streams an existing local Python mempalace — drawers, diary, closets, knowledge-graph facts and tunnels — into your workspace over /import. The server re-embeds each memory and rebuilds the graph, and the import is idempotent.",
		},
		{
			"What does agent memory cost to start?",
			"The Personal plan is free with 10,000 requests per month. Teams that share memory across many agents and members use the Enterprise plan at $50 per month.",
		},
	}
}

// faqToggle is the datastar expression a FAQ question runs: open this row if
// closed, close it if already open (an accordion where one is open at a time).
func faqToggle(i int) string {
	n := strconv.Itoa(i)
	return "$_faq = ($_faq === " + n + " ? -1 : " + n + ")"
}

// faqOpen is the datastar predicate "this FAQ row is the open one", used to drive
// the answer's visibility (data-show treats it as a boolean).
func faqOpen(i int) string { return "$_faq === " + strconv.Itoa(i) }

// faqExpanded is the same predicate as a string-valued expression for
// aria-expanded. datastar's data-attr sets a *boolean* attribute to "" when
// truthy (HTML boolean-attribute semantics), but ARIA states must be the literal
// "true"/"false", so the expression must resolve to those strings explicitly.
func faqExpanded(i int) string { return faqOpen(i) + " ? 'true' : 'false'" }

// landingQuickstart is the copy-paste shell snippet shown in the quick-start
// section (and copied verbatim by the Copy button via clipboardExpr).
const landingQuickstart = `go build -o agentsmemory ./cmd/server
./agentsmemory --addr :8080 --db agentsmemory.db
# prints a one-time MCP bearer token to the log`

// clipboardExpr is the datastar click expression that copies the quick-start to
// the clipboard and flashes a "copied" signal. The payload is JSON-encoded so
// quotes/newlines embed safely as a JS string literal.
func clipboardExpr(s string) string {
	return "navigator.clipboard.writeText(" + jsString(s) +
		"); $_copied = true; setTimeout(() => $_copied = false, 1600)"
}

// landingJSONLD builds the schema.org structured data for the landing page: a
// SoftwareApplication describing the product (with both price tiers) and a
// FAQPage built from landingFAQ. Emitting this as JSON-LD is the highest-signal
// way to make the page citable by AI answer engines (GEO) and rich in search.
// Go's json.Marshal HTML-escapes <, > and & by default, so the result is safe
// to drop inside a <script> tag via templ.Raw without a </script> breakout.
func landingJSONLD() string {
	faqs := landingFAQ()
	questions := make([]map[string]any, 0, len(faqs))
	for _, f := range faqs {
		questions = append(questions, map[string]any{
			"@type": "Question",
			"name":  f.Q,
			"acceptedAnswer": map[string]any{
				"@type": "Answer",
				"text":  f.A,
			},
		})
	}

	doc := map[string]any{
		"@context": "https://schema.org",
		"@graph": []map[string]any{
			{
				"@type":               "SoftwareApplication",
				"name":                siteName,
				"alternateName":       "agentsmemory",
				"applicationCategory": "DeveloperApplication",
				"operatingSystem":     "Linux, macOS, Windows",
				"url":                 siteURL + "/",
				"sameAs":              []string{repoURL},
				"description":         "AI Agent Memory is a multi-tenant memory palace for AI agents — long-term agent memory served as a remote MCP server, with hybrid semantic search backed by Ollama and Qdrant.",
				"offers": []map[string]any{
					{"@type": "Offer", "name": "Personal", "price": "0", "priceCurrency": "USD"},
					{"@type": "Offer", "name": "Enterprise", "price": "50", "priceCurrency": "USD"},
				},
			},
			{
				"@type":      "FAQPage",
				"mainEntity": questions,
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "{}" // never expected; a valid-but-empty object keeps the tag well-formed
	}
	return string(b)
}

// landingJSONLDScript returns the complete <script type="application/ld+json">
// element as a string. templ treats the *contents* of a <script> tag as literal
// text and does not evaluate expressions inside it, so the JSON-LD must be
// emitted as a whole tag via templ.Raw from HTML context rather than written as
// a child of a literal <script>. The marshalled JSON is HTML-escaped (no
// </script> can appear), so this is safe to mark raw.
func landingJSONLDScript() string {
	return `<script type="application/ld+json">` + landingJSONLD() + `</script>`
}
