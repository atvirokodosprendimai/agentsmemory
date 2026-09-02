package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/billing"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// seedMember inserts a second user already holding a role in the workspace, so a
// test can act ON somebody rather than only as somebody. It returns that user's
// id.
func seedMember(t *testing.T, gdb *gorm.DB, teamID, email string, role tenant.Role) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	targetID := uuid.NewString()
	if err := gdb.Exec("INSERT INTO users (id, email, password_hash, display_name, created_at) VALUES (?,?,?,?,?)",
		targetID, email, "x", "T", now).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := gdb.Exec("INSERT INTO memberships (id, team_id, user_id, role, created_at) VALUES (?,?,?,?,?)",
		uuid.NewString(), teamID, targetID, string(role), now).Error; err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	return targetID
}

// asRole builds a POST request already carrying the workspace path param and the
// authenticated user, which is how every handler in this package receives them:
// requireUser puts the user in the context and chi puts teamID in the route.
// Extra path params are passed as name/value pairs.
func asRole(teamID, userID, path string, params ...string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("teamID", teamID)
	for i := 0; i+1 < len(params); i += 2 {
		rctx.URLParams.Add(params[i], params[i+1])
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(context.WithValue(ctx, userCtxKey, tenant.User{ID: userID, Email: "member@example.test"}))
}

// enabledBilling returns a billing.Service that Enabled() reports as live without
// any network wiring: the OpenCollective provider's checkout is a static
// contribution URL. The role gates in postUpgrade and postManageSubscription sit
// BEHIND the Enabled() check, so a disabled Service would make them unreachable
// and a test written over one would pass with the gate deleted.
func enabledBilling() *billing.Service {
	return billing.NewService(billing.Config{
		Provider:                 billing.ProviderOpenCollective,
		OpenCollectiveProjectURL: "https://opencollective.test/agentsmemory",
		PriceByPlanCode:          map[string]string{"pro_monthly": "https://opencollective.test/agentsmemory/contribute/pro"},
	}, nil, nil)
}

// TestAddMemberRefusesANonAdmin asserts the admin gate on the handler that grants
// workspace access.
//
// ⚠ FOUND BY MUTATION (round 5): neutering `if role != tenant.RoleAdmin` left the
// whole repository green. The refusal is asserted against the DATABASE rather
// than the flash text, because the flash is what the handler says and the
// membership row is what it did — and a reader who can add members can add an
// admin, which is workspace takeover by one more request.
func TestAddMemberRefusesANonAdmin(t *testing.T) {
	srv, gdb, userID, teamID := newRoleEnv(t, tenant.RoleMember)
	outsiderID := seedMember(t, gdb, uuid.NewString(), "outsider@example.test", tenant.RoleMember)
	_ = outsiderID

	before, err := srv.tenants.ListMembers(context.Background(), teamID)
	if err != nil {
		t.Fatalf("list before: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.postAddMember(rec, asRole(teamID, userID, "/projects/"+teamID+"/members"))

	after, err := srv.tenants.ListMembers(context.Background(), teamID)
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("a non-admin adding a member changed the roster from %d to %d members; the admin gate on "+
			"postAddMember did not refuse, and a member who can grant access can grant admin access",
			len(before), len(after))
	}
	if !strings.Contains(rec.Body.String(), "Only an admin can manage members") {
		t.Errorf("a non-admin was not told why the add was refused; body: %s", rec.Body.String())
	}
}

// TestSetMemberRoleRefusesANonAdmin asserts the admin gate on the handler that
// changes what a member may do.
//
// ⚠ FOUND BY MUTATION (round 5), and it is the worst of this round: without the
// gate a read-only member can hand ITSELF the admin role — every other control in
// the workspace is downstream of that one, so the escalation is total. The
// assertion reads the role back through the real repository, because a stubbed
// membership would agree with whatever the handler asked it.
func TestSetMemberRoleRefusesANonAdmin(t *testing.T) {
	srv, _, userID, teamID := newRoleEnv(t, tenant.RoleMember)

	rec := httptest.NewRecorder()
	req := asRole(teamID, userID, "/projects/"+teamID+"/members/"+userID+"/role?role=admin", "userID", userID)
	srv.postSetMemberRole(rec, req)

	role, err := srv.tenants.MembershipRole(context.Background(), userID, teamID)
	if err != nil {
		t.Fatalf("read role back: %v", err)
	}
	if role == tenant.RoleAdmin {
		t.Fatalf("a read-only member promoted itself to admin. The role gate on postSetMemberRole did not "+
			"refuse, so every admin-only control in the workspace is reachable by any member. Body: %s",
			rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Only an admin can change roles") {
		t.Errorf("a non-admin was not told why the role change was refused; body: %s", rec.Body.String())
	}
}

// TestRemoveMemberRefusesANonAdmin asserts the admin gate on the handler that
// revokes workspace access and the API keys behind it.
//
// ⚠ FOUND BY MUTATION (round 5). Removal is destructive in a way an add is not:
// it revokes the target's keys, so an ungated handler lets any member lock out
// every other member, the admins included.
func TestRemoveMemberRefusesANonAdmin(t *testing.T) {
	srv, gdb, userID, teamID := newRoleEnv(t, tenant.RoleMember)
	targetID := seedMember(t, gdb, teamID, "admin@example.test", tenant.RoleAdmin)

	rec := httptest.NewRecorder()
	srv.postRemoveMember(rec, asRole(teamID, userID, "/projects/"+teamID+"/members/"+targetID+"/remove", "userID", targetID))

	if _, err := srv.tenants.MembershipRole(context.Background(), targetID, teamID); err != nil {
		t.Fatalf("a non-admin removed another member: reading the target's membership back failed with %v. "+
			"The admin gate on postRemoveMember did not refuse, so any member can revoke an admin's access "+
			"and API keys. Body: %s", err, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Only an admin can remove members") {
		t.Errorf("a non-admin was not told why the removal was refused; body: %s", rec.Body.String())
	}
}

// TestSkillWriteRefusesAReadOnlyMember asserts the write gate behind the handler
// that publishes a centralised skill.
//
// A shared skill is loaded by every agent in the workspace and outranks what they
// would otherwise infer, so a read-only member who could save one changes how
// everybody else works — the same "arrives under the team's name" failure as the
// wing import, one artifact narrower.
//
// The service is REAL, over the same migrated database. A nil skill.Service also
// returns ErrForbidden here (the role check runs before it touches any field), so
// a test written over one passes whether or not the gate exists — it asserts the
// nil, not the policy. The refusal is then confirmed against the store: no skill
// row was written.
func TestSkillWriteRefusesAReadOnlyMember(t *testing.T) {
	srv, gdb, userID, teamID := newRoleEnv(t, tenant.RoleMember)
	srv.skills = skill.NewService(skill.NewRepo(gdb))

	// A parseable signals body, because postSkill reads the payload before the
	// service sees it: an unparseable body is refused for the wrong reason and
	// would pass with the gate deleted.
	body := `{"skillName":"house-style","skillDescription":"d","skillContent":"c"}`
	req := asRole(teamID, userID, "/projects/"+teamID+"/skills")
	req.Body = io.NopCloser(strings.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.postSkill(rec, req)

	if summaries, err := srv.skills.List(context.Background(), teamID); err != nil {
		t.Fatalf("list skills: %v", err)
	} else if len(summaries) != 0 {
		t.Errorf("a read-only member published %d shared skill(s); the role gate in skill.Service.Update did "+
			"not refuse, so a member can change the conventions every agent in the workspace loads",
			len(summaries))
	}
	if !strings.Contains(rec.Body.String(), "You need the writer or admin role to edit skills") {
		t.Errorf("a read-only member editing a shared skill was not refused; body: %s", rec.Body.String())
	}
}

// TestSkillBodyRefusesAReadOnlyMember asserts the gate on the handler that loads a
// stored skill body into the editor.
//
// ⚠ FOUND BY MUTATION (round 5): neutering `if !(webSkillCaller{role: role}.CanWrite())`
// in getSkillBody left the whole repository green. It is a READ, so it is the
// mildest finding of this round — but it is the one gate here that hands content
// out rather than refusing to take it in, and the comment above it states the
// intent the code was no longer proving: only editors reach the editor.
func TestSkillBodyRefusesAReadOnlyMember(t *testing.T) {
	srv, gdb, userID, teamID := newRoleEnv(t, tenant.RoleMember)
	srv.skills = skill.NewService(skill.NewRepo(gdb))

	body := `{"skillName":"house-style"}`
	req := asRole(teamID, userID, "/projects/"+teamID+"/skills/body")
	req.Body = io.NopCloser(strings.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.getSkillBody(rec, req)

	if !strings.Contains(rec.Body.String(), "You need writer access to edit skills") {
		t.Errorf("a read-only member was not refused the skill editor's load path; body: %s", rec.Body.String())
	}
}

// TestUpgradeRefusesANonAdmin asserts the admin gate on the handler that starts a
// checkout.
//
// ⚠ FOUND BY MUTATION (round 5). It is the least severe of this round — a
// non-admin cannot take the workspace over with it — but it commits the workspace
// to a recurring charge nobody with the authority to pay it approved.
func TestUpgradeRefusesANonAdmin(t *testing.T) {
	srv, _, userID, teamID := newRoleEnv(t, tenant.RoleMember)
	srv.billing = enabledBilling()

	rec := httptest.NewRecorder()
	srv.postUpgrade(rec, asRole(teamID, userID, "/projects/"+teamID+"/upgrade"))

	if !strings.Contains(rec.Body.String(), "Only a workspace admin can change the plan") {
		t.Errorf("a non-admin starting a checkout was not refused by postUpgrade; body: %s", rec.Body.String())
	}
}

// TestManageSubscriptionRefusesANonAdmin asserts the admin gate on the handler
// that opens the provider's billing portal.
//
// ⚠ FOUND BY MUTATION (round 5). The portal can cancel the plan, so an ungated
// handler is not merely a spending path but a way for any member to downgrade the
// workspace out from under it.
func TestManageSubscriptionRefusesANonAdmin(t *testing.T) {
	srv, _, userID, teamID := newRoleEnv(t, tenant.RoleMember)
	srv.billing = enabledBilling()

	rec := httptest.NewRecorder()
	srv.postManageSubscription(rec, asRole(teamID, userID, "/projects/"+teamID+"/billing/manage"))

	if !strings.Contains(rec.Body.String(), "Only a workspace admin can manage the plan") {
		t.Errorf("a non-admin opening the billing portal was not refused by postManageSubscription; body: %s",
			rec.Body.String())
	}
}
