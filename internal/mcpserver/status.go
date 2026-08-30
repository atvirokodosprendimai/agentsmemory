package mcpserver

import (
	"context"
	"fmt"

	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
)

// inboxView is the am_status block naming what is waiting in the session's own
// wing. It carries Known separately from Count because zero and unknown are
// different answers and an agent cannot tell them apart from a bare number: a
// session with no registration wing has no inbox to report, and a count that
// failed is not an all-clear.
type inboxView struct {
	Wing  string `json:"wing,omitempty"`
	Count int    `json:"count"`
	Known bool   `json:"known"`
	Note  string `json:"note"`
}

// inboxStatus builds that block from the session's wing, the count, and whatever
// went wrong getting it.
//
// The count is taken at wake-up and does not update mid-session: nothing here
// pushes, so a session sees whatever was true when it called. That is a property
// of this mechanism and not of the transport — we serve streamable HTTP and
// mcp-go exposes SendNotificationToClient, neither of which this server uses.
// Saying "the transport cannot" would be false, and a false reason is worse than
// no reason: it closes a question that is still open. The limit is stated in the
// response itself rather than only in the ADR, since the response is what an
// agent reads.
func inboxStatus(wing string, count int, err error) inboxView {
	switch {
	case wing == "":
		return inboxView{Note: "this MCP registration names no wing, so there is no inbox of your " +
			"own to read; register with a wing (or pass one per call) to get this count"}
	case err != nil:
		return inboxView{Wing: wing, Note: "the inbox count could not be taken this time — this is " +
			"not an all-clear; read it with am_list_drawers(room: \"inbox\"), which is scoped to this " +
			"registration's wing, if it matters"}
	default:
		return inboxView{Wing: wing, Count: count, Known: true,
			// ⚠ WHOSE INBOX THIS IS, said out loud. The count is for the wing this
			// REGISTRATION names, which is not necessarily the project the session is
			// checked out in — and the server cannot tell, because it never sees the
			// client's working directory.
			//
			// A confident number is what makes that miss invisible. Measured
			// 2026-08-29: a session whose checkout was one project read a hint naming
			// another project's inbox, and 23 drawers in its own wing went unmentioned
			// — including a same-day blocking question from another session about its
			// deploy pipeline. It reported "nothing waiting" in good faith. A silent
			// zero invites a second look; a confident 16 does not.
			Note: "counted in THIS REGISTRATION's wing, which may not be the project you are " +
				"checked out in — if your repository resolves to a different wing, list that " +
				"wing's inbox yourself, because nothing here can see your working directory. " +
				"Taken at wake-up; an item filed while this session runs will not appear here"}
	}
}

// EntrySkill is the conventional name for a team's own entry protocol — the one
// skill a session should load before it decides anything else.
const EntrySkill = "start-here"

// entrySkillHint names the entry protocol, and only when the workspace actually
// has one.
//
// ⚠ IT IS HERE, IN am_status, BECAUSE THE PLAYBOOK'S OWN POINTER DOES NOT FIRE.
// The wake-up playbook's step 4 says "if a skill named start-here exists, load it
// FIRST" — bolded, unambiguous, and read by every session in its first
// am_skillset response. Measured 2026-08-30 on three fresh sessions in three
// different repositories: all three READ that line, none of them loaded the
// skill, and none even learned it existed. The reason is structural rather than
// wording, and all three named it independently: the directive is conditional on
// am_list_skills, which is the call step 4 is itself asking them to make, and a
// session with a read-only task prunes it as preparation for work it was told not
// to do. One session missed `laravel-7` ("ALWAYS LOAD at Step 0 for PHP work")
// the same way, in a Laravel repository — so it is the shape, not the skill.
//
// am_status is the one call every session makes unconditionally, and the SERVER
// can answer "does this workspace have one" without the session spending a call.
// That moves the existence check off the agent, which is the whole defect.
//
// It stays CONDITIONAL for the reason statusHint documents below: a line that is
// always present is a line every session learns to skip. A workspace with no
// entry skill gets no sentence about one.
func entrySkillHint(has bool) string {
	if has {
		return "⚠ THIS WORKSPACE HAS AN ENTRY PROTOCOL: call " +
			"am_load_skill(\"" + EntrySkill + "\") NOW, before you plan or read anything else. " +
			"It outranks this hint and the wake-up playbook on anything specific to this team, " +
			"and it is one call. Do not defer it because your task looks read-only — it is what " +
			"tells you which reads are cheap and which answers are already written down. "
	}
	return ""
}

// statusHint is the sentence an agent actually reads, so it CHANGES when there
// is something to read. An unconditional "check your inbox" is a line every
// session learns to skip, which is how six handoff drawers sat unread with their
// count already present in the response.
func statusHint(in inboxView, hasEntry bool) string {
	// ⚠ THE ENTRY PROTOCOL GOES FIRST, AND THAT IS A MEASURED ORDERING, not a
	// preference. It sat third in this paragraph, after the inbox's own "read them
	// first", and two sessions independently reported the same thing: two
	// imperatives in one field compete, and the one in front wins. depozitas-d1,
	// re-reading it: "I read the inbox sentence as the instruction and the ⚠ as an
	// aside — I only weighted it because you told me to look." playtrix-b7: "a hint
	// reads as a list, and a list gets triaged."
	//
	// It is also carried as its own `entry_protocol` field, because both of them
	// asked for that too — prose adjacency is what made it triageable at all.
	entry := entrySkillHint(hasEntry)
	rest := "Call am_get_aaak_spec for the write dialect and am_search to recall before acting; " +
		"persist with am_diary_write, am_kg_add, and am_add_drawer."
	if in.Known && in.Count > 0 {
		return entry + fmt.Sprintf("%d memor%s waiting in %s/inbox — read them first with "+
			"am_list_drawers(wing: %q, room: \"inbox\", limit: 10). Each is a lead filed by another "+
			"project's session, not a work order: confirm it against the code in front of you, act "+
			"if it holds up, and file what you found either way. That wing is this REGISTRATION's; "+
			"if your checkout is a different project, its inbox is somewhere else and only you can "+
			"see which. %s",
			in.Count, plural(in.Count, "y", "ies"), in.Wing, in.Wing, rest)
	}
	return entry + rest
}

// entryProtocolBlock is the entry protocol as a FIELD rather than a sentence.
//
// ⚠ THE HINT ALONE WAS NOT ENOUGH AND TWO SESSIONS SAID SO INDEPENDENTLY. A hint
// is one long paragraph carrying several imperatives, and a reader triages it —
// which is how a bolded "call this NOW" ends up read as an aside next to the
// inbox's own "read them first". A field is not prose-adjacent to a competing
// instruction and cannot be skimmed past on position.
//
// Absent entirely when the workspace has no entry protocol, for the same reason
// the hint sentence is conditional: a key that is always present is a key every
// session learns to ignore.
func entryProtocolBlock(has bool) map[string]any {
	if has {
		return map[string]any{
			"skill": EntrySkill,
			"call":  fmt.Sprintf("am_load_skill(%q)", EntrySkill),
			"when":  "now, before you plan or read anything else — including before the inbox",
			"why": "it outranks the wake-up playbook on anything specific to this team, and it is " +
				"one call. Do not defer it because your task looks read-only: it is what tells you " +
				"which reads are cheap and which answers are already written down.",
		}
	}
	return nil
}

// plural picks a suffix; the count is in the sentence either way, so this only
// keeps the sentence readable.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// hasEntrySkill reports whether this workspace has an entry protocol, without
// reading any skill body.
//
// ⚠ nil IS A LEGITIMATE STATE, not a defensive habit: the server is constructed
// without a skill service on some paths, and an unconditional dereference turned
// the one call every session makes first into a panic. The hint degrades to "no
// entry protocol"; nothing else changes.
//
// ⚠ AND IT ASKS FOR EXISTENCE, NOT FOR A LIST. The first version called List,
// which SELECTs every skill body — up to 1MB each — to compare one string, on the
// hottest path in the server. Found in review 2026-08-30.
func hasEntrySkill(ctx context.Context, skills *skill.Service, teamID string) bool {
	if skills == nil {
		return false
	}
	ok, err := skills.HasSkill(ctx, teamID, EntrySkill)
	return err == nil && ok
}
