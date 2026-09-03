package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
)

// resourceScheme is the URI scheme a memory is addressed by.
const resourceScheme = "agentsmemory"

// drawerURITemplate is the address of one memory (ADR-050).
//
// It carries the wing and the room, not just the id, because provenance is the
// thing a reader most needs and most often lacks: this protocol's own rule is
// that a memory is evidence from a context you do not have, and an address that
// hid the project it belongs to would argue against that.
const drawerURITemplate = resourceScheme + "://wing/{wing}/room/{room}/drawer/{id}"

// registerResources gives a memory an address.
//
// Every existing route into a memory SPENDS it: am_search returns a window of the
// text, am_get_drawer returns a chunk or the whole thing, and both put content in
// a tool result, which is the model's context. Four accepted records — ADR-013,
// ADR-019, ADR-024, ADR-044 — are mitigations for that one fact, and am_search's
// own description records where it ends: "there is no cursor".
//
// A resource is content with a URI. A drawer already has everything one needs: an
// opaque id minted once and never recomputed (ADR-038), a wing, a room, and a
// reassembly path that already exists. What was missing was the address.
//
// ⚠ TEMPLATES ONLY, NO resources/list. This palace holds thousands of drawers and
// enumerating them would be a page nobody asked for, in a protocol whose listing
// carries no relevance order — the capability's most obvious call would be its
// least useful one, and worse than the search that already exists. A template
// lets a client construct the URI for a memory it already has a reason to want,
// which is what the `uri` field on every hit gives it.
func registerResources(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	srv.AddResourceTemplate(
		mcp.NewResourceTemplate(drawerURITemplate, "memory",
			mcp.WithTemplateDescription("One memory, whole. Read it by the `uri` that am_search and am_get_drawer return on every hit — the wing and room are in the address so a reader can judge provenance without a second call. Retracted and superseded memories are not served: an ended record keeps its id, so a stored URI can outlive what it named, and this answers not-found rather than quietly handing back history."),
			mcp.WithTemplateMIMEType("text/plain"),
		),
		readDrawerResource(drawers, usageSvc),
	)
}

// readDrawerResource serves one memory by URI.
//
// It returns the WHOLE memory rather than the chunk the id happens to name. A
// resource that served one chunk would reintroduce, one protocol layer up, the
// exact defect ADR-044 was written against: a fragment that reads as a complete
// short memory. There is no `whole` parameter here for the same reason — a URI
// names a thing, and half of it is not that thing.
func readDrawerResource(drawers *palace.Service, usageSvc *usage.Service) server.ResourceTemplateHandlerFunc {
	return func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		// admit returns a tool result, which a resource handler cannot use — so the
		// tenant and the metering are taken the same way, and the refusal is an
		// error. Metering a read here matters: a resource is a cheaper call to make
		// than a tool, which is the point, and an uncounted route into the same
		// data would be a hole in the meter rather than a feature.
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return nil, fmt.Errorf("%s", refusalMessage(errResult))
		}

		wing, room, id, err := parseDrawerURI(req.Params.URI)
		if err != nil {
			return nil, err
		}

		// The addressed ROW, not the memory: a URI names one drawer, so the wing and
		// room it claims are checked against that row and its currency is judged by
		// the same call the tools use.
		//
		// ⚠ THE ENDED HALF OF THIS IS DEFENCE, NOT A FIX, AND SAYING SO IS THE POINT.
		// Review argued that GetMemory alone is not enough — MemoryChunks resolves any
		// id to its ROOT and returns the whole family, and GetMemory drops ended chunks
		// while refusing only when EVERY one has ended, so a URI naming an ended CHILD
		// could return its surviving siblings. Probed both routes named: invalidating
		// one child of a five-chunk memory, and shortening it through Update. Both end
		// the whole family (5 ended, 0 current), because a memory ends whole and
		// TestNoMemoryEndsHalfway pins that. So the mixed family is not reachable
		// through the public API today and GetMemory's all-ended refusal already
		// answers. This call is selected for the wing/room check either way — a mutant
		// removing that is killed — and the ended branch is a guard against an
		// invariant break elsewhere, which no test here can provoke.
		addressed, err := drawers.Get(ctx, t.TeamID, id)
		if err != nil {
			return nil, err
		}

		chunks, err := drawers.GetMemory(ctx, t.TeamID, id)
		if err != nil {
			return nil, err
		}
		if len(chunks) == 0 {
			return nil, fmt.Errorf("no memory at %s", req.Params.URI)
		}

		// ⚠ THE ADDRESS IS CHECKED AGAINST THE RECORD, NOT TRUSTED. A URI is
		// caller-supplied, and an id alone would resolve regardless of the wing and
		// room written beside it — so a stale or hand-edited address would return a
		// memory while displaying somebody else's provenance. Answering not-found is
		// the honest response to an address that no longer describes its target.
		//
		// ⚠ EXACT, NOT strings.EqualFold, AND THE FIRST VERSION HAD IT WRONG.
		// SanitizeName preserves case, so wing_acme and wing_ACME are two wings
		// holding two different sets of memories — measured, not assumed. A folded
		// comparison therefore made the check one case-fold WIDER than the palace: an
		// address naming wing_ACME resolved a drawer living in wing_acme and returned
		// it, which is the exact failure the check exists to refuse. Every address the
		// server itself renders preserves case, so nothing legitimate needs the fold.
		// ⚠ AND THE REFUSAL DOES NOT NAME WHERE THE RECORD REALLY LIVES. Saying "that
		// id lives in X/Y" turns a wrong guess into a lookup: a caller holding an id
		// learns its wing and room by asking with the wrong ones. Same-team, so not a
		// tenancy breach, but a URI is exactly the artifact that travels — and an
		// oracle that answers a question the caller could not otherwise ask is worth
		// nothing to a legitimate reader, who already has the address. Reported by
		// review.
		if addressed.Wing != wing || addressed.Room != room {
			return nil, fmt.Errorf("no memory at %s", req.Params.URI)
		}

		// ⚠ REASSEMBLE, DO NOT JOIN — AND THE FIRST VERSION JOINED. ChunkText overlaps
		// adjacent chunks by ChunkOverlap runes (320) for context continuity, so
		// concatenating them repeats up to 320 characters at every seam and a
		// separator inserts bytes the memory never had. That corruption is invisible
		// downstream: the result is longer than any chunk, contains the memory's
		// words, and reads as prose. palace.ReassembleMemory is the implementation
		// the search path already uses, including the zero-overlap diary case and the
		// single space that stops two chunks welding into a word appearing in
		// neither. Reported by review.
		return []mcp.ResourceContents{mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/plain",
			Text:     palace.ReassembleMemory(chunks),
		}}, nil
	}
}

// drawerURI renders a memory's address.
//
// Every wing, room and id is escaped: a room name is caller-supplied at write
// time, and an unescaped separator would produce a URI that parses back into a
// different address than the one it was built from.
func drawerURI(wing, room, id string) string {
	return fmt.Sprintf("%s://wing/%s/room/%s/drawer/%s",
		resourceScheme, url.PathEscape(wing), url.PathEscape(room), url.PathEscape(id))
}

// parseDrawerURI reads an address back, and refuses anything it does not fully
// recognise.
//
// It matches the literal segments rather than counting slashes, so a URI that is
// merely the right length in the wrong shape is rejected instead of being read as
// something plausible.
func parseDrawerURI(uri string) (wing, room, id string, err error) {
	rest, ok := strings.CutPrefix(uri, resourceScheme+"://")
	if !ok {
		return "", "", "", fmt.Errorf("not an %s URI: %s", resourceScheme, uri)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 6 || parts[0] != "wing" || parts[2] != "room" || parts[4] != "drawer" {
		return "", "", "", fmt.Errorf("malformed memory URI %s: want %s", uri, drawerURITemplate)
	}
	if wing, err = url.PathUnescape(parts[1]); err != nil {
		return "", "", "", fmt.Errorf("malformed wing in %s: %w", uri, err)
	}
	if room, err = url.PathUnescape(parts[3]); err != nil {
		return "", "", "", fmt.Errorf("malformed room in %s: %w", uri, err)
	}
	if id, err = url.PathUnescape(parts[5]); err != nil {
		return "", "", "", fmt.Errorf("malformed id in %s: %w", uri, err)
	}
	if wing == "" || room == "" || id == "" {
		return "", "", "", fmt.Errorf("malformed memory URI %s: want %s", uri, drawerURITemplate)
	}
	return wing, room, id, nil
}

// refusalMessage pulls the message out of a tool-result error so a resource handler
// can report the same refusal in the shape its own protocol uses.
//
// The two halves of the server speak different error dialects — a tool returns a
// result carrying isError, a resource returns a Go error — and admit only knows
// the first. Restating its message keeps one refusal with one wording rather than
// two that can drift.
func refusalMessage(r *mcp.CallToolResult) string {
	if r == nil {
		return "refused"
	}
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return "refused"
}
