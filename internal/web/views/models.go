// Package views holds the templ templates and their view models for the
// dashboard. View models are plain, display-ready structs so the templates stay
// logic-free and the handlers do the shaping. Keeping them here (not in the web
// package) avoids an import cycle: web imports views, never the reverse.
package views

import (
	"encoding/json"
	"strconv"
	"strings"
)

// AssetVersion is a cache-busting token — a short content hash of the embedded
// stylesheet — appended to static asset URLs as ?v=<hash>. The web package sets
// it at startup (it owns the embedded FS); it is exported so web can write it
// without a views→web import cycle. When empty, staticURL returns the bare path.
var AssetVersion string

// staticURL appends the asset version to a static path so a redeploy with changed
// CSS busts browser/CDN caches (the fix for a deploy that rendered "without
// CSS"). With no version set it returns the path unchanged.
func staticURL(path string) string {
	if AssetVersion == "" {
		return path
	}
	return path + "?v=" + AssetVersion
}

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
	// CanUpgrade is true when the viewer may upgrade this workspace to Pro — an
	// admin of a workspace on the free plan, with Stripe billing configured. It
	// gates the UpgradeCard; the upgrade handler re-checks the role server-side.
	CanUpgrade bool
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

// installScriptURL is the raw GitHub URL of the kit's curl|bash bootstrap. It is a
// fixed repo path (not derived from the request), so the install command is the
// same wherever the dashboard runs.
const installScriptURL = "https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh"

// InstallCommand is the one-paste shell command that installs the whole
// aiagentmemory Claude Code kit globally with this workspace's token. The
// bootstrap downloads the binary and runs `install --global`, wiring the slash
// commands, Stop hook, auto-loaded CLAUDE.md, and the MCP in one step — which is
// why the dashboard offers only this, not a bare `claude mcp add` that would set
// up the MCP alone.
//
// The token rides in the AGENTSMEMORY_TOKEN env var (the binary's --token flag
// reads it there) rather than as a visible positional arg; --global pins the
// target so the install never prompts. The token is double-quoted so it survives
// the odd non-alphanumeric character. It is already visible in the surrounding
// revealed block, so embedding it here exposes nothing new. Line continuations
// match the other copy commands' multi-line style.
func InstallCommand(token string) string {
	return "curl -fsSL " + installScriptURL + " \\\n" +
		"  | AGENTSMEMORY_TOKEN=\"" + token + "\" bash -s -- --global"
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
	// Share backs the wing-share card (push a wing to another workspace) and the
	// incoming-requests inbox. Its flags decide which halves render; the handler
	// computes them from the membership role, the template never decides authority.
	Share ShareData
	// Merge backs the wing-merge card (fold duplicate wings together as a
	// background job). Rendered only when the viewer manages the workspace.
	Merge MergeData
}

// MergeData backs the "Merge wings" section: auto-detected duplicate pairs, the
// generic from/to picker, and the recent background jobs. CanManage gates the
// whole section (writer/admin); the handler computes it from the role.
type MergeData struct {
	TeamID     string
	CanManage  bool         // viewer is writer/admin: may merge wings
	Wings      []string     // all wing names — source <select> + target datalist
	Duplicates []MergeDupVM // detected wing_X / X collisions
	Jobs       []MergeJobVM // recent merge jobs, newest first
	Active     bool         // a job is queued/running — drives the status poller
}

// MergeDupVM is one detected duplicate: Source (the wing_X) folds into Target (X).
type MergeDupVM struct {
	Source        string
	Target        string
	SourceDrawers int
	TargetDrawers int
}

// MergeJobVM is one merge job as shown in the recent-jobs panel.
type MergeJobVM struct {
	ID      string
	Sources string // source wings joined for display
	Target  string
	Status  string // queued|running|done|failed
	Summary string // counts on done, the error on failed, else empty
	When    string // created date (YYYY-MM-DD)
}

// mergeDupLabel renders a duplicate pair as a one-line description with weights,
// e.g. "wing_research (5) -> research (3)".
func mergeDupLabel(d MergeDupVM) string {
	return d.Source + " (" + strconv.Itoa(d.SourceDrawers) + ") → " + d.Target + " (" + strconv.Itoa(d.TargetDrawers) + ")"
}

// mergeJobStatusClass maps a job status to its badge CSS modifier.
func mergeJobStatusClass(status string) string {
	switch status {
	case "done":
		return "badge ok"
	case "failed":
		return "badge danger"
	case "running":
		return "badge running"
	default:
		return "badge"
	}
}

// ShareData backs the "Share memory" section on the project page. The same struct
// serves both halves: the outgoing push form (gated by CanShare) and the incoming
// request inbox (gated by IsAdmin). TeamID is this workspace — the source for an
// outgoing share, the destination for incoming requests.
type ShareData struct {
	TeamID   string
	CanShare bool          // viewer is writer/admin: may push a wing out
	IsAdmin  bool          // viewer is admin: may accept/decline incoming requests
	Wings    []ShareWingVM // this workspace's wings, offered in the push picker
	Incoming []ShareReqVM  // pending requests addressed to this workspace
}

// ShareWingVM is one wing offered in the push picker, with its size so the user
// can tell wings apart at a glance.
type ShareWingVM struct {
	Name    string
	Drawers int
	Rooms   int
}

// ShareReqVM is one pending incoming request as shown to the destination admin:
// who (FromName/Requester) wants to copy which wing here.
type ShareReqVM struct {
	ID        string
	Wing      string
	FromName  string // source workspace display name
	FromSlug  string // source workspace slug
	Requester string // email of the user who filed the request
	CreatedAt string // RFC3339; rendered date-only
}

// shareWingOption renders a wing as a <select> option label, e.g.
// "research — 124 drawers · 8 rooms", so a chooser sees each wing's weight.
func shareWingOption(w ShareWingVM) string {
	return w.Name + " — " + strconv.Itoa(w.Drawers) + " drawers · " + strconv.Itoa(w.Rooms) + " rooms"
}

// shareReqWhen renders a request's filing time as a YYYY-MM-DD date (the RFC3339
// time is noise in a list); empty stays empty.
func shareReqWhen(r ShareReqVM) string {
	if len(r.CreatedAt) >= 10 {
		return r.CreatedAt[:10]
	}
	return r.CreatedAt
}

// shareReqFrom names the source of an incoming request — its workspace name and
// slug — with a neutral fallback when the source team could not be resolved (e.g.
// deleted between the request and the admin reading the inbox).
func shareReqFrom(r ShareReqVM) string {
	if r.FromName == "" {
		return "another workspace"
	}
	if r.FromSlug == "" {
		return r.FromName
	}
	return r.FromName + " (" + r.FromSlug + ")"
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
	// IsSuperAdmin reveals the platform link to the global-skillset editor. It is
	// presentation only — the route itself is gated server-side — but it keeps the
	// one cross-tenant control out of every other user's sight.
	IsSuperAdmin bool
}

// SkillsetAdminData backs the platform-superadmin editor for the global am_skillset
// wakeup playbook. There is no CanWrite flag: only a superadmin reaches the page
// (the route is gated), so being here IS the authority.
type SkillsetAdminData struct {
	UserEmail string
	Content   string // the current playbook body (empty before the first edit/seed)
	Version   int    // 0 when unset, else the stored version
	UpdatedBy string // user id of the last editor (provenance)
	Flash     FlashVM
}

// skillsetEditorSignals is the data-signals payload that seeds the editor: the
// current playbook in the bound signal, plus a frontend-only (_ prefixed, so it is
// never sent to the server) copy of the original, so Revert and the
// unsaved-changes hint work without a round-trip.
func skillsetEditorSignals(content string) string {
	return "{skillsetContent: " + jsString(content) + ", _skillsetOriginal: " + jsString(content) + "}"
}

// skillsetVersionLabel renders the playbook version as "v3", or "unset" before the
// first edit/seed.
func skillsetVersionLabel(d SkillsetAdminData) string {
	if d.Version <= 0 {
		return "unset"
	}
	return "v" + strconv.Itoa(d.Version)
}

// AuthData backs the login and register pages.
type AuthData struct {
	Error          string
	Email          string // preserved on error so the user need not retype
	OAuthProviders []string
}

// TOTPChallengeData backs the login second-factor page. It carries only an error
// message: the page is stateless otherwise (the pending user lives in a cookie,
// never in the markup), so nothing about the account leaks into the HTML.
type TOTPChallengeData struct {
	Error string
}

// AccountData backs the account/security page. It holds the signed-in identity
// and the two interactive cards: two-factor (TOTP) and passkeys.
type AccountData struct {
	UserEmail string
	TwoFactor TwoFactorVM
	Passkeys  PasskeysVM
}

// PasskeyVM is one registered passkey as shown on the account page — metadata
// only. ID is our row id (the delete handle), never the WebAuthn credential id.
type PasskeyVM struct {
	ID       string
	Name     string
	Added    string // YYYY-MM-DD
	LastUsed string // YYYY-MM-DD, or "" when never used to sign in
}

// PasskeysVM drives the passkeys card (a datastar morph target, id "passkeys").
// It lists the user's registered passkeys and carries an optional inline error.
// It is deliberately its own view type (not the domain's CredentialInfo) so the
// views package stays free of the passkey/webauthn dependency.
type PasskeysVM struct {
	Passkeys []PasskeyVM
	Error    string
}

// passkeyCountLabel renders the "On" badge count, e.g. "1 passkey" / "3 passkeys".
func passkeyCountLabel(d PasskeysVM) string {
	if len(d.Passkeys) == 1 {
		return "1 passkey"
	}
	return strconv.Itoa(len(d.Passkeys)) + " passkeys"
}

// passkeyMeta renders a passkey's provenance line: when it was added and, if ever
// used, when it was last used to sign in.
func passkeyMeta(p PasskeyVM) string {
	meta := "Added " + p.Added
	if p.LastUsed != "" {
		return meta + " · last used " + p.LastUsed
	}
	return meta + " · never used"
}

// TwoFactorVM drives the two-factor card, which is a single datastar morph target
// (id "twofactor") the setup/enable/disable handlers swap between three states:
//
//   - off        — Enabled=false, Setup=false: an invitation to turn 2FA on.
//   - enrolling  — Setup=true: the QR + secret + code field to confirm a new secret.
//   - on         — Enabled=true: confirmation, plus the code-gated disable control.
//
// RecoveryCodes is non-empty only in the one render right after enabling, where
// the codes are shown exactly once. QRDataURI/Secret are set only while enrolling.
type TwoFactorVM struct {
	Enabled       bool
	Setup         bool
	QRDataURI     string   // data:image/png;base64,… QR of the otpauth URL (enrolling)
	Secret        string   // base32 secret for manual entry (enrolling)
	RecoveryCodes []string // shown once, immediately after enabling
	Error         string   // inline message; rendered into the #totp-error slot
}

// recoveryCodesText joins recovery codes one-per-line for the "copy all" button
// and the download hint — the form a user pastes into a password manager.
func recoveryCodesText(codes []string) string {
	return strings.Join(codes, "\n")
}

// LandingData backs the public marketing landing page served at "/". It is
// shown to everyone — logged-out visitors and signed-in users alike (the latter
// reach it via the header logo), so it carries just enough auth state to swap
// the auth-specific calls to action.
type LandingData struct {
	// HasOAuth reports whether any social login provider is configured, so the
	// hero/CTA copy can mention it; purely cosmetic, the buttons live on /register.
	HasOAuth bool

	// SignedIn is true when the visitor already has a session. When set, the nav
	// and hero drop the "Sign in / Get started / Start free" prompts (meaningless
	// to a logged-in user) in favour of a single route back to the dashboard.
	SignedIn bool
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

// costDriver is one honest reason the service is not free at scale, shown under
// the pricing plans. Tag is a short brass mono kicker (matching the feature/
// install-dep card catalog), so the cards reuse the .lp-model grid with no new
// CSS. The copy answers the "why should I pay?" objection at the point it arises.
type costDriver struct{ Tag, Title, Body string }

// faqItem is one question/answer, rendered both as an accordion row and as a
// schema.org Question in the JSON-LD so AI answer engines can cite it.
type faqItem struct{ Q, A string }

// landingNav lists the section anchors shown in the primary nav.
func landingNav() []navItem {
	return []navItem{
		{"#what", "What it is"},
		{"#model", "Data model"},
		{"#features", "Features"},
		{"#install", "Install"},
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
		{"€0", "to start — 10k requests / month"},
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
		{"Export", "Own and export your data", "Download everything a workspace holds as one self-contained SQLite file — scoped to your tenant, secrets redacted. Your BDAR/GDPR right of access and data portability, in one click."},
	}
}

// landingPlans are the pricing tiers, matching the plans catalog (Free + Pro).
// Pro is one tier sold two ways — €50 / month or €500 / year (two months free) —
// so the annual option rides as a point under the headline monthly price.
func landingPlans() []plan {
	return []plan{
		{
			Name: "Free", Price: "€0", Period: "forever",
			Tagline: "For solo agents and side projects.",
			Points:  []string{"10,000 requests / month", "Unlimited drawers & diary", "Hybrid search + knowledge graph", "Centralised skills"},
		},
		{
			Name: "Pro", Price: "€50", Period: "/ month",
			Tagline: "For teams running agents in production.", Featured: true,
			Points: []string{"or €500 / year — 2 months free", "1,000,000 requests / month", "Everything in Free", "Per-team isolated vector store"},
		},
	}
}

// landingCosts explains why the Pro plan is priced rather than free — the three
// real, recurring costs of hybrid semantic recall. They are specific to this
// stack (Ollama bge-m3 embeddings, per-tenant Qdrant vectors) rather than vague
// "infra costs", so the argument is concrete: the compute and the electricity
// behind every write and every recall are what a subscription pays for.
func landingCosts() []costDriver {
	return []costDriver{
		{
			"Compute",
			"Every memory runs a model on a GPU",
			"There is no keyword shortcut to meaning. Each drawer you file and each search you run is embedded by the bge-m3 model — a neural network whose matrix maths is only fast on a GPU, and a busy GPU draws hundreds of watts. That electricity is spent on every write and every recall, not once at signup.",
		},
		{
			"Always-on",
			"Vectors live in memory, day and night",
			"So recall stays fast, Qdrant keeps each team's vectors hot in RAM on a server that runs 24/7 — powered, cooled and standing by whether you query once an hour or a thousand times a minute. You are renting a slice of always-on hardware, not just the seconds you spend searching.",
		},
		{
			"Isolation",
			"Your memory can't share a bill",
			"Every workspace gets its own physically separate vector store, named by a hash of the team id — the guarantee that one team's memory can never leak into another's. That isolation is the point, and it means we can't amortise one giant shared index across everyone: your compute is genuinely yours.",
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
			"What is long-term agent memory?",
			"Long-term agent memory is persistent storage that outlives a model's context window, letting an AI agent keep what it learned — decisions, facts, open threads — across sessions instead of forgetting when the window closes. AI Agent Memory stores each memory verbatim and recalls it later with hybrid semantic search, so later runs build on earlier ones.",
		},
		{
			"What is multi-agent memory?",
			"Multi-agent memory is one shared memory store that a whole team of agents reads and writes, rather than a private notebook per agent. AI Agent Memory is multi-tenant: every agent connecting with a team's token shares the same wings, drawers and knowledge graph, so one agent recalls what another filed — while memory stays isolated between teams in physically separate vector stores.",
		},
		{
			"Is AI Agent Memory open source?",
			"Yes. AI Agent Memory is open-source software — the full Go server is on GitHub, so you can read exactly how memories are stored, embedded and ranked, and self-host it with no proprietary core. Run the hosted service or your own copy; your agents' memory is portable and never locked in.",
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
			"Can I export all my data (BDAR / GDPR)?",
			"Yes. Any workspace member can download everything the workspace holds — drawers, diary, closets, knowledge-graph facts, tunnels, skills and account details — as a single self-contained SQLite file, scoped to your own tenant with credentials redacted. It is the BDAR (the EU GDPR) right of access and data portability: one click from the project page, and the file opens in any SQLite tool.",
		},
		{
			"What does agent memory cost to start?",
			"The Free plan is free forever with 10,000 requests per month. Teams running agents in production upgrade to Pro at €50 per month, or €500 per year (two months free).",
		},
		{
			"Why does agent memory cost money?",
			"Because hybrid semantic recall runs on real hardware. Every memory you file and every search you run is embedded by the bge-m3 model on a GPU that draws hundreds of watts, and each team's vectors are kept hot in a Qdrant store on a server that runs 24/7 — physically isolated per team, so the compute can't be shared. The Free plan absorbs that cost for small use; Pro covers the always-on GPU and electricity for teams that lean on it. And because the server is open source, you can always self-host and pay your own hardware bill instead.",
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

// clipboardExpr copies s to the clipboard and flashes the default "_copied"
// signal — the single-copy-button case used by the quick-start card.
func clipboardExpr(s string) string { return clipboardExprSignal(s, "_copied") }

// clipboardExprSignal is the datastar click expression that copies s to the
// clipboard and flashes the named boolean signal. A distinct signal per copy
// button matters because datastar signals are global: two buttons both driving
// "_copied" would flash together. The payload is JSON-encoded so quotes/newlines
// embed safely as a JS string literal.
func clipboardExprSignal(s, signal string) string {
	return "navigator.clipboard.writeText(" + jsString(s) +
		"); $" + signal + " = true; setTimeout(() => $" + signal + " = false, 1600)"
}

// landingInstallCmd is the copy-paste one-liner in the install section: it
// downloads the aiagentmemory binary from GitHub Releases and runs `install`.
const landingInstallCmd = "curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash"

// claudeGuideURL is the canonical public URL of the agent-facing install guide
// (served raw-Markdown by handleClaudeGuide). It is hardcoded like landingInstallCmd
// because the landing page is static marketing copy, not request-scoped.
const claudeGuideURL = "https://aiagentmemory.dev/claude-guide"

// landingClaudePrompt is the copy-paste prompt a visitor hands to Claude (or any
// agent) to install the kit hands-free: the agent fetches the guide, asks for the
// workspace token, and runs the install itself. Kept as one line so it pastes
// cleanly into a chat box.
const landingClaudePrompt = "Read " + claudeGuideURL + " and install the agentsmemory Claude Code kit for me. When you need my workspace API token, ask me — I'll create one in the dashboard."

// installGroup is one column of the "what it installs" breakdown: a heading, the
// command that triggers it, and the pieces it adds.
type installGroup struct {
	Title string
	Cmd   string
	Items []string
}

// landingInstallGroups enumerates what the installer sets up — the always-on
// core versus the opt-in --recommended extensions — mirroring the kit README so
// visitors see exactly which dependencies land on their machine.
func landingInstallGroups() []installGroup {
	return []installGroup{
		{
			Title: "Core — every install",
			Cmd:   "aiagentmemory install",
			Items: []string{
				"The /M and /am bootstrap commands",
				"The Stop hook that persists each session",
				"The agentsmemory MCP, authed by your token",
			},
		},
		{
			Title: "Recommended — opt in",
			Cmd:   "aiagentmemory install --recommended",
			Items: []string{
				"codebase-memory MCP — live code graph",
				"eidos plugin — spec + plan skills",
				"codex plugin — independent review",
			},
		},
	}
}

// cmdRef is one row of the install command reference: the command and a
// one-line description of what it does.
type cmdRef struct{ Cmd, Desc string }

// landingCommands is the tidy command reference under the install one-liner —
// install (global / sandboxed) plus the run/wrap launchers, so `run <name>`
// (open Claude in a sandbox without re-installing) is front and centre.
func landingCommands() []cmdRef {
	return []cmdRef{
		{"aiagentmemory install", "Global — wire the kit into your existing ~/.claude."},
		{"aiagentmemory install --sandbox <name>", "Isolated — a self-contained config under ~/.sandboxes/<name>."},
		{"aiagentmemory run <name>", "Open Claude in a sandbox — no re-install; args pass through to claude."},
		{"aiagentmemory wrap", "Open Claude against the global config."},
	}
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
				"description":         "AI Agent Memory is an open-source, multi-tenant memory palace for AI agents — long-term, multi-agent memory served as a remote MCP server, with hybrid semantic search backed by Ollama and Qdrant.",
				"offers": []map[string]any{
					{"@type": "Offer", "name": "Free", "price": "0", "priceCurrency": "EUR"},
					{"@type": "Offer", "name": "Pro Monthly", "price": "50", "priceCurrency": "EUR"},
					{"@type": "Offer", "name": "Pro Annual", "price": "500", "priceCurrency": "EUR"},
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
