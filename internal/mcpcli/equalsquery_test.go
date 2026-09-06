package mcpcli

import "testing"

// searchProps is the shape ParseArgs is given for am_search: the properties the
// tool actually declares, which is what tells an argument from a query.
//
// ⚠ rawTail carries the TAIL — the tool name is consumed by the caller before
// ParseArgs sees it. The first draft of these fixtures put "search" back at the
// front, and it won the positional in every case, so all five failed identically
// with query = "search" against a fix that works.
var searchProps = map[string]any{
	"query":         map[string]any{"type": "string"},
	"limit":         map[string]any{"type": "number"},
	"wing":          map[string]any{"type": "string"},
	"room":          map[string]any{"type": "string"},
	"snippet_chars": map[string]any{"type": "number"},
	"max_distance":  map[string]any{"type": "number"},
}

// TestAQueryContainingEqualsIsStillTheQuery pins the recall path against a defect
// that was silent and reached every session.
//
// ParseArgs treated any tail token containing "=" as a key=value argument. A
// positional query carrying an equals sign therefore vanished into the argument
// map under a key nobody asked for, the primary key arrived empty, and the server
// answered `required argument "query" not found` while the caller could see the
// query in their own command line.
//
// It landed on the UserPromptSubmit hook, which builds its query from the user's
// prompt: any prompt containing "=" — a URL, a code snippet, an XML-ish attribute
// — lost its recall entirely and reported only "agentsmemory could not look".
// Reported by a peer session as intermittent across "roughly half" its prompts;
// deterministic once the query was the variable rather than load.
func TestAQueryContainingEqualsIsStillTheQuery(t *testing.T) {
	for _, tc := range []struct{ name, tail, want string }{
		{"plain", "what changed here", "what changed here"},
		{"bare equals", "key=value pair", "key=value pair"},
		{"url", "see https://x.test/a?b=c for the shape", "see https://x.test/a?b=c for the shape"},
		{"attribute", `from="uds:/tmp/cc.sock" said so`, `from="uds:/tmp/cc.sock" said so`},
		{"code", "if err = f(); err != nil", "if err = f(); err != nil"},
		{"declared-key prefix", "wing=wing_acme is what I set", "wing=wing_acme is what I set"},
	} {
		got := ParseArgs(nil, []string{tc.tail}, searchProps, "query")
		if got["query"] != tc.want {
			t.Errorf("%s: query = %#v, want %q.\n"+
				"  A positional that contains \"=\" is still the positional. Losing it means the "+
				"server is asked for a search with no query, and on the recall path that is a "+
				"session silently getting no memory at all.", tc.name, got["query"], tc.want)
		}
	}
}

// TestARealArgumentIsStillAnArgument is the other direction, and it is why the
// discriminator is the schema rather than "never split a positional".
//
// Without this, the fix is satisfied by treating every bare token as the query,
// which would break `limit=3` and every other documented key=value the CLI accepts
// — trading a silent failure for a loud one rather than removing it.
func TestARealArgumentIsStillAnArgument(t *testing.T) {
	got := ParseArgs(nil, []string{"what changed here", "limit=3", "wing=wing_acme"}, searchProps, "query")

	if got["query"] != "what changed here" {
		t.Errorf("query = %#v, want the positional back", got["query"])
	}
	if got["limit"] != float64(3) && got["limit"] != 3 {
		t.Errorf("limit = %#v, want 3 — a declared property must still parse as an argument", got["limit"])
	}
	if got["wing"] != "wing_acme" {
		t.Errorf("wing = %#v, want wing_acme", got["wing"])
	}
}

// TestAnUndeclaredKeyDoesNotStealThePositional covers the case that made the
// original defect invisible: the stolen key is one the tool never declared, so
// nothing downstream reads it and nothing reports it missing.
func TestAnUndeclaredKeyDoesNotStealThePositional(t *testing.T) {
	got := ParseArgs(nil, []string{"session_id=abc123 what happened"}, searchProps, "query")

	if got["query"] != "session_id=abc123 what happened" {
		t.Errorf("query = %#v — an undeclared key must not consume the query", got["query"])
	}
	if _, stolen := got["session_id"]; stolen {
		t.Error("session_id was folded into the arguments; the tool declares no such property, " +
			"so it would travel to the server as an argument nobody asked for while the query " +
			"went missing — which is exactly how this defect stayed silent")
	}
}

// TestWithNoSchemaEveryKeyValueIsStillAnArgument binds the fallback that keeps
// the fix from being wider than the defect.
//
// The discriminator is the tool's declared properties, so a caller who has no
// schema — a tool whose input schema failed to arrive, or one that declares
// none — has nothing to ask. Answering "not declared" there would turn every
// argument to every tool into a positional in one step, breaking far more than
// the equals-in-a-query case being repaired.
func TestWithNoSchemaEveryKeyValueIsStillAnArgument(t *testing.T) {
	got := ParseArgs(nil, []string{"limit=3"}, nil, "query")

	if got["limit"] != "3" {
		t.Errorf("limit = %#v, want the argument back: with no properties to consult, "+
			"the pre-schema shape is the only safe answer", got["limit"])
	}
	if _, swallowed := got["query"]; swallowed {
		t.Errorf("query = %#v — a schemaless key=value became the positional, which is "+
			"the wider break this fallback exists to prevent", got["query"])
	}
}

// TestAnUndeclaredKeyStillTravelsWhenThePositionalIsTaken binds the other half:
// what happens to an undeclared key=value that has no positional role left.
//
// It is passed on rather than dropped, so the server refuses it by name. A drop
// would be the same silence as the defect above — the caller sees their token on
// their own command line and nothing anywhere says it went nowhere. This is also
// the documented hybrid syntax, which clients/claude-code exercises against a
// schema declaring neither key it passes.
func TestAnUndeclaredKeyStillTravelsWhenThePositionalIsTaken(t *testing.T) {
	got := ParseArgs(nil, []string{"what changed here", "session_id=abc123"}, searchProps, "query")

	if got["query"] != "what changed here" {
		t.Errorf("query = %#v, want the positional", got["query"])
	}
	if got["session_id"] != "abc123" {
		t.Errorf("session_id = %#v, want it forwarded so the server can refuse it by name "+
			"rather than the client silently discarding it", got["session_id"])
	}
}
