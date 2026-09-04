package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
)

// TestTheResourceTemplateIsAdvertisedAndReadable is the reachability half.
//
// A resource handler can be perfect while nothing registers it, or registered
// while the capability is never declared — and in the second case a conforming
// client never asks, so the resource exists and is never seen. That is this
// repository's §Reachability defect in the shape the handshake makes possible, so
// this drives a real client: read the capability, list the templates, then fetch.
func TestTheResourceTemplateIsAdvertisedAndReadable(t *testing.T) {
	cli, svc, team := newResourceServer(t)

	init, err := cli.Initialize(t.Context(), mcp.InitializeRequest{})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if init.Capabilities.Resources == nil {
		t.Fatal("initialize does not advertise the resources capability; a conforming client will never ask for one")
	}

	tmpl, err := cli.ListResourceTemplates(t.Context(), mcp.ListResourceTemplatesRequest{})
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	if len(tmpl.ResourceTemplates) == 0 {
		t.Fatal("no resource templates advertised; a client has no way to learn how a memory is addressed")
	}
	var found bool
	for _, rt := range tmpl.ResourceTemplates {
		if rt.URITemplate != nil && strings.Contains(rt.URITemplate.Raw(), "{id}") {
			found = true
			if strings.TrimSpace(rt.Description) == "" {
				t.Errorf("the %s template has no description", rt.Name)
			}
		}
	}
	if !found {
		t.Error("no template carries an {id} variable; nothing tells a client what part of the URI varies")
	}

	// A real memory, fetched by the address the tool layer hands out.
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: team, UserID: "u1", Role: tenant.RoleAdmin})
	added, err := svc.Add(ctx, team, palace.AddInput{
		Wing: "wing_acme", Room: "decisions", Content: "the retry budget is five attempts",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	uri := drawerURI("wing_acme", "decisions", added.Drawers[0].ID)

	got, err := cli.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: uri}})
	if err != nil {
		t.Fatalf("read %s: %v", uri, err)
	}
	if len(got.Contents) == 0 {
		t.Fatal("the resource resolved to no contents")
	}
	text, ok := got.Contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("contents[0] is %T, not text", got.Contents[0])
	}
	if !strings.Contains(text.Text, "retry budget is five attempts") {
		t.Errorf("the resource did not return the memory's own text:\n%s", text.Text)
	}
}

// TestAResourceReturnsTheWholeMemory pins the property that separates this from
// am_get_drawer's default.
//
// A URI names a thing, and half of it is not that thing. Serving the chunk an id
// happens to point at would reintroduce, one protocol layer up, the exact defect
// ADR-044 was written against: a fragment that reads as a complete short memory.
func TestAResourceReturnsTheWholeMemory(t *testing.T) {
	cli, svc, team := newResourceServer(t)
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: team, UserID: "u1", Role: tenant.RoleAdmin})

	// ⚠ EVERY SENTENCE IS DISTINCT, AND THAT IS THE POINT OF THE FIXTURE. The first
	// version repeated ONE sentence, which made the last chunk a literal substring of
	// the first — so a `strings.Contains` assertion for the last chunk's text passed
	// over a handler returning only the head. Measured: the head-only mutant survived
	// against a homogeneous fixture and is killed against this one. A fixture whose
	// pieces are indistinguishable cannot witness a claim about which piece came back.
	// ⚠ NO TRAILING WHITESPACE, and that is a fact about chunking rather than a
	// tidiness preference. ChunkText trims each chunk, so a memory filed with a
	// trailing space comes back one byte shorter — the round trip through Add is
	// lossy at the edges. The first version of this fixture ended in a space and the
	// equality assertion failed by exactly one character, which is the assertion
	// working: it found a real property, just not the one under test here.
	sentences := make([]string, 90)
	for i := range sentences {
		sentences[i] = fmt.Sprintf("sentence %03d of a memory long enough to be stored as several chunks.", i)
	}
	long := strings.Join(sentences, " ")
	added, err := svc.Add(ctx, team, palace.AddInput{Wing: "wing_acme", Room: "decisions", Content: long})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("fixture did not chunk (%d rows); it cannot show the difference", len(added.Drawers))
	}

	// Addressed by a NON-FIRST chunk, which is the case that would silently
	// return a fragment if the handler resolved the id it was given.
	last := added.Drawers[len(added.Drawers)-1]
	uri := drawerURI("wing_acme", "decisions", last.ID)

	got, err := cli.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: uri}})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// ⚠ BYTE-FOR-BYTE AGAINST WHAT WAS FILED, AND TWO WEAKER VERSIONS CAME FIRST.
	//
	// v1 required only len(text) > len(last.Content). On this fixture the head is
	// 1600 characters and the addressed last chunk is 929, so a handler returning
	// chunks[:1] passed comfortably — measured, not reasoned about.
	//
	// v2 required the text to CONTAIN both the first chunk and the addressed one.
	// That still passed a head-only handler, because the fixture repeated one
	// sentence and the last chunk was therefore a literal substring of the first. A
	// fixture whose pieces are indistinguishable cannot witness a claim about which
	// piece came back, which is why every sentence above is numbered.
	//
	// Neither version could see the bug that was actually shipped: chunks OVERLAP by
	// 320 runes, so joining them repeats text at every seam. The result was longer
	// than any chunk, contained both ends, and was not the memory. Only equality
	// with the filed text refuses all three at once.
	text := got.Contents[0].(mcp.TextResourceContents).Text
	if text != long {
		t.Errorf("the resource did not return the memory as filed: got %d chars, filed %d\n first difference at %d",
			len(text), len(long), firstDifference(text, long))
	}
}

// firstDifference reports where two strings diverge, because a diff of two 4kB
// near-identical blobs is unreadable and the offset alone says which failure it
// is: near a chunk boundary means a seam bug, at the very end means truncation.
func firstDifference(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// TestAnAddressThatNoLongerDescribesItsTargetIsRefused covers the half a URI
// makes possible and a tool call does not.
//
// A URI is caller-supplied and durable: it can be stored, pasted, and read back
// long after the memory moved. Resolving on the id alone would return a memory
// while DISPLAYING somebody else's provenance, which is worse than not finding it
// — the address would be lying in the one field a reader uses to judge whether a
// memory is even about their project.
func TestAnAddressThatNoLongerDescribesItsTargetIsRefused(t *testing.T) {
	cli, svc, team := newResourceServer(t)
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: team, UserID: "u1", Role: tenant.RoleAdmin})

	added, err := svc.Add(ctx, team, palace.AddInput{Wing: "wing_acme", Room: "decisions", Content: "a real memory"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := added.Drawers[0].ID

	// ⚠ EACH CASE NAMES THE STAGE IT MUST BE REFUSED AT, because "an error came
	// back" is the assertion that would pass over the bug this test exists for. A
	// parser that rejected every URI would refuse all five for one reason, and the
	// wing and room check — the half that is actually about provenance — would
	// never run. The `want` string is what distinguishes the stages.
	for _, tc := range []struct{ name, uri, want string }{
		// "no memory at <uri>" and nothing more: the refusal must not name where the
		// record really lives, or a caller holding an id learns its wing and room by
		// guessing wrong. It still identifies the stage, because a bad id is refused
		// by the palace in its own words and a bad shape by the router in its.
		{"wrong wing", drawerURI("wing_beta", "decisions", id), "no memory at"},
		{"wrong room", drawerURI("wing_acme", "gotchas", id), "no memory at"},
		{"wing differing only in case", drawerURI("WING_ACME", "decisions", id), "no memory at"},
		{"room differing only in case", drawerURI("wing_acme", "Decisions", id), "no memory at"},
		{"right shape, no such id", drawerURI("wing_acme", "decisions", "deadbeef"), "not found"},
		// ⚠ THESE TWO ARE REFUSED BY THE TEMPLATE ROUTER, NOT BY parseDrawerURI, and
		// the expectation says so rather than hiding it. A URI that does not match
		// the registered template never reaches the handler at all — mcp-go answers
		// "handler not found for resource URI". So the scheme and shape branches in
		// parseDrawerURI are a second line of defence for callers that reach it by
		// another route, not the layer a wire client meets first. Asserting the
		// parser's own wording here would have been asserting a message nothing on
		// this path emits.
		{"not a memory URI", "https://example.com/x", "handler not found"},
		{"missing segments", "agentsmemory://wing/wing_acme/" + id, "handler not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cli.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: tc.uri}})
			if err == nil {
				t.Fatalf("%s resolved; an address that does not describe its target must not return a memory", tc.uri)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused at the wrong stage: want a message containing %q, got %q", tc.want, err)
			}
			// The refusal must not answer the question the caller got wrong.
			if strings.Contains(err.Error(), "wing_acme/decisions") || strings.Contains(err.Error(), "lives in") {
				t.Errorf("the refusal names where the record really lives, so a wrong guess is a lookup: %q", err)
			}
		})
	}
}

// TestACaseVariantAddressDoesNotResolve is the one that was measured rather than
// assumed, and it is separate because it needs two real wings.
//
// SanitizeName preserves case, so wing_acme and wing_ACME are two wings holding
// two different sets of memories. The first version of the address check used
// strings.EqualFold and was therefore one case-fold WIDER than the palace: an
// address naming one wing resolved a drawer living in the other and returned it,
// which is precisely the failure the check exists to refuse.
func TestACaseVariantAddressDoesNotResolve(t *testing.T) {
	cli, svc, team := newResourceServer(t)
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: team, UserID: "u1", Role: tenant.RoleAdmin})

	lower, err := svc.Add(ctx, team, palace.AddInput{Wing: "wing_acme", Room: "decisions", Content: "the lower-case wing memory"})
	if err != nil {
		t.Fatalf("seed lower: %v", err)
	}
	upper, err := svc.Add(ctx, team, palace.AddInput{Wing: "WING_ACME", Room: "decisions", Content: "the upper-case wing memory"})
	if err != nil {
		t.Fatalf("seed upper: %v", err)
	}
	if lower.Drawers[0].ID == upper.Drawers[0].ID {
		t.Skip("the palace folds wing case, so the two wings are one and this test has nothing to say")
	}

	// The upper-case wing's ADDRESS over the lower-case wing's ID. Under a folded
	// comparison this returned the lower memory under an address naming the other
	// wing — a memory served with somebody else's provenance.
	crossed := drawerURI("WING_ACME", "decisions", lower.Drawers[0].ID)
	got, err := cli.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: crossed}})
	if err == nil {
		t.Fatalf("%s resolved to %q; the address names a different wing than the record", crossed,
			got.Contents[0].(mcp.TextResourceContents).Text)
	}

	// And each wing's own address still works, so the fix refuses the crossing
	// rather than refusing case.
	for _, want := range []struct{ uri, text string }{
		{drawerURI("wing_acme", "decisions", lower.Drawers[0].ID), "lower-case wing memory"},
		{drawerURI("WING_ACME", "decisions", upper.Drawers[0].ID), "upper-case wing memory"},
	} {
		res, err := cli.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: want.uri}})
		if err != nil {
			t.Errorf("a legitimate address was refused: %s: %v", want.uri, err)
			continue
		}
		if !strings.Contains(res.Contents[0].(mcp.TextResourceContents).Text, want.text) {
			t.Errorf("%s returned the wrong memory", want.uri)
		}
	}
}

// TestAnAddressForAnEndedRecordIsRefused covers the promise the template's own
// description makes, which nothing else here tested.
//
// The template says a retracted or superseded memory is not served. GetMemory
// alone does not deliver that: MemoryChunks resolves ANY id to its root and
// returns the whole family, and GetMemory then drops the ended chunks — refusing
// only when every one of them has ended. So a URI naming an ended record could
// resolve to whatever of its family survived: content the address did not name,
// handed back under an address for a record that is history. That is a false
// description, which this repository treats as unshipping the capability rather
// than as a cosmetic defect. Reported by review.
func TestAnAddressForAnEndedRecordIsRefused(t *testing.T) {
	cli, svc, team := newResourceServer(t)
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: team, UserID: "u1", Role: tenant.RoleAdmin})

	added, err := svc.Add(ctx, team, palace.AddInput{
		Wing: "wing_acme", Room: "decisions", Content: "the claim that was later withdrawn",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	uri := drawerURI("wing_acme", "decisions", added.Drawers[0].ID)

	// It resolves while the record is current — otherwise the assertion below
	// would pass over a URI that never worked at all.
	if _, err := cli.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: uri}}); err != nil {
		t.Fatalf("the address did not resolve before the record was ended: %v", err)
	}

	if err := svc.InvalidateDrawer(ctx, team, added.Drawers[0].ID, "withdrawn by the author"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	got, err := cli.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: uri}})
	if err == nil {
		t.Fatalf("the address still resolves after the record ended, returning %q; a stored URI outlives its target and must answer not-found",
			got.Contents[0].(mcp.TextResourceContents).Text)
	}
}

// TestEveryDrawerViewCarriesItsAddress is what makes the template reachable
// without a client knowing the scheme.
//
// A template says how an address is SHAPED; it does not hand out any particular
// one. If a hit did not carry its own uri, a caller would have to compose the
// string from the scheme — which is exactly the "you had to know" step the
// address exists to remove.
func TestEveryDrawerViewCarriesItsAddress(t *testing.T) {
	v := toView(palace.Drawer{ID: "abc123", Wing: "wing_acme", Room: "decisions", Content: "x"})
	want := "agentsmemory://wing/wing_acme/room/decisions/drawer/abc123"
	if v.URI != want {
		t.Errorf("toView URI = %q, want %q", v.URI, want)
	}

	// And it round-trips: an address the server renders must parse back to the
	// same three parts, or a client that stores one cannot use it.
	wing, room, id, err := parseDrawerURI(v.URI)
	if err != nil {
		t.Fatalf("the server's own URI does not parse: %v", err)
	}
	if wing != "wing_acme" || room != "decisions" || id != "abc123" {
		t.Errorf("round trip gave %q/%q/%q", wing, room, id)
	}

	// A room with a separator in it is the case escaping exists for: unescaped, it
	// would parse back as a different address than the one that was built.
	odd := drawerURI("wing_acme", "a/b", "id/1")
	w2, r2, i2, err := parseDrawerURI(odd)
	if err != nil {
		t.Fatalf("an escaped URI does not parse: %v", err)
	}
	if w2 != "wing_acme" || r2 != "a/b" || i2 != "id/1" {
		t.Errorf("escaped round trip gave %q/%q/%q, want wing_acme/a/b/id/1", w2, r2, i2)
	}
}

func newResourceServer(t *testing.T) (*client.Client, *palace.Service, string) {
	t.Helper()
	gdb := graphTestDB(t)
	svc := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)
	srv := New(Deps{
		Version: "test",
		Drawers: svc,
		Usage:   usage.NewService(usage.NewRepo(gdb), graphTestCaps{}),
	})
	cli, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	return cli, svc, "team-resources"
}

// TestTheResourceListingIsBounded is the assertion that keeps ADR-050's argument
// intact while giving the capability a door.
//
// ADR-050 rejected enumerating the palace, and that reasoning stands: thousands of
// entries in a listing with no relevance order is a worse answer than the search
// that already exists. What it did not anticipate is that Claude Code's documented
// discovery calls are "tools/list, prompts/list, and resources/list" — templates
// are named nowhere — so an empty listing made the addresses undiscoverable in the
// client that matters. A BOUNDED listing keeps both: a door, and no enumeration.
func TestTheResourceListingIsBounded(t *testing.T) {
	cli, svc, team := newResourceServer(t)
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: team, UserID: "u1", Role: tenant.RoleAdmin})

	for i := range listedResourceLimit + 7 {
		if _, err := svc.Add(ctx, team, palace.AddInput{
			Wing: "wing_acme", Room: "decisions",
			Content: fmt.Sprintf("memory number %03d, filed so the listing has more than its bound", i),
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	got, err := cli.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(got.Resources) == 0 {
		t.Fatal("the listing is empty with memories present; Claude Code reads resources/list and would show nothing")
	}
	if len(got.Resources) > listedResourceLimit {
		t.Errorf("the listing returned %d entries against a bound of %d — an unbounded listing is the enumeration ADR-050 rejected",
			len(got.Resources), listedResourceLimit)
	}
}

// TestAListedResourceUriReadsBack is the gate that matters most here.
//
// A listing is a set of promises. An entry that does not resolve is a pointer to
// nothing, which this corpus already treats as worse than no pointer at all —
// and a listing is exactly where a client learns which addresses exist, so a
// broken entry is discovered by a caller rather than by us.
func TestAListedResourceUriReadsBack(t *testing.T) {
	cli, svc, team := newResourceServer(t)
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: team, UserID: "u1", Role: tenant.RoleAdmin})
	if _, err := svc.Add(ctx, team, palace.AddInput{
		Wing: "wing_acme", Room: "decisions", Content: "a memory the listing must be able to hand out",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := cli.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got.Resources) == 0 {
		t.Fatal("nothing listed")
	}
	for _, r := range got.Resources {
		res, err := cli.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: r.URI}})
		if err != nil {
			t.Errorf("the listing handed out %s and it does not resolve: %v", r.URI, err)
			continue
		}
		if len(res.Contents) == 0 {
			t.Errorf("%s resolved to nothing", r.URI)
		}
	}
}

// TestTheListingAndTheTemplateBothResolve: adding the first must not cost the
// second.
//
// A client that reads templates keeps the general form — how ANY memory is
// addressed, including the thousands the bounded listing omits. A client that
// reads only the list gets a door. Both, or the capability is worse for one of
// them than it was before.
func TestTheListingAndTheTemplateBothResolve(t *testing.T) {
	cli, svc, team := newResourceServer(t)
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: team, UserID: "u1", Role: tenant.RoleAdmin})
	if _, err := svc.Add(ctx, team, palace.AddInput{Wing: "wing_acme", Room: "decisions", Content: "x marks a memory"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	list, err := cli.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil || len(list.Resources) == 0 {
		t.Fatalf("resources/list is empty (%v); the door is shut", err)
	}
	tmpl, err := cli.ListResourceTemplates(ctx, mcp.ListResourceTemplatesRequest{})
	if err != nil || len(tmpl.ResourceTemplates) == 0 {
		t.Fatalf("resources/templates/list is empty (%v); the general form is gone", err)
	}
}
