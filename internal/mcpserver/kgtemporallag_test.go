package mcpserver

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
)

// TestTheTemporalLagIsOnTheToolSurface is the advertisement half of issue #47,
// and it is the half the fix cannot do without.
//
// The write path now stamps an instant, so no NEW retraction joins the ambiguous
// class. What it cannot repair is a date-only `valid_to` a caller passes
// explicitly, or one stored before the change — and for those, `as_of` still lags
// status:"current" by up to a day. palace.TestADateOnlyEndStillStretchesToEndOfDay
// proves that residual is real; this proves an agent can find out about it
// without hitting it.
//
// The issue's own framing is why this is a gate rather than a note: option 1 was
// "document it and stop", and a documentation-only remedy that nothing checks
// decays into no remedy at all. AGENTS.md puts it generally — documentation is
// load-bearing, and a promise a test does not read is a promise nobody keeps.
//
// It reads the LIVE tools/list schema, not the registration source, because the
// wire is what an agent receives: a sentence that never reaches the caller is not
// documentation, and a description edited in a file nothing publishes would pass
// a source-level grep while telling no agent anything.
func TestTheTemporalLagIsOnTheToolSurface(t *testing.T) {
	_, tools := liveSurface(t, false)

	// Each entry names ONE claim a reader has to be able to reach, and the words
	// are matched case-insensitively so a rewording that keeps the meaning keeps
	// the gate. What is pinned is that the sentence exists and says which two
	// things disagree — never its phrasing.
	for _, want := range []struct {
		tool     string
		property string
		phrases  []string
		why      string
	}{
		{
			tool:     mcpprotocol.ToolPrefix + "kg_query",
			property: "as_of",
			phrases:  []string{"date-granular", "current"},
			why: "as_of is the READING side of the lag. A caller asking for a past-date snapshot\n" +
				"    gets a fact that status:\"current\" calls dead, and nothing in the answer says\n" +
				"    the two were measured at different resolutions",
		},
		{
			tool:     mcpprotocol.ToolPrefix + "kg_invalidate",
			property: "ended",
			phrases:  []string{"instant", "as_of"},
			why: "ended is the WRITING side. It must say that omitting it stamps an instant — the\n" +
				"    description used to say \"default today\", which is now simply wrong — and that a\n" +
				"    date passed explicitly opts back into the day-scale reading",
		},
	} {
		description, ok := livePropertyDescription(t, tools, want.tool, want.property)
		if !ok {
			t.Errorf("%s publishes no %s property, so the caveat has nowhere to live", want.tool, want.property)
			continue
		}
		lowered := strings.ToLower(description)
		for _, phrase := range want.phrases {
			if !strings.Contains(lowered, strings.ToLower(phrase)) {
				t.Errorf("%s's %s description never names %q.\n"+
					"  description: %q\n"+
					"  %s.\n"+
					"  Issue #47's cheapest option was \"document it and stop\"; this is what stops\n"+
					"  that option from decaying into nothing being documented at all.",
					want.tool, want.property, phrase, description, want.why)
			}
		}
	}
}
