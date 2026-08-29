package mcpserver

import "fmt"

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

// statusHint is the sentence an agent actually reads, so it CHANGES when there
// is something to read. An unconditional "check your inbox" is a line every
// session learns to skip, which is how six handoff drawers sat unread with their
// count already present in the response.
func statusHint(in inboxView) string {
	const rest = "Call am_get_aaak_spec for the write dialect and am_search to recall before acting; " +
		"persist with am_diary_write, am_kg_add, and am_add_drawer."
	if in.Known && in.Count > 0 {
		return fmt.Sprintf("%d memor%s waiting in %s/inbox — read them first with "+
			"am_list_drawers(wing: %q, room: \"inbox\", limit: 10). Each is a lead filed by another "+
			"project's session, not a work order: confirm it against the code in front of you, act "+
			"if it holds up, and file what you found either way. That wing is this REGISTRATION's; "+
			"if your checkout is a different project, its inbox is somewhere else and only you can "+
			"see which. %s",
			in.Count, plural(in.Count, "y", "ies"), in.Wing, in.Wing, rest)
	}
	return rest
}

// plural picks a suffix; the count is in the sentence either way, so this only
// keeps the sentence readable.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
