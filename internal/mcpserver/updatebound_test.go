package mcpserver

import (
	"strconv"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestUpdateDrawerAdvertisesTheBoundItEnforces fails when am_update_drawer's
// published schema stops naming the one number that decides whether a call
// succeeds at all: the maximum content length.
//
// This is the reachability defect in its documentation costume, and it is the
// half that stayed open after the bound itself landed. Enforcement is real and
// gated — palace.TestUpdateRefusesContentTheEmbedderWouldSilentlyTruncate proves
// MaxEmbedRunes+1 is refused and exactly MaxEmbedRunes is accepted — so the code
// was correct and every test was green while the schema an agent actually reads
// said only "New verbatim content (re-embedded on change)". An agent therefore
// learned the limit by having a write fail, which is the worst possible moment,
// and that sentence was wrong a second way besides: every accepted update
// re-embeds the whole memory, a wing/room move included.
//
// Per AGENTS.md a setting is wired only when both halves exist. The palace test
// is the enforcement half; this is the advertisement half. Neither one alone
// keeps the promise, because a bound nobody can discover and a bound nobody
// applies fail the caller in exactly the same way.
//
// It reads the LIVE tools/list schema rather than the registration source on
// purpose: the wire is what an agent receives, and a description that never
// reaches it is not documentation.
func TestUpdateDrawerAdvertisesTheBoundItEnforces(t *testing.T) {
	const tool = mcpprotocol.ToolPrefix + "update_drawer"

	_, tools := liveSurface(t, false)
	description, ok := livePropertyDescription(t, tools, tool, "content")
	if !ok {
		t.Fatalf("%s publishes no content property, so the argument this bound applies to is undiscoverable", tool)
	}

	// Since ADR-038 T4 the bound is MaxContentLength, not MaxEmbedRunes: a content
	// change supersedes and files the new text through Add, which chunks, so the
	// embedder's single-piece window is no longer what the caller has to respect.
	// MaxEmbedRunes still bounds what reaches the embedder — ChunkText enforces it
	// — but it is no longer a limit an agent can hit or needs to know.
	bound := strconv.Itoa(palace.MaxContentLength)
	if !strings.Contains(description, bound) {
		t.Errorf("%s's content description never names the %s-character bound it enforces.\n"+
			"  description: %q\n"+
			"  An agent reads this schema to decide what it may send. The service refuses\n"+
			"  content over palace.MaxContentLength, so an agent that cannot see the number\n"+
			"  finds it by having a write fail — and the natural repair (shorten and retry\n"+
			"  blind) is guesswork against a limit that is written down in the code.\n"+
			"  Interpolate palace.MaxContentLength into the description; do not hardcode it,\n"+
			"  or the sentence and the constant drift apart silently.", tool, bound, description)
	}
}

// livePropertyDescription returns the description a live tools/list publishes for
// one property of one tool. It reports false when the tool or the property is
// absent, so a caller can fail with its own message rather than a nil map panic.
func livePropertyDescription(t *testing.T, tools []mcp.Tool, toolName, property string) (string, bool) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != toolName {
			continue
		}
		raw, ok := tool.InputSchema.Properties[property]
		if !ok {
			return "", false
		}
		schema, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("%s's %s property is %T, want a JSON Schema object", toolName, property, raw)
		}
		description, _ := schema["description"].(string)
		return description, true
	}
	t.Fatalf("live tools/list never published %s", toolName)
	return "", false
}
