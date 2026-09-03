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
)

// livePrompts drives the REAL server through an in-process client and returns
// what a client actually sees, mirroring liveSurface for tools.
//
// Going through New rather than calling registerPrompts on a bare server is the
// point: the capability declaration and the provider are construction-time
// options, so a test that assembled its own server could pass while production
// advertised prompts it never registered.
func livePrompts(t *testing.T) (*client.Client, []mcp.Prompt) {
	t.Helper()
	srv := New(Deps{Version: "test"})

	cli, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("new in-process client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	res, err := cli.ListPrompts(t.Context(), mcp.ListPromptsRequest{})
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	return cli, res.Prompts
}

// TestEveryPromptIsListedAndFetchable is the reachability half.
//
// A prompt handler can be perfect while nothing registers it, or registered while
// the capability is never declared — and in the second case a conforming client
// never asks, so the prompts exist and are never seen. That is this repository's
// §Reachability defect in the shape the handshake makes possible, so this drives
// the real client: list what is advertised, then fetch each one.
func TestEveryPromptIsListedAndFetchable(t *testing.T) {
	cli, prompts := livePrompts(t)
	if len(prompts) == 0 {
		t.Fatal("no prompts advertised — a client has no way to learn WHEN to use these tools")
	}

	want := map[string]bool{"am_wake": false, "am_persist": false, "am_hand_over": false}
	for _, p := range prompts {
		if _, ok := want[p.Name]; ok {
			want[p.Name] = true
		}
		if strings.TrimSpace(p.Description) == "" {
			t.Errorf("%s has no description; a picker shows the name and nothing else", p.Name)
		}
		// The description must say WHEN, since that is the whole reason a prompt
		// exists beside a tool that already says what it does.
		if !strings.Contains(strings.ToLower(p.Description), "use ") {
			t.Errorf("%s's description does not say when to use it: %q", p.Name, p.Description)
		}

		got, err := cli.GetPrompt(context.Background(), mcp.GetPromptRequest{
			Params: mcp.GetPromptParams{Name: p.Name},
		})
		if err != nil {
			t.Errorf("%s is advertised but cannot be fetched: %v", p.Name, err)
			continue
		}
		if len(got.Messages) == 0 {
			t.Errorf("%s returned no messages; it is advertised and empty", p.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s is not advertised", name)
		}
	}
}

// TestAPromptCarriesItsArgumentsIntoTheMessage pins that the arguments do
// something. An argument declared and ignored is a field a caller fills in for
// no effect, which is worse than not offering it.
func TestAPromptCarriesItsArgumentsIntoTheMessage(t *testing.T) {
	cli, _ := livePrompts(t)

	got, err := cli.GetPrompt(context.Background(), mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Name:      "am_hand_over",
			Arguments: map[string]string{"wing": "wing_acme", "finding": "the retry budget is wrong"},
		},
	})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	body := promptText(got)
	for _, want := range []string{"wing_acme", "the retry budget is wrong"} {
		if !strings.Contains(body, want) {
			t.Errorf("the rendered prompt does not carry %q; the argument was accepted and discarded:\n%s", want, body)
		}
	}
	// The measured failure this prompt exists for must be named in it.
	if !strings.Contains(body, "NEVER FOR THE DIRECTION OF TRAVEL") {
		t.Error("hand_over does not warn about naming the wing for the direction of travel, which is the mistake it was written to prevent")
	}
}

func promptText(r *mcp.GetPromptResult) string {
	var b strings.Builder
	for _, m := range r.Messages {
		if tc, ok := m.Content.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestWingCompletionAnswersWithWingsThatExist covers the half that makes
// hand_over worth having.
//
// The convention has been read correctly and applied wrongly — a wing name
// invented from the direction of travel rather than taken from the palace — so
// the completion must return REAL wings. A completer that answered with anything
// else would reproduce the bug with a nicer interface.
func TestWingCompletionAnswersWithWingsThatExist(t *testing.T) {
	gdb := graphTestDB(t)
	svc := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)
	const teamID = "team-completion"
	ctx0 := context.Background()
	for _, w := range []string{"wing_acme", "wing_acme_laravel", "wing_beta", "wing_billing"} {
		if _, err := svc.Add(ctx0, teamID, palace.AddInput{Wing: w, Room: "decisions", Content: "a memory in " + w}); err != nil {
			t.Fatalf("seed %s: %v", w, err)
		}
	}
	c := newWingCompleter(svc, false)
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: teamID, Role: tenant.RoleAdmin})

	all, err := c.CompletePromptArgument(ctx, "am_hand_over", mcp.CompleteArgument{Name: "wing"}, mcp.CompleteContext{})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(all.Values) == 0 {
		t.Fatal("no wings returned; the completion offers nothing and the caller invents a name")
	}

	// A prefix narrows, which is what makes it usable in a picker.
	narrowed, err := c.CompletePromptArgument(ctx, "am_hand_over", mcp.CompleteArgument{Name: "wing", Value: "wing_acme"}, mcp.CompleteContext{})
	if err != nil {
		t.Fatalf("complete with prefix: %v", err)
	}
	for _, v := range narrowed.Values {
		if !strings.HasPrefix(strings.ToLower(v), "wing_acme") {
			t.Errorf("prefix %q returned %q", "wing_acme", v)
		}
	}
	if len(narrowed.Values) >= len(all.Values) {
		t.Errorf("the prefix narrowed nothing: %d of %d", len(narrowed.Values), len(all.Values))
	}

	// Any other argument gets an empty completion rather than a guess: inventing
	// values for a field it knows nothing about is the failure mode, not the fix.
	other, err := c.CompletePromptArgument(ctx, "am_hand_over", mcp.CompleteArgument{Name: "finding", Value: "w"}, mcp.CompleteContext{})
	if err != nil {
		t.Fatalf("complete other: %v", err)
	}
	if len(other.Values) != 0 {
		t.Errorf("the completer invented %d values for an argument it cannot know: %v", len(other.Values), other.Values)
	}
}

// TestTheCompletionCapabilityIsBackedByAProvider is the arrow-reversed check
// this package already carries for tools.
//
// ⚠ ITS FIRST VERSION ONLY ASSERTED THAT wingCompleter SATISFIED THE INTERFACE,
// which proves nothing: New could declare WithCompletions and never pass the
// provider, leaving mcp-go's default to answer empty for everything. A client
// would see the capability, ask, and get silence — advertised and backed by
// nothing, the exact defect the tools gate exists for. So this drives the REAL
// server through a client and reads what comes back.
func TestTheCompletionCapabilityIsBackedByAProvider(t *testing.T) {
	gdb := graphTestDB(t)
	svc := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)
	const teamID = "team-wired"
	if _, err := svc.Add(context.Background(), teamID, palace.AddInput{
		Wing: "wing_acme", Room: "decisions", Content: "a memory so the wing exists",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := New(Deps{Version: "test", Drawers: svc})
	cli, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// The in-process transport carries no HTTP auth, so the tenant is put on the
	// context the way auth.Bridge would.
	ctx := auth.WithTenant(t.Context(), tenant.Tenant{TeamID: teamID, Role: tenant.RoleAdmin})
	res, err := cli.Complete(ctx, mcp.CompleteRequest{
		Params: mcp.CompleteParams{
			Ref:      mcp.PromptReference{Type: "ref/prompt", Name: "am_hand_over"},
			Argument: mcp.CompleteArgument{Name: "wing", Value: "wing_"},
		},
	})
	if err != nil {
		t.Fatalf("completion/complete through the real server: %v", err)
	}
	if len(res.Completion.Values) == 0 {
		t.Fatal("the server advertises completions and returned none — the provider is not wired, so a client asks and gets silence")
	}
	for _, v := range res.Completion.Values {
		if !strings.HasPrefix(v, "wing_") {
			t.Errorf("completion returned %q, which is not a wing from this palace", v)
		}
	}
}
