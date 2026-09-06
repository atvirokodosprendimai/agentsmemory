package mcpserver

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

var errCounting = errors.New("count failed")

// TestStatusNamesAWaitingInbox.
//
// An inbox item is only read if something makes the reader look. Measured
// 2026-08-20: every explicit tunnel in this palace reported access_count 0 since
// creation, and six handoff drawers had never been opened. The count existed the
// whole time — inside am_status's `wings` array, as one number among sixty.
//
// am_status is the site because it is the one call the protocol mandates first
// and it is server-side, so it cannot drift per-harness. The hint is the part an
// agent actually reads, so it must CHANGE when there is something to read: prose
// that always says "check your inbox" is prose that is always skipped.
func TestStatusNamesAWaitingInbox(t *testing.T) {
	waiting := inboxStatus("wing_agentmemories", 3, nil)
	if waiting.Count != 3 || waiting.Wing != "wing_agentmemories" {
		t.Fatalf("inbox block = %+v; want 3 in wing_agentmemories", waiting)
	}
	if !waiting.Known {
		t.Error("a counted inbox must report as known")
	}

	hint := statusHint(waiting, false)
	if !strings.Contains(hint, "3") || !strings.Contains(hint, "wing_agentmemories") {
		t.Errorf("the hint does not name the count and the wing, so nothing distinguishes it "+
			"from the hint on a quiet session:\n%s", hint)
	}

	quiet := statusHint(inboxStatus("wing_agentmemories", 0, nil), false)
	if quiet == hint {
		t.Error("the hint is identical with and without items waiting — an unconditional " +
			"reminder is one every session learns to skip")
	}
	if strings.Contains(quiet, "wing_agentmemories") {
		t.Errorf("the quiet hint still points at an empty inbox:\n%s", quiet)
	}
}

// TestStatusInboxWithoutADefaultWing: with no registration wing there is no "own
// wing" to count, and reporting 0 would be a claim. Zero and unknown are
// different answers and an agent cannot tell them apart from a bare number.
func TestStatusInboxWithoutADefaultWing(t *testing.T) {
	unknown := inboxStatus("", 0, nil)
	if unknown.Known {
		t.Error("reported a known inbox for a session with no wing to look in")
	}
	if unknown.Note == "" {
		t.Error("an unknown inbox must say why, or it reads as an empty one")
	}
	if h := statusHint(unknown, false); strings.Contains(h, "inbox") && !strings.Contains(h, "no wing") {
		t.Errorf("the hint sends the agent to an inbox it cannot name:\n%s", h)
	}
}

// TestStatusInboxSurvivesACountingFailure: am_status is the wake-up call. It
// already omits the taxonomy and the workspace block rather than failing when a
// lookup errors, and the inbox count is worth strictly less than either.
func TestStatusInboxSurvivesACountingFailure(t *testing.T) {
	failed := inboxStatus("wing_agentmemories", 0, errCounting)
	if failed.Known {
		t.Error("a failed count reported as a known zero — that is a false all-clear")
	}
	if failed.Note == "" {
		t.Error("a failed count must say so")
	}
}

// TestStatusResponseCarriesTheInbox pins the SELECTION. inboxStatus and
// statusHint can both be correct and never reach the wire: a count computed and
// not marshalled is invisible, and this repo's characteristic defect is exactly
// a component that works and nothing selects.
func TestStatusResponseCarriesTheInbox(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	body := string(src)

	i := strings.Index(body, `"total_drawers":`)
	if i < 0 {
		t.Fatal("the am_status response map has moved — this check has stopped checking anything")
	}
	end := strings.Index(body[i:], "})")
	if end < 0 {
		t.Fatal("could not find the end of the am_status response map")
	}
	resp := body[i : i+end]

	if !strings.Contains(resp, `"inbox"`) {
		t.Error("am_status does not marshal the inbox block — the count is computed and thrown away, " +
			"which is where it already sat for the six handoffs nobody read")
	}
	if !strings.Contains(resp, "statusHint(") {
		t.Error("am_status uses a fixed hint string, so it says the same thing whether or not " +
			"anything is waiting")
	}
	if !strings.Contains(body, "drawers.InboxCount(") {
		t.Error("nothing calls InboxCount, so the inbox block always reports zero")
	}
}

// TestTheInboxCountSaysWhoseInboxItCounted is the wing-mismatch half of the
// wake-up surface.
//
// The count is for the wing this REGISTRATION names. That is not necessarily the
// project the session is checked out in, and the server cannot tell — it never
// sees the client's working directory. Measured 2026-08-29: a session whose
// checkout was one project read a hint naming another project's inbox, and 23
// drawers in its own wing went unmentioned, including a same-day blocking question
// from another session. It reported "nothing waiting" in good faith.
//
// A confident number is what makes that miss invisible, so the number has to carry
// the caveat. This is the same move `resolution` made for am_kg_query's count:0 —
// the response distinguishes "I looked elsewhere" from "there is nothing".
func TestTheInboxCountSaysWhoseInboxItCounted(t *testing.T) {
	in := inboxStatus("wing_alpha", 16, nil)
	if !strings.Contains(in.Note, "REGISTRATION") {
		t.Errorf("the inbox note does not say the count is scoped to the registration's wing, so "+
			"a session in a differently-named checkout cannot tell that its own inbox went "+
			"uncounted: %q", in.Note)
	}
	if !strings.Contains(in.Note, "working directory") {
		t.Errorf("the note does not say why the server cannot resolve the session's own project; "+
			"without the reason the caveat reads as boilerplate: %q", in.Note)
	}

	hint := statusHint(in, false)
	if !strings.Contains(hint, "REGISTRATION") {
		t.Errorf("the hint is the sentence an agent actually reads and it does not carry the "+
			"caveat the note carries: %q", hint)
	}
	// The recommended call must be BOUNDED. Following it verbatim on a large inbox
	// returned 51.2 KB in one measured session, over that client's tool-result cap,
	// so the whole listing spilled to a file and left the context entirely — and an
	// empty-looking room reads as "nothing is filed".
	if !strings.Contains(hint, "limit:") {
		t.Errorf("the hint recommends a listing with no limit; past a client's result cap the "+
			"whole page leaves the context and the room reads as empty: %q", hint)
	}
}

// TestStatusHintNamesTheEntryProtocolWhenOneExists pins the pointer that the
// wake-up playbook could not deliver.
//
// ⚠ THE PLAYBOOK'S OWN LINE WAS MEASURED AND IT DOES NOT FIRE. Step 4 says "if a
// skill named start-here exists, load it FIRST", bolded, in the first am_skillset
// response every session reads. Measured 2026-08-30 across three fresh sessions in
// three repositories: all three read it, none loaded the skill, none learned it
// existed — because the directive is conditional on am_list_skills, the very call
// step 4 is asking them to make, and a read-only task prunes it. am_status is the
// call nobody prunes, and the server can answer the existence question itself.
func TestStatusHintNamesTheEntryProtocolWhenOneExists(t *testing.T) {
	quiet := inboxStatus("wing_agentmemories", 0, nil)

	with := statusHint(quiet, true)
	if !strings.Contains(with, EntrySkill) {
		t.Errorf("the hint never names %q, so the one call every session makes still does not "+
			"point at the entry protocol:\n%s", EntrySkill, with)
	}
	// Naming it is not enough: a session has to be told to CALL it, by name.
	if !strings.Contains(with, "am_load_skill") {
		t.Errorf("the hint names the skill but not the call that loads it — that is the same "+
			"defect one level down:\n%s", with)
	}

	// ⚠ CONDITIONAL, for the reason statusHint documents: a line that is always
	// there is a line every session learns to skip. A workspace with no entry
	// skill must get no sentence about one.
	without := statusHint(quiet, false)
	if strings.Contains(without, EntrySkill) {
		t.Errorf("a workspace with no entry protocol is told to load one anyway:\n%s", without)
	}
	if with == without {
		t.Error("the hint is identical with and without an entry skill — an unconditional " +
			"line is one every session learns to skip")
	}

	// And it survives the other axis: an inbox with items must not swallow it.
	busy := statusHint(inboxStatus("wing_agentmemories", 3, nil), true)
	if !strings.Contains(busy, EntrySkill) {
		t.Errorf("a waiting inbox drops the entry-protocol pointer:\n%s", busy)
	}
}

// TestEntryProtocolLeadsTheHintAndHasItsOwnField pins an ORDERING that was
// measured, not preferred.
//
// ⚠ THE SENTENCE WAS THIRD AND IT LOST. It sat after the inbox's own imperative
// ("read them first with am_list_drawers(...)"), and three sessions reported the
// same thing on 2026-08-30 without being asked: two "do this first"s in one field
// compete, and position decides. depozitas-d1, re-reading it: "I read the inbox
// sentence as the instruction and the ⚠ as an aside." wcag-ec: "those two
// literally contradict on ordering… moved, yes; loaded FIRST, probably not."
// playtrix-b7: "a hint reads as a list, and a list gets triaged."
//
// So the entry protocol leads the string AND is carried as its own field, because
// a field cannot be skimmed past on position the way a clause can.
func TestEntryProtocolLeadsTheHintAndHasItsOwnField(t *testing.T) {
	const names = true

	busy := statusHint(inboxStatus("wing_agentmemories", 3, nil), names)
	entryAt := strings.Index(busy, EntrySkill)
	inboxAt := strings.Index(busy, "am_list_drawers")
	if entryAt < 0 || inboxAt < 0 {
		t.Fatalf("hint is missing one of the two instructions:\n%s", busy)
	}
	if entryAt > inboxAt {
		t.Errorf("the inbox instruction comes before the entry protocol — two imperatives in "+
			"one field compete and the first one wins, which is the defect three sessions "+
			"reported:\n%s", busy)
	}

	// The field is the robust half: present with a skill, absent without.
	if b := entryProtocolBlock(true); b == nil {
		t.Error("no entry_protocol block when the workspace has one")
	} else {
		call, _ := b["call"].(string)
		if !strings.Contains(call, "am_load_skill") || !strings.Contains(call, EntrySkill) {
			t.Errorf("the block does not carry the literal call to make: %v", b)
		}
	}
	if b := entryProtocolBlock(false); b != nil {
		t.Errorf("a workspace with no entry protocol still gets a block: %v", b)
	}
}

// TestAddDrawerDescribesTheEntryRoomsDifferentRules pins a description against the
// behaviour it describes. The entry room has always behaved differently from every
// other room; WHAT that difference IS has now changed twice, and this test has to
// track the current one rather than the one it was born pinning.
//
// ⚠ THIS TEST WAS GREEN FOR THE WRONG REASON AND A REVIEWER CAUGHT IT (PR #147).
// It originally required the word "REFUSED", because a write to the entry room that
// would chunk was refused. ADR-046 deleted that refusal — and the assertion kept
// passing, because the description had gained an UNRELATED refusal in the meantime
// ("an ENDED record cannot be relocated"). A substring check on a word that appears
// for a second reason is not a check; it is a coincidence with a green tick, and it
// survived the very commit that removed the thing it was written to pin.
//
// So the assertion below is keyed to the CURRENT difference — entry-room records are
// served WHOLE at every wake-up (ADR-046), which is a cost every session pays rather
// than a limit the server enforces — and it separately refuses the retired sentence,
// so the description cannot drift back to promising a refusal that is gone.
func TestAddDrawerDescribesTheEntryRoomsDifferentRules(t *testing.T) {
	srv := New(Deps{})
	tool := srv.GetTool(mcpprotocol.ToolPrefix + "add_drawer")
	if tool == nil {
		t.Fatal("am_add_drawer is not registered — this check has stopped checking anything")
	}

	desc := tool.Tool.Description
	if !strings.Contains(desc, palace.EntryRoom) {
		t.Errorf("the am_add_drawer description never names %q, the one room whose writes are "+
			"refused rather than chunked — so the rule is undiscoverable until an agent hits "+
			"it:\n%s", palace.EntryRoom, desc)
	}
	// Naming the room is not enough: the description must say what is different
	// about it. Today that is the SERVING cost, not a write-time refusal.
	if !strings.Contains(desc, "WHOLE") {
		t.Errorf("the description names %q but never says its records are served WHOLE at every "+
			"wake-up. That is the entry room's only remaining difference, and an agent who does "+
			"not know it cannot know that length there is paid by every session:\n%s",
			palace.EntryRoom, desc)
	}
	// And it must not drift back to the refusal ADR-046 deleted. Keyed to the
	// retired CLAUSE rather than to the word "REFUSED", which the description
	// still uses correctly for the unrelated ended-record rule — matching that
	// word is what let this test pass through the commit that removed its subject.
	if strings.Contains(desc, "REFUSED if it would chunk") {
		t.Errorf("the description still says a write to %q is refused if it would chunk. ADR-046 "+
			"deleted that refusal, so this promises a limit the server no longer has:\n%s",
			palace.EntryRoom, desc)
	}

	raw, err := json.Marshal(tool.Tool.InputSchema)
	if err != nil {
		t.Fatalf("marshal the input schema: %v", err)
	}
	if !strings.Contains(string(raw), palace.EntryRoom) {
		t.Errorf("the `room` parameter description never names %q, so a caller choosing a room "+
			"cannot learn that one of them behaves differently", palace.EntryRoom)
	}

	// ⚠ BOTH DESCRIPTIONS, AND THE BOUND IN EACH. Review of PR #325 reverted the
	// room parameter's corrected sentence and watched 45 packages stay green: this
	// test read tool.Tool.Description only, and its `WHOLE` check was satisfied by
	// the OTHER description — the one that was still un-qualified. Two sentences
	// about one behaviour, fifteen lines apart, disagreeing, with the more
	// prominent one wrong: a caller reads the tool description first, and hosted
	// v0.0.121 served the un-qualified sentence in the catalogue.
	//
	// The limit is asserted from the constant rather than as a word, because "first
	// ten" is exactly the shape of "roughly 800 characters" — a number frozen into
	// prose that outlived the code. Change BootstrapEagerLimit and this fails until
	// both sentences follow.
	limit := strconv.Itoa(palace.BootstrapEagerLimit)
	for _, part := range []struct{ what, text string }{
		{"tool description", desc},
		{"`room` parameter description", string(raw)},
	} {
		if !strings.Contains(part.text, limit) {
			t.Errorf("the %s says entry-room records are served whole without naming the bound "+
				"(%s). Records past it arrive as POINTERS, so the unqualified sentence promises "+
				"a cost model the server does not have:\n%s", part.what, limit, part.text)
		}
		if !strings.Contains(part.text, "pointers") {
			t.Errorf("the %s never says what happens past the bound; a reader takes the first "+
				"sentence for the whole rule:\n%s", part.what, part.text)
		}
	}
}
