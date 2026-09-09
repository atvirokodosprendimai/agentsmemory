package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// seedSkill creates a skill through the real tool surface, so every test below
// starts from a body the server actually stored rather than one a fake handed it.
func seedSkill(t *testing.T, h *mcptest.Harness, name, description, content string) {
	t.Helper()
	h.MustCall(t, "am_update_skill", map[string]any{
		"name": name, "description": description, "content": content,
	})
}

// loadSkill reads a skill back through am_load_skill — the same call an agent
// makes, so the test asserts what a caller would actually see.
func loadSkill(t *testing.T, h *mcptest.Harness, name string) map[string]any {
	t.Helper()
	return h.JSON(t, h.MustCall(t, "am_load_skill", map[string]any{"name": name}))
}

// TestUpdateSkillEditsAreReachableFromTheToolSurface is the rung
// internal/skill's own tests cannot cover, and the one this repository keeps
// shipping without: Service.Patch being correct is worth nothing while no agent
// can select it.
//
// The package tests drive the service directly, so all of them pass with the
// `edits` argument declared in the schema and never read by the handler — the
// tool would then fall through to "nothing to write" or, worse, to a whole-body
// replace. Delete the parseEdits/Patch branch in registerUpdateSkill and this
// test is what goes red.
func TestUpdateSkillEditsAreReachableFromTheToolSurface(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	seedSkill(t, h, "guide", "the house guide", "step one\nstep two\nstep three\n")

	res := h.JSON(t, h.MustCall(t, "am_update_skill", map[string]any{
		"name": "guide",
		"edits": []any{
			map[string]any{"old": "step two", "new": "step two, corrected"},
		},
	}))
	if got, _ := res["wrote"].(string); got != "edits" {
		t.Errorf("wrote = %q; want \"edits\" — the response must say which path ran", got)
	}
	if n, _ := res["edits_applied"].(float64); n != 1 {
		t.Errorf("edits_applied = %v; want 1", res["edits_applied"])
	}

	loaded := loadSkill(t, h, "guide")
	body, _ := loaded["content"].(string)
	if want := "step one\nstep two, corrected\nstep three\n"; body != want {
		t.Errorf("stored body = %q, want %q", body, want)
	}
	// The half a body assertion cannot see: an edits write must not disturb the
	// description, and the whole-body path reads an omitted one as "clear it".
	if desc, _ := loaded["description"].(string); desc != "the house guide" {
		t.Errorf("description = %q; want it untouched by an edits write", desc)
	}
	if v, _ := loaded["version"].(float64); v != 2 {
		t.Errorf("version = %v; want the edit to bump it to 2", loaded["version"])
	}
}

// TestUpdateSkillRefusesAnAmbiguousAnchorAtTheToolBoundary proves the refusal
// travels. A service that refuses correctly while the tool reports success is the
// tool-boundary drop this repository has measured before — and here the caller
// would go away believing a change landed.
func TestUpdateSkillRefusesAnAmbiguousAnchorAtTheToolBoundary(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	seedSkill(t, h, "guide", "d", "note: one\nnote: two\n")

	refusal := h.MustRefuse(t, "am_update_skill", map[string]any{
		"name":  "guide",
		"edits": []any{map[string]any{"old": "note", "new": "NOTE"}},
	})
	for _, want := range []string{"2 times", "count=2"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal does not carry %q, so the caller cannot act on it: %s", want, refusal)
		}
	}
	if body, _ := loadSkill(t, h, "guide")["content"].(string); body != "note: one\nnote: two\n" {
		t.Errorf("a refused call wrote anyway: %q", body)
	}
}

// TestUpdateSkillRefusesAMalformedEditList covers the distinction parseAnchorList
// records next door: tolerance is right where an unreadable entry means "no
// anchors added", and wrong here, where it means "this change did not happen".
// `edits: {…}` instead of `[{…}]` is an ordinary mistake for an LLM caller.
func TestUpdateSkillRefusesAMalformedEditList(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	seedSkill(t, h, "guide", "d", "body\n")

	refusal := h.MustRefuse(t, "am_update_skill", map[string]any{
		"name":  "guide",
		"edits": map[string]any{"old": "body", "new": "BODY"}, // an object, not a list
	})
	if !strings.Contains(refusal, "LIST") {
		t.Errorf("the refusal does not name the shape it wanted: %s", refusal)
	}
	if body, _ := loadSkill(t, h, "guide")["content"].(string); body != "body\n" {
		t.Errorf("a malformed edit list wrote anyway: %q", body)
	}
}

// TestUpdateSkillRefusesContentAndEditsTogether: a call carrying both expresses
// two different intentions for one body. Picking either silently is the class of
// quiet wrong answer the whole design avoids.
func TestUpdateSkillRefusesContentAndEditsTogether(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	seedSkill(t, h, "guide", "d", "body\n")

	refusal := h.MustRefuse(t, "am_update_skill", map[string]any{
		"name": "guide", "content": "wholly new",
		"edits": []any{map[string]any{"old": "body", "new": "BODY"}},
	})
	if !strings.Contains(refusal, "not both") {
		t.Errorf("refusal does not say the two are exclusive: %s", refusal)
	}
	if body, _ := loadSkill(t, h, "guide")["content"].(string); body != "body\n" {
		t.Errorf("an ambiguous call wrote anyway: %q", body)
	}
}

// TestUpdateSkillWithNeitherArgumentSaysWhatToSend. `content` stopped being a
// required parameter when `edits` arrived, so the schema no longer refuses a call
// that carries neither — the handler has to, and it has to point at both
// arguments rather than at the one that used to be mandatory.
func TestUpdateSkillWithNeitherArgumentSaysWhatToSend(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	refusal := h.MustRefuse(t, "am_update_skill", map[string]any{"name": "guide"})
	for _, want := range []string{"content", "edits"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal does not name %q as an option: %s", want, refusal)
		}
	}
}

// TestUpdateSkillHonoursExpectedVersionAtTheToolBoundary proves the guard is
// wired, not merely implemented. expected_version is the only protection an edit
// list has against being applied to a body it was not written for, and a number
// the handler parses and drops would leave every caller believing they had it.
func TestUpdateSkillHonoursExpectedVersionAtTheToolBoundary(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")
	seedSkill(t, h, "guide", "d", "original\n")
	// A second write moves the skill under the version the caller below read.
	seedSkill(t, h, "guide", "d", "someone else's body\n")

	refusal := h.MustRefuse(t, "am_update_skill", map[string]any{
		"name": "guide", "expected_version": 1,
		"edits": []any{map[string]any{"old": "someone", "new": "SOMEONE"}},
	})
	for _, want := range []string{"v1", "v2"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal does not name %s, so the caller cannot tell how far behind it is: %s", want, refusal)
		}
	}
	// And the same edit succeeds once the caller names the version that is there,
	// which is what distinguishes a wired guard from a handler that refuses always.
	h.MustCall(t, "am_update_skill", map[string]any{
		"name": "guide", "expected_version": 2,
		"edits": []any{map[string]any{"old": "someone", "new": "SOMEONE"}},
	})
	if body, _ := loadSkill(t, h, "guide")["content"].(string); body != "SOMEONE else's body\n" {
		t.Errorf("the guarded edit did not apply: %q", body)
	}
}

// TestUpdateSkillEditsRequireTheWriteRole holds the new path to the same role gate
// as the old one. A second write path is a second place the check can be missed,
// which is the shape §Reachability records against every duplicated mechanism
// here — and the tool's own description promises writer-or-admin for both.
func TestUpdateSkillEditsRequireTheWriteRole(t *testing.T) {
	// No seeding, and that is the assertion: Patch checks the role BEFORE it looks
	// the skill up, so a member is refused without learning whether the skill
	// exists. AsRole builds its own server, so a seed here would prove nothing
	// about the member's database anyway — the ordering is what makes the test
	// meaningful rather than accidental.
	member := mcptest.AsRole(t, tenant.RoleMember)
	refusal := member.MustRefuse(t, "am_update_skill", map[string]any{
		"name":  "guide",
		"edits": []any{map[string]any{"old": "body", "new": "BODY"}},
	})
	if !strings.Contains(strings.ToLower(refusal), "role") {
		t.Errorf("the refusal does not name the missing role: %s", refusal)
	}
}
