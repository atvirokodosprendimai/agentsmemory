package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/glebarez/sqlite"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// fakeGraph answers the two questions the note asks without a database, so these
// tests drive the DECISION rather than the storage under it. The two lookups
// fail independently because fail-open has to hold for each: a version that
// swallows one error and propagates the other still turns a working call into a
// warning about the palace's own health.
type fakeGraph struct {
	hallways []palace.Hallway
	drawers  []palace.Drawer
	hallErr  error
	listErr  error
}

func (f fakeGraph) ListHallways(_ context.Context, _, _ string) ([]palace.Hallway, error) {
	if f.hallErr != nil {
		return nil, f.hallErr
	}
	return f.hallways, nil
}

func (f fakeGraph) List(_ context.Context, _, _, _ string, _, _ int) ([]palace.Drawer, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.drawers, nil
}

// TestEmptyGraphSaysWhyItIsEmpty: three states produce an empty graph and each
// needs a different action from the reader, so one message for all three is a
// message that tells nobody what to do.
func TestEmptyGraphSaysWhyItIsEmpty(t *testing.T) {
	cases := []struct {
		name  string
		graph fakeGraph
		// want are fragments the note must carry: what is wrong, and the thing
		// that would change it.
		want []string
		// forbid are fragments it must NOT carry. A note that explains an
		// emptiness with a cause that has since been FIXED is worse than no note:
		// it sends the reader to change something that is already right. This one
		// blamed am_add_drawer for stamping no entities, which was true when it
		// was written and false within the day.
		forbid []string
	}{
		{
			name:  "nothing has been filed here at all",
			graph: fakeGraph{},
			want:  []string{"holds no memories"},
		},
		{
			name: "memories are filed but none carries an entity",
			graph: fakeGraph{drawers: []palace.Drawer{
				{ID: "d1", Wing: "wing_acme", Room: "decisions", Content: "a memory an agent filed"},
			}},
			want: []string{"carries an entity", "am_recompute_graph"},
			// And it must NOT repeat the cause that was true when this note was
			// written and false within the day: every write path stamps entities
			// now, so blaming am_add_drawer sends the reader to change something
			// already correct.
			forbid: []string{"am_add_drawer carries none", "no derivable graph at all"},
		},
		{
			name: "entities are there but no pair ever meets",
			graph: fakeGraph{drawers: []palace.Drawer{
				{ID: "d1", Wing: "wing_acme", Room: "decisions", Entities: []string{"Raft"}},
			}},
			want: []string{"co-occur", "am_recompute_graph"},
		},
	}

	notes := make([]string, len(cases))
	for i, tc := range cases {
		note := emptyGraphNote(context.Background(), tc.graph, "team", "wing_acme")
		if note == "" {
			t.Fatalf("%s: produced no note — an empty result is byte-identical to a graph that "+
				"can never have one, so the reader concludes the graph is empty and stops asking", tc.name)
		}
		for _, want := range tc.want {
			if !strings.Contains(note, want) {
				t.Errorf("%s: the note does not name %q, so the reader is not told what would change it: %q",
					tc.name, want, note)
			}
		}
		for _, bad := range tc.forbid {
			if strings.Contains(note, bad) {
				t.Errorf("%s: the note still carries %q, a cause that has since been fixed — it sends "+
					"the reader to change something already correct: %q", tc.name, bad, note)
			}
		}
		notes[i] = note
	}

	// The three cases need three different actions — file something, mine or wire
	// the extractor, run a recompute. Collapsing them into one message hides which
	// one the reader is in, which is the whole failure this note exists to close.
	for i := range notes {
		for j := i + 1; j < len(notes); j++ {
			if notes[i] == notes[j] {
				t.Errorf("%q and %q produce the identical note, so the reader cannot tell which "+
					"of the two they are in:\n  %s", cases[i].name, cases[j].name, notes[i])
			}
		}
	}
}

// TestGraphNoteIsSilentWhenTheGraphHasContent: a warning on every call is a
// warning nobody reads. The note explains an EMPTY derived graph and must
// disappear the moment one hallway exists.
func TestGraphNoteIsSilentWhenTheGraphHasContent(t *testing.T) {
	g := fakeGraph{
		hallways: []palace.Hallway{{
			ID: "hallway_1", Wing: "wing_acme", EntityA: "Quorum", EntityB: "Raft", CoOccurrence: 3,
		}},
		drawers: []palace.Drawer{{ID: "d1", Wing: "wing_acme", Entities: []string{"Raft", "Quorum"}}},
	}
	if note := emptyGraphNote(context.Background(), g, "team", "wing_acme"); note != "" {
		t.Errorf("a wing whose graph holds a hallway produced a note: %q", note)
	}
	// And team-wide, which is the scope traverse and graph_stats ask about.
	if note := emptyGraphNote(context.Background(), g, "team", ""); note != "" {
		t.Errorf("a palace whose graph holds a hallway produced a note: %q", note)
	}
}

// TestGraphNoteFailsOpen: a lookup failure must never turn a working call into an
// error, nor into a warning about the palace's own health. The note is a
// diagnostic, and a diagnostic that breaks the tool it diagnoses is worse than
// none at all.
func TestGraphNoteFailsOpen(t *testing.T) {
	for _, tc := range []struct {
		name  string
		graph fakeGraph
	}{
		{"the hallway lookup fails", fakeGraph{hallErr: errors.New("db down")}},
		{"the drawer lookup fails", fakeGraph{listErr: errors.New("db down")}},
		{"both fail", fakeGraph{hallErr: errors.New("db down"), listErr: errors.New("db down")}},
	} {
		if note := emptyGraphNote(context.Background(), tc.graph, "team", "wing_acme"); note != "" {
			t.Errorf("%s: produced a note %q — a failed lookup must leave the call exactly as it was",
				tc.name, note)
		}
	}
}

// TestEveryGraphToolCarriesTheNote drives the three REGISTERED handlers rather
// than the function, because the function is correct whether or not anything
// attaches its output. Attaching a diagnostic to one tool and forgetting the
// others is this repository's recurring shape: four capabilities shipped
// finished and unreachable in one week, every one of them tested.
func TestEveryGraphToolCarriesTheNote(t *testing.T) {
	srv := graphToolServer(t)
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{
		TeamID: graphTestTeam, UserID: "u1", Role: tenant.RoleAdmin,
	})

	for _, tc := range []struct {
		tool string
		args map[string]any
		// keeps are TOP-LEVEL wire fields the note must not have displaced.
		// graph_stats gained the note by embedding palace.GraphStats in a view,
		// and an embedded field that stops being anonymous nests every metric
		// under a new key — the JSON still CONTAINS "total_rooms", so only
		// decoding the object catches it.
		keeps []string
	}{
		{
			tool:  mcpprotocol.ToolPrefix + "list_hallways",
			args:  map[string]any{"wing": graphTestWing},
			keeps: []string{"hallways", "count"},
		},
		// traverse and graph_stats name no wing: they answer for the whole palace.
		{
			tool:  mcpprotocol.ToolPrefix + "traverse",
			args:  map[string]any{"start_room": graphTestRoom},
			keeps: []string{"nodes", "count"},
		},
		{
			tool:  mcpprotocol.ToolPrefix + "graph_stats",
			args:  map[string]any{},
			keeps: []string{"total_rooms", "tunnel_rooms", "total_edges", "rooms_per_wing"},
		},
	} {
		st := srv.GetTool(tc.tool)
		if st == nil {
			t.Fatalf("%s is not registered — this check has stopped checking anything", tc.tool)
		}
		res, err := st.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: tc.tool, Arguments: tc.args},
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.tool, err)
		}
		body := errText(res)
		if res.IsError {
			t.Fatalf("%s returned an error result: %s", tc.tool, body)
		}
		if !strings.Contains(body, "carries an entity") {
			t.Errorf("%s carries a note that does not name the reason the graph is empty:\n  %s",
				tc.tool, body)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &fields); err != nil {
			t.Fatalf("%s did not return a JSON object: %v\n  %s", tc.tool, err, body)
		}
		if _, ok := fields["note"]; !ok {
			t.Errorf("%s carries no note on a palace whose derived graph is empty:\n  %s\n"+
				"  Its result is byte-identical to a working tool with nothing to report, so an "+
				"agent concludes the graph is empty and stops asking.", tc.tool, body)
		}
		for _, keep := range tc.keeps {
			if _, ok := fields[keep]; !ok {
				t.Errorf("%s no longer carries %q at the TOP level — adding the note moved a field "+
					"every caller already parses:\n  %s", tc.tool, keep, body)
			}
		}
	}
}

// The harness below stands the graph tools up over a real migrated palace in the
// state the ADR measured: memories filed through the agent write path, none of
// which carries an entity, so the derived graph cannot exist.
const (
	graphTestTeam = "team-emptygraph"
	graphTestWing = "wing_acme"
	graphTestRoom = "decisions"
	// graphTestDim is the fake embedder's width; the migrated vector table and
	// the seeded points have to agree on it.
	graphTestDim = 8
)

// graphToolServer registers the real graph tools over a seeded palace and returns
// the server the registered handlers are pulled off.
func graphToolServer(t *testing.T) *server.MCPServer {
	t.Helper()
	gdb := graphTestDB(t)
	drawers := palace.NewService(palace.NewRepo(gdb, gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)

	for _, content := range []string{
		"the rerank pool is read from the environment, and the compose file advertised one nobody read",
		"a wing comes into existence when something is first written to it",
	} {
		if _, err := drawers.Add(context.Background(), graphTestTeam, palace.AddInput{
			Wing: graphTestWing, Room: graphTestRoom, Content: content,
		}); err != nil {
			t.Fatalf("seed drawer: %v", err)
		}
	}

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	registerGraph(&registrar{srv: srv}, drawers, usage.NewService(usage.NewRepo(gdb), graphTestCaps{}), false)
	return srv
}

// graphTestDB opens a migrated throwaway SQLite palace.
func graphTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "emptygraph.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// ⚠ CLOSED, and the leak it prevents is INVISIBLE ON THIS PLATFORM.
	// POSIX unlinks a file with an open descriptor happily, so an unclosed
	// handle produces no signal on Linux or macOS. Windows refuses the unlink,
	// and t.TempDir registers its RemoveAll at call time — so the leak surfaces
	// there as a cleanup failure in tests whose assertions all passed, 40 of
	// them, none for a reason to do with what they assert (#162). Cleanup runs
	// LIFO, so this is registered after TempDir's and therefore runs before it.
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb
}

// graphTestEmbedder is deterministic and content-derived. Nothing here searches;
// the write path simply needs a vector to store.
type graphTestEmbedder struct{}

// Embed returns one deterministic vector per input.
func (graphTestEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, s := range inputs {
		v := make([]float32, graphTestDim)
		for j, r := range s {
			v[j%graphTestDim] += float32(r%17) / 16
		}
		out[i] = v
	}
	return out, nil
}

// EmbedOne returns the single-input case of Embed.
func (e graphTestEmbedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	v, err := e.Embed(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

// graphTestCaps leaves the meter unlimited: admit() still runs, but a call
// failing on a quota would be a test about the quota.
type graphTestCaps struct{}

// MonthlyCap reports no ceiling.
func (graphTestCaps) MonthlyCap(_ context.Context, _ string) (int, error) { return -1, nil }
