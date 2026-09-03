package mcpserver

import (
	"context"
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

	long := strings.Repeat("a memory long enough to be stored as several chunks. ", 90)
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
	text := got.Contents[0].(mcp.TextResourceContents).Text
	if len(text) <= len(last.Content) {
		t.Errorf("the resource returned %d chars for a chunk of %d; it served the fragment rather than the memory",
			len(text), len(last.Content))
	}
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

	for _, tc := range []struct{ name, uri string }{
		{"wrong wing", drawerURI("wing_beta", "decisions", id)},
		{"wrong room", drawerURI("wing_acme", "gotchas", id)},
		{"not a memory URI", "https://example.com/x"},
		{"right shape, no such id", drawerURI("wing_acme", "decisions", "deadbeef")},
		{"missing segments", "agentsmemory://wing/wing_acme/" + id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := cli.ReadResource(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: tc.uri}}); err == nil {
				t.Errorf("%s resolved; an address that does not describe its target must not return a memory", tc.uri)
			}
		})
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
