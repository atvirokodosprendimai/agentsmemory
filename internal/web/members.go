package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/go-chi/chi/v5"
	"github.com/starfederation/datastar-go/datastar"
)

// maxMemberRequestBytes caps the add-member POST body. The payload is tiny (an
// email and a role), so a small ceiling is plenty and bounds buffering of this
// authenticated-but-untrusted body.
const maxMemberRequestBytes int64 = 1 << 16 // 64 KiB

// memberSignals is the datastar payload for the add-member form — one typed
// struct per command (per the datastar Go convention) rather than ad-hoc map
// parsing. Role changes carry their value in the URL query (see postSetMemberRole)
// because datastar signals are global and a per-row select would collide; a role
// is not a secret, so a query param is safe here.
type memberSignals struct {
	MemberEmail string `json:"memberEmail"`
	MemberRole  string `json:"memberRole"`
}

// buildMembersVM assembles the Members-section view model for a team: the member
// list plus whether the viewer may manage it (admin only). selfID marks the
// viewer's own row so the UI can label it and steer them away from self-lockout.
// It is the single place the section is shaped — reused by the full page render
// and by every member mutation's refresh.
func (s *Server) buildMembersVM(ctx context.Context, teamID, selfID string, role tenant.Role) (views.MembersVM, error) {
	members, err := s.tenants.ListMembers(ctx, teamID)
	if err != nil {
		return views.MembersVM{}, err
	}
	vms := make([]views.MemberVM, 0, len(members))
	for _, m := range members {
		vms = append(vms, views.MemberVM{
			UserID: m.UserID, Email: m.Email, DisplayName: m.DisplayName,
			Role: string(m.Role), IsSelf: m.UserID == selfID,
		})
	}
	return views.MembersVM{TeamID: teamID, CanManage: role == tenant.RoleAdmin, Members: vms}, nil
}

// patchMembers re-renders the Members section with an optional inline notice or
// error, reloading the list so the fragment always reflects committed state after
// a mutation. A refresh failure degrades to the same fragment with a reload hint
// rather than a 500 into the stream.
func (s *Server) patchMembers(sse *datastar.ServerSentEventGenerator, ctx context.Context, teamID, selfID string, role tenant.Role, notice, errMsg string) {
	vm, err := s.buildMembersVM(ctx, teamID, selfID, role)
	if err != nil {
		_ = sse.PatchElementTempl(views.MembersBlock(views.MembersVM{
			TeamID: teamID, CanManage: role == tenant.RoleAdmin,
			Error: "Could not refresh the member list — reload the page.",
		}))
		return
	}
	vm.Notice, vm.Error = notice, errMsg
	_ = sse.PatchElementTempl(views.MembersBlock(vm))
}

// postAddMember adds an existing user (by email) to the workspace with a role and
// mints them their own API key. Admin-only: managing members is privileged, so a
// non-admin is refused with an inline error and the change never runs (the UI
// hides the control, but the server is the boundary). Each domain outcome maps to
// a message; no secret is shown here — the new member reveals their own key from
// their own dashboard.
func (s *Server) postAddMember(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	// Parse (and cap) the body BEFORE opening the SSE stream — once the stream is
	// flushed the request body is no longer readable.
	r.Body = http.MaxBytesReader(w, r.Body, maxMemberRequestBytes)
	var sig memberSignals
	readErr := datastar.ReadSignals(r, &sig)

	sse := datastar.NewSSE(w, r)
	if role != tenant.RoleAdmin {
		s.patchMembers(sse, r.Context(), teamID, u.ID, role, "", "Only an admin can manage members.")
		return
	}
	if readErr != nil {
		s.patchMembers(sse, r.Context(), teamID, u.ID, role, "", "Could not read the form — please try again.")
		return
	}
	email := strings.TrimSpace(sig.MemberEmail)
	if email == "" {
		s.patchMembers(sse, r.Context(), teamID, u.ID, role, "", "Enter the email of a registered user to add.")
		return
	}
	memberRole := tenant.Role(strings.TrimSpace(sig.MemberRole))
	if memberRole == "" {
		memberRole = tenant.RoleMember // default to least privilege when unset
	}
	m, err := s.tenants.AddMemberByEmail(r.Context(), teamID, email, memberRole)
	if err != nil {
		s.patchMembers(sse, r.Context(), teamID, u.ID, role, "", addMemberErrMsg(err, email))
		return
	}
	s.patchMembers(sse, r.Context(), teamID, u.ID, role,
		"Added "+m.Email+" as "+string(m.Role)+". They can reveal their own API key from their dashboard.", "")
	// Clear the email so the admin can add another; keep the role selection.
	_ = sse.MarshalAndPatchSignals(map[string]any{"memberEmail": ""})
}

// postSetMemberRole changes a member's role. Admin-only; the target user id comes
// from the path and the new role from the query (?role=…). The tenant layer
// validates the role and guards the last admin, so a bad value or a self-lockout
// is refused with an inline message rather than an HTTP error.
func (s *Server) postSetMemberRole(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	sse := datastar.NewSSE(w, r)
	if role != tenant.RoleAdmin {
		s.patchMembers(sse, r.Context(), teamID, u.ID, role, "", "Only an admin can change roles.")
		return
	}
	targetID := chi.URLParam(r, "userID")
	newRole := tenant.Role(strings.TrimSpace(r.URL.Query().Get("role")))
	if err := s.tenants.SetMemberRole(r.Context(), teamID, targetID, newRole); err != nil {
		s.patchMembers(sse, r.Context(), teamID, u.ID, role, "", roleChangeErrMsg(err))
		return
	}
	s.patchMembers(sse, r.Context(), teamID, u.ID, role, "Role updated.", "")
}

// postRemoveMember removes a member from the workspace and revokes every key they
// hold in it (so they can no longer connect). Admin-only; the last admin is
// guarded by the tenant layer. The target user id comes from the path.
func (s *Server) postRemoveMember(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	sse := datastar.NewSSE(w, r)
	if role != tenant.RoleAdmin {
		s.patchMembers(sse, r.Context(), teamID, u.ID, role, "", "Only an admin can remove members.")
		return
	}
	targetID := chi.URLParam(r, "userID")
	if err := s.tenants.RemoveMember(r.Context(), teamID, targetID); err != nil {
		s.patchMembers(sse, r.Context(), teamID, u.ID, role, "", removeMemberErrMsg(err))
		return
	}
	s.patchMembers(sse, r.Context(), teamID, u.ID, role,
		"Member removed — their API keys have been revoked.", "")
}

// addMemberErrMsg maps an AddMemberByEmail failure to a user-facing line. The
// email is echoed so an admin adding several people knows which one bounced.
func addMemberErrMsg(err error, email string) string {
	switch {
	case errors.Is(err, tenant.ErrUserNotFound):
		return "No account for " + email + " — ask them to sign up first, then add them."
	case errors.Is(err, tenant.ErrAlreadyMember):
		return email + " is already a member of this workspace."
	case errors.Is(err, tenant.ErrInvalidRole):
		return "Pick a valid role (member, writer, or admin)."
	default:
		return "Could not add that member right now. Please try again."
	}
}

// roleChangeErrMsg maps a SetMemberRole failure to a user-facing line.
func roleChangeErrMsg(err error) string {
	switch {
	case errors.Is(err, tenant.ErrLastAdmin):
		return "This is the workspace's only admin — promote someone else to admin first."
	case errors.Is(err, tenant.ErrInvalidRole):
		return "Pick a valid role (member, writer, or admin)."
	case errors.Is(err, tenant.ErrNotMember):
		return "That person is no longer a member — the list has been refreshed."
	default:
		return "Could not change that role right now. Please try again."
	}
}

// removeMemberErrMsg maps a RemoveMember failure to a user-facing line.
func removeMemberErrMsg(err error) string {
	switch {
	case errors.Is(err, tenant.ErrLastAdmin):
		return "You can't remove the workspace's only admin — promote another admin first."
	case errors.Is(err, tenant.ErrNotMember):
		return "That person is no longer a member — the list has been refreshed."
	default:
		return "Could not remove that member right now. Please try again."
	}
}
