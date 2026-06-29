package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/go-chi/chi/v5"
	"github.com/starfederation/datastar-go/datastar"
)

// webSkillCaller adapts a dashboard user's (team, role) to the skill package's
// RoleHolder, so the skill service authorizes a web edit exactly as it does an
// MCP update_skill call — one role gate, two surfaces. It mirrors
// mcpserver.skillCaller; the duplication is intentional, since each surface owns
// its own boundary adapter rather than sharing a type across bounded contexts.
type webSkillCaller struct {
	teamID string
	userID string
	role   tenant.Role
}

// Team identifies the workspace the skill is written to.
func (c webSkillCaller) Team() string { return c.teamID }

// User records who authored the version (stored as updated_by).
func (c webSkillCaller) User() string { return c.userID }

// CanWrite gates skill mutation: only a writer or admin may edit a shared skill.
// It is the single source of truth for the role gate — the view flag and the
// service call both derive from it, so the UI never disagrees with the server.
func (c webSkillCaller) CanWrite() bool {
	return c.role == tenant.RoleWriter || c.role == tenant.RoleAdmin
}

// skillSignals is the datastar payload for the skill editor. One typed struct per
// command (per the datastar Go convention) instead of ad-hoc map parsing.
type skillSignals struct {
	SkillName        string `json:"skillName"`
	SkillDescription string `json:"skillDescription"`
	SkillContent     string `json:"skillContent"`
}

// maxSkillRequestBytes caps the editor's POST body so an authenticated-but-
// untrusted writer cannot make the server buffer an unbounded payload. It is a
// transport guard only — the authoritative content limit (1 MB of decoded text)
// is enforced after parsing in skill.Service.Update. The ceiling sits well above
// that because JSON-escaping a 1 MB body can expand it several-fold; 8 MiB lets
// any legitimate skill through while still bounding memory.
const maxSkillRequestBytes int64 = 8 << 20

// membership resolves the signed-in user's role in the {teamID} URL parameter, or
// denies access. The team id is untrusted (it comes from the path), so a
// project-scoped handler must never act on it without a membership row: a
// non-member gets 404 (we never confirm a team exists to an outsider) and a
// lookup failure gets 500. Centralising this means every handler fails closed
// the same way.
func (s *Server) membership(w http.ResponseWriter, r *http.Request) (tenant.User, string, tenant.Role, bool) {
	u, _ := userFrom(r.Context())
	teamID := chi.URLParam(r, "teamID")
	role, err := s.tenants.MembershipRole(r.Context(), u.ID, teamID)
	if errors.Is(err, tenant.ErrNotMember) {
		http.NotFound(w, r)
		return tenant.User{}, "", "", false
	}
	if err != nil {
		http.Error(w, "could not verify access", http.StatusInternalServerError)
		return tenant.User{}, "", "", false
	}
	return u, teamID, role, true
}

// getProject renders a project's skills workspace. Access is gated by membership;
// the editor controls render only for a writer/admin (the server re-checks on
// write regardless, so hiding them is convenience, not the security boundary).
func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	proj, found := s.projectVM(r.Context(), u.ID, teamID)
	if !found {
		// Member of a team the dashboard can't shape (e.g. deleted mid-request):
		// treat as not found rather than render a broken page.
		http.NotFound(w, r)
		return
	}
	summaries, err := s.skills.List(r.Context(), teamID)
	if err != nil {
		http.Error(w, "could not load skills", http.StatusInternalServerError)
		return
	}
	s.render(w, r, views.ProjectDetailPage(views.ProjectDetailData{
		UserEmail:  u.Email,
		Project:    proj,
		Skills:     toSkillVMs(summaries),
		CanWrite:   webSkillCaller{role: role}.CanWrite(),
		ServerBase: requestBaseURL(r),
		Share:      s.buildShareData(r.Context(), u, teamID, role),
		Merge:      s.buildMergeData(r.Context(), u, teamID, role),
	}))
}

// requestBaseURL reconstructs this server's public origin (scheme + host) from
// the request, honouring an X-Forwarded-Proto from a TLS-terminating proxy. It
// is used to render the migration command with the correct /import host, so the
// command works whether the dashboard is reached over localhost or a real domain.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// postSkill creates or updates a skill from the editor signals and streams back
// datastar fragments: a flash, the refreshed list, and cleared inputs. The
// skill.Service enforces the role gate and validates the payload; this handler
// maps each outcome to a user-facing banner. A role refusal is a normal banner,
// not an HTTP error — the page stays put and tells the user why.
func (s *Server) postSkill(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}

	// Read (and cap) the request body BEFORE opening the SSE response. Starting
	// the SSE stream flushes the response, after which the request body is no
	// longer readable — so signals must be parsed first. The cap bounds buffering
	// of this authenticated-but-untrusted payload; a blank/malformed body is
	// caught by Update's validation below, an oversized one is reported here.
	r.Body = http.MaxBytesReader(w, r.Body, maxSkillRequestBytes)
	var sig skillSignals
	readErr := datastar.ReadSignals(r, &sig)

	sse := datastar.NewSSE(w, r)
	if readErr != nil {
		msg := "Could not read the skill — please try again."
		var maxErr *http.MaxBytesError
		if errors.As(readErr, &maxErr) {
			msg = "That skill is too large to save."
		}
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "error", Message: msg}))
		return
	}

	caller := webSkillCaller{teamID: teamID, userID: u.ID, role: role}
	sk, err := s.skills.Update(r.Context(), caller, sig.SkillName, sig.SkillDescription, sig.SkillContent)
	if err != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "error", Message: skillErrMsg(err)}))
		return
	}

	summaries, err := s.skills.List(r.Context(), teamID)
	if err != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
			Kind: "error", Message: "Saved, but the list failed to refresh — reload the page.",
		}))
		return
	}
	proj, found := s.projectVM(r.Context(), u.ID, teamID)
	if !found {
		// Keep the edit links working even if the display model failed to load.
		proj = views.ProjectVM{TeamID: teamID}
	}

	_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
		Kind:    "success",
		Message: "Saved \"" + sk.Name + "\" (v" + strconv.Itoa(sk.Version) + ").",
	}))
	_ = sse.PatchElementTempl(views.SkillsList(proj, toSkillVMs(summaries), caller.CanWrite()))
	_ = sse.MarshalAndPatchSignals(map[string]any{
		"skillName": "", "skillDescription": "", "skillContent": "",
	})
}

// getSkillBody loads an existing skill's body into the editor signals. The list
// payload omits bodies (they can be large), so editing fetches the freshest
// stored content on demand. Only editors reach the editor, so members are
// refused with a banner rather than silently handed content they can't save.
func (s *Server) getSkillBody(w http.ResponseWriter, r *http.Request) {
	_, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	sse := datastar.NewSSE(w, r)
	if !(webSkillCaller{role: role}.CanWrite()) {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
			Kind: "error", Message: "You need writer access to edit skills.",
		}))
		return
	}

	var sig skillSignals
	_ = datastar.ReadSignals(r, &sig)
	name := strings.TrimSpace(sig.SkillName)
	if name == "" {
		return // nothing to load; ignore a stray click
	}
	res, err := s.skills.Load(r.Context(), teamID, name)
	if err != nil {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
			Kind: "error", Message: "Could not load \"" + name + "\".",
		}))
		return
	}
	// Seed all three editor signals from the stored skill, then confirm.
	_ = sse.MarshalAndPatchSignals(map[string]any{
		"skillName":        res.Name,
		"skillDescription": res.Description,
		"skillContent":     res.Content,
	})
	_ = sse.PatchElementTempl(views.Flash(views.FlashVM{
		Kind:    "success",
		Message: "Loaded \"" + res.Name + "\" into the editor — edit and save to publish a new version.",
	}))
}

// projectVM finds one of the user's projects by team id and returns its display
// model (plan + usage), reusing projectsForUser so plan/usage shaping lives in
// exactly one place. A miss (not in the user's set) returns found=false.
func (s *Server) projectVM(ctx context.Context, userID, teamID string) (views.ProjectVM, bool) {
	projects, err := s.projectsForUser(ctx, userID)
	if err != nil {
		return views.ProjectVM{}, false
	}
	for _, p := range projects {
		if p.TeamID == teamID {
			return p, true
		}
	}
	return views.ProjectVM{}, false
}

// toSkillVMs maps skill.Summary metadata to the view model. The body is never
// part of a Summary, so the list payload stays light.
func toSkillVMs(summaries []skill.Summary) []views.SkillVM {
	out := make([]views.SkillVM, len(summaries))
	for i, sm := range summaries {
		out[i] = views.SkillVM{
			Name: sm.Name, Description: sm.Description, Version: sm.Version,
			UpdatedBy: sm.UpdatedBy, UpdatedAt: sm.UpdatedAt,
		}
	}
	return out
}

// skillErrMsg turns a skill.Service error into a user-facing banner message,
// keeping the raw error (and its internals) off the page.
func skillErrMsg(err error) string {
	switch {
	case errors.Is(err, skill.ErrForbidden):
		return "You need the writer or admin role to edit skills."
	case errors.Is(err, skill.ErrInvalidName):
		return "Enter a skill name (1–128 characters)."
	case errors.Is(err, skill.ErrInvalidContent):
		return "The skill body can't be empty."
	case errors.Is(err, skill.ErrInvalidDescription):
		return "The description is too long — keep it to one line."
	default:
		return "Could not save the skill. Please try again."
	}
}
