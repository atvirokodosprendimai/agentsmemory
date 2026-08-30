package mcpserver

import (
	"strconv"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// TestAddDrawerAdvertisesTheThresholdThatFreezesAMemory fails when am_add_drawer's
// published description stops naming palace.ChunkSize — the number that decides,
// permanently and at the moment of creation, whether a memory can ever be edited
// again.
//
// This is the sibling of TestUpdateDrawerAdvertisesTheBoundItEnforces, and it is
// here because that ADR-016 shape recurred exactly: one producer was fixed and the
// one beside it was missed. The UPDATE bound got both halves — enforcement in
// palace, advertisement on the wire. The CREATION threshold got neither. Its
// description shipped for months claiming "~800 chars", the frozen Python miner's
// figure that chunk.go:10-18 explicitly diverges from and explains why.
//
// The cost is not a rounding error, because the threshold is a ONE-WAY DOOR.
// Content over ChunkSize becomes several drawers sharing a parent, and
// Service.Update then refuses every in-place content edit AND every wing/room move
// for the life of that memory. An agent that believes the limit is 800 writes well
// under it and is merely puzzled; an agent that reasons FROM 800 concludes a
// 1200-rune note is already multi-chunk and stops trying to maintain it, or files a
// live document at 1700 believing it has doubled the limit and discovers otherwise
// only when the first edit is refused — days later, with no way back that preserves
// the id, the filed_at, or anything pointing at them.
//
// Three independent read-only sessions on 2026-08-25 read the wrong number off this
// description before any of them read the constant, and each reported it as a
// finding. A description that RESTATES a value is a second source of truth; the
// repair is to interpolate palace.ChunkSize, not to correct the digits, because
// correcting the digits leaves the drift mechanism intact.
//
// It reads the LIVE tools/list rather than the registration source on purpose: the
// wire is what an agent receives, and a description that never reaches it is not
// documentation.
func TestAddDrawerAdvertisesTheThresholdThatFreezesAMemory(t *testing.T) {
	const tool = mcpprotocol.ToolPrefix + "add_drawer"

	_, tools := liveSurface(t, false)

	var description string
	var found bool
	for _, published := range tools {
		if published.Name == tool {
			description, found = published.Description, true
			break
		}
	}
	if !found {
		t.Fatalf("live tools/list never published %s", tool)
	}

	threshold := strconv.Itoa(palace.ChunkSize)
	if !strings.Contains(description, threshold) {
		t.Errorf("%s's description never names the %s-rune threshold that decides whether a memory is editable for life.\n"+
			"  description: %q\n"+
			"  ChunkText splits above palace.ChunkSize, and Service.Update then refuses every\n"+
			"  in-place edit and every wing/room move on the result — permanently. An agent\n"+
			"  reads this description to choose how long to make a note it intends to maintain,\n"+
			"  so a number that is absent (or, as shipped, wrong) is not a documentation gap:\n"+
			"  it is the caller being told the wrong thing about a one-way door.\n"+
			"  Interpolate palace.ChunkSize into the description; do not hardcode it, or the\n"+
			"  sentence and the constant drift apart silently — which is exactly what happened.", tool, threshold, description)
	}
}
