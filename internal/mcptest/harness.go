// Package mcptest stands a real agentsmemory MCP server up in a test and drives
// it with a real MCP client.
//
// It exists because of a gap this repository measured on 2026-08-20: 41 tools
// registered, 39 named in no test file, and no test anywhere that drove a tool
// handler. The palace services underneath were well covered; the surface every
// agent actually calls was not covered at all — and those are not the same
// thing, as four shipped-but-unreachable capabilities in this tree already
// demonstrate.
//
// Two design choices are load-bearing.
//
// It goes through the TRANSPORT rather than calling handlers directly. admit(),
// usage metering, tenant resolution and argument decoding all live between the
// wire and the handler, and three of the defects found by hand this week lived
// in that layer. A harness that calls the closure directly proves something
// about a path nobody runs.
//
// It is hermetic: migrated SQLite, a deterministic fake embedder, no network and
// no model. A gate that needs a compose stack does not run in the loop where
// defects are introduced, and a gate that does not run there is one people learn
// to work around.
package mcptest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver"
	"github.com/atvirokodosprendimai/agentsmemory/internal/oauth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skillset"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/glebarez/sqlite"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// fakeDim matches the palace's test embedder so a migrated schema and a seeded
// vector index agree on width.
const fakeDim = 8

// teamHeader is how a harness client states which workspace it is, standing in
// for the bearer token the real gate resolves.
const teamHeader = "X-Mcptest-Team"

// roleHeader lets one scenario dial as a least-privileged member while the rest
// stay admin. The role is resolved per request from a header for the same reason
// the workspace is: pinning one role for the process makes "a member and an admin
// on one server" inexpressible, which is exactly the shape the missing write
// authorization had.
const roleHeader = "X-Mcptest-Role"

// TeamID is the workspace a harness client authenticates as by default.
//
// OtherTeamID is a SECOND workspace over the same database, and it exists
// because a review pointed out that one team by construction makes a whole class
// of defect unobservable: with a single tenant, no assertion can tell "scoped to
// my workspace" from "scoped to the whole database", and dropping the team
// filter from a repository query leaves every scenario green.
const (
	TeamID      = "team-mcptest"
	OtherTeamID = "team-mcptest-other"
)

// fakeEmbedder is deterministic and content-derived: two identical texts embed
// identically and different texts do not, which is all a round-trip scenario
// needs. It is not a model and makes no claim to rank like one.
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, s := range inputs {
		v := make([]float32, fakeDim)
		for j, r := range s {
			v[j%fakeDim] += float32(r%17) / 16
		}
		out[i] = v
	}
	return out, nil
}

func (f fakeEmbedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	v, err := f.Embed(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

// caps grants every workspace an effectively unlimited monthly allowance. The
// cap path itself is metered and exercised — admit() still calls Allow — but a
// scenario failing because it was the 501st call would be a test about the cap,
// not about the tool.
type caps struct{}

func (caps) MonthlyCap(_ context.Context, _ string) (int, error) { return -1, nil }

// Harness is a running server, a client that speaks to it, and the palace
// underneath. Wing is the default wing this client's registration declares,
// which is what makes two-party scoping observable.
type Harness struct {
	Drawers *palace.Service
	Wing    string
	Team    string

	cli *client.Client
	srv *httptest.Server

	// called records every tool this harness invoked, in order.
	//
	// It exists so coverage is MEASURED rather than declared. A scenario that
	// lists the tools it covers is a list kept beside the truth, and this repo
	// has been bitten by exactly that twice this week — an exclusion list keyed
	// by arm name, and a registration gate that scanned only const declarations.
	// Recording the calls means a scenario cannot claim a tool it never invoked.
	called []string
}

// Hosted is a real hosted MCP edge over a migrated database. Unlike the normal
// scenario harness it does not synthesize identity headers: clients must present
// a raw project token or an OAuth access token to cross AuthServer.Gate.
type Hosted struct {
	URL     string
	Tenants *tenant.Repo
	Usage   *usage.Service

	drawers *palace.Service
	db      *gorm.DB
	srv     *httptest.Server
}

// Called returns the tools this harness invoked, in order. The exhaustiveness
// gate reads it; a scenario's own assertions do not need it.
func (h *Harness) Called() []string { return append([]string(nil), h.called...) }

// Endpoint returns the live MCP URL used by this harness. It lets a command
// test dial through its own production transport code instead of borrowing the
// harness's private client.
func (h *Harness) Endpoint() string { return h.srv.URL }

// New returns a harness whose client authenticates as TeamID with no default
// wing, mirroring a registration that named no project.
func New(t *testing.T) *Harness { return NewWithWing(t, "") }

// NewWithWing returns a harness whose registration declares wing. Two harnesses
// over one database with different wings are how a scenario observes scoping
// from both sides.
func NewWithWing(t *testing.T, wing string) *Harness {
	t.Helper()
	return newOn(t, openDB(t, filepath.Join(t.TempDir(), "mcptest.db")), wing)
}

// NewLocalWithWing returns a harness mounted behind the production local-mode
// tenant middleware. Use it for local-only tools: setting Deps.Local registers
// those tools, while this constructor additionally proves the HTTP edge injects
// the fixed local administrator that makes their handlers reachable.
func NewLocalWithWing(t *testing.T, wing string) *Harness {
	t.Helper()
	gdb := openDB(t, filepath.Join(t.TempDir(), "mcptest.db"))
	srv, drawers := newLocalServer(t, gdb)
	return newClient(t, srv, drawers, wing)
}

// NewHosted stands up the production hosted authentication chain: bearer
// resolution, OAuth challenge/endpoints, MCP HTTP transport, Bridge, admission,
// and handlers. Tests seed workspaces through Tenants and connect through Client.
func NewHosted(t *testing.T) *Hosted {
	t.Helper()
	gdb := openDB(t, filepath.Join(t.TempDir(), "mcptest-hosted.db"))
	tenants := tenant.NewRepo(gdb, tenant.WithTokenSecret("mcptest-token-at-rest-secret"))
	usageSvc := usage.NewService(usage.NewRepo(gdb), tenants)
	stream, drawers := newStreamWith(gdb, usageSvc, false)
	sealer, err := oauth.NewSealer("mcptest-oauth-signing-secret")
	if err != nil {
		t.Fatalf("oauth sealer: %v", err)
	}
	var root http.Handler
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root.ServeHTTP(w, r)
	}))
	issuer := "http://" + srv.Listener.Addr().String()
	authSrv := oauth.NewAuthServer(issuer, sealer, tenants, tenants)

	mux := http.NewServeMux()
	mux.Handle("/mcp", authSrv.Gate(stream))
	mux.HandleFunc("/authorize", authSrv.Authorize)
	mux.HandleFunc("/token", authSrv.Token)
	mux.HandleFunc("/.well-known/oauth-protected-resource", authSrv.ProtectedResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server", authSrv.AuthorizationServerMetadata)
	root = mux
	srv.Start()
	t.Cleanup(srv.Close)
	return &Hosted{URL: srv.URL, Tenants: tenants, Usage: usageSvc, drawers: drawers, db: gdb, srv: srv}
}

// DurableStateDigest returns a deterministic digest of every durable
// application table in the hosted harness. Tests use it to prove a refused call
// did not mutate some table they forgot to put in a hand-maintained ledger.
//
// api_keys.last_used_at is deliberately normalised: resolving any valid bearer
// stamps that observation field before the MCP role guard runs. It records that
// authentication happened, not a change to stored memory. usage_counters is
// likewise operational telemetry and is asserted independently by callers. All
// other application rows and schemas participate in the digest.
func (h *Hosted) DurableStateDigest(ctx context.Context) ([sha256.Size]byte, error) {
	var empty [sha256.Size]byte
	sqlDB, err := h.db.DB()
	if err != nil {
		return empty, fmt.Errorf("open hosted state snapshot: %w", err)
	}
	tx, err := sqlDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return empty, fmt.Errorf("begin hosted state snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	type tableSchema struct {
		name string
		sql  string
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT name, COALESCE(sql, '')
		FROM sqlite_schema
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name <> 'usage_counters'
		ORDER BY name`)
	if err != nil {
		return empty, fmt.Errorf("list hosted state tables: %w", err)
	}
	var tables []tableSchema
	for rows.Next() {
		var table tableSchema
		if err := rows.Scan(&table.name, &table.sql); err != nil {
			_ = rows.Close()
			return empty, fmt.Errorf("scan hosted state table: %w", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return empty, fmt.Errorf("iterate hosted state tables: %w", err)
	}
	if err := rows.Close(); err != nil {
		return empty, fmt.Errorf("close hosted state tables: %w", err)
	}

	digest := sha256.New()
	writeJSON := func(value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, _ = digest.Write(encoded)
		_, _ = digest.Write([]byte{'\n'})
		return nil
	}
	for _, table := range tables {
		if err := writeJSON([]string{table.name, table.sql}); err != nil {
			return empty, fmt.Errorf("digest hosted table %s schema: %w", table.name, err)
		}

		columnRows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table.name)
		if err != nil {
			return empty, fmt.Errorf("list hosted table %s columns: %w", table.name, err)
		}
		var columns []string
		for columnRows.Next() {
			var column string
			if err := columnRows.Scan(&column); err != nil {
				_ = columnRows.Close()
				return empty, fmt.Errorf("scan hosted table %s column: %w", table.name, err)
			}
			columns = append(columns, column)
		}
		if err := columnRows.Err(); err != nil {
			_ = columnRows.Close()
			return empty, fmt.Errorf("iterate hosted table %s columns: %w", table.name, err)
		}
		if err := columnRows.Close(); err != nil {
			return empty, fmt.Errorf("close hosted table %s columns: %w", table.name, err)
		}
		if len(columns) == 0 {
			return empty, fmt.Errorf("hosted table %s has no columns", table.name)
		}

		selects := make([]string, len(columns))
		order := make([]string, len(columns))
		for i, column := range columns {
			quoted := quoteSQLiteIdentifier(column)
			selects[i] = quoted
			if table.name == "api_keys" && column == "last_used_at" {
				selects[i] = "NULL AS " + quoted
			}
			order[i] = quoted
		}
		query := "SELECT " + strings.Join(selects, ", ") +
			" FROM " + quoteSQLiteIdentifier(table.name) +
			" ORDER BY " + strings.Join(order, ", ")
		dataRows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return empty, fmt.Errorf("read hosted table %s: %w", table.name, err)
		}
		for dataRows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for i := range values {
				destinations[i] = &values[i]
			}
			if err := dataRows.Scan(destinations...); err != nil {
				_ = dataRows.Close()
				return empty, fmt.Errorf("scan hosted table %s row: %w", table.name, err)
			}
			if err := writeJSON(values); err != nil {
				_ = dataRows.Close()
				return empty, fmt.Errorf("digest hosted table %s row: %w", table.name, err)
			}
		}
		if err := dataRows.Err(); err != nil {
			_ = dataRows.Close()
			return empty, fmt.Errorf("iterate hosted table %s: %w", table.name, err)
		}
		if err := dataRows.Close(); err != nil {
			return empty, fmt.Errorf("close hosted table %s: %w", table.name, err)
		}
	}

	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func quoteSQLiteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// Client dials the hosted /mcp endpoint with the supplied bearer and registration
// wing. Successful construction proves the credential crossed AuthServer.Gate.
func (h *Hosted) Client(t *testing.T, wing, bearer string) *Harness {
	t.Helper()
	headers := map[string]string{"Authorization": "Bearer " + bearer}
	if wing != "" {
		headers[mcpprotocol.WingHeader] = wing
	}
	return newClientWithHeaders(t, h.srv, h.URL+"/mcp", h.drawers, wing, "", headers)
}

// Parties returns one client per wing, all over ONE database, so what one writes
// the others can be asked to find.
//
// Two parties prove scoping, and only as a pair: a test showing that A's wing is
// visible to B passes with scoping removed entirely, so the claim needs its
// negative beside it. A third party is what turns "delivered" into "delivered to
// the right place" — the handoff defect this repo shipped was invisible because
// nobody ever looked from the recipient's side, let alone a bystander's.
func Parties(t *testing.T, wings ...string) []*Harness {
	t.Helper()
	gdb := openDB(t, filepath.Join(t.TempDir(), "mcptest.db"))
	// ONE server, N clients. An earlier version built a server per party and
	// shared only the database, which a review caught: production runs one
	// process serving everybody, so per-process state that leaks between clients
	// — a cached search key that omits the wing, a latched "current wing" field —
	// is invisible when each client has a server to itself. Each party would
	// latch its own correct wing and isolation would look perfect while the real
	// deployment served B as A.
	srv, drawers := newServer(t, gdb)
	out := make([]*Harness, len(wings))
	for i, w := range wings {
		out[i] = newClient(t, srv, drawers, w)
	}
	return out
}

// Tenants returns two clients on ONE server and ONE database belonging to
// DIFFERENT workspaces — the outer trust boundary, which wings sit inside.
func Tenants(t *testing.T, wing string) (*Harness, *Harness) {
	t.Helper()
	gdb := openDB(t, filepath.Join(t.TempDir(), "mcptest.db"))
	srv, drawers := newServer(t, gdb)
	return newClientAs(t, srv, drawers, wing, TeamID),
		newClientAs(t, srv, drawers, wing, OtherTeamID)
}

// Pair is Parties for the common two-sided case.
func Pair(t *testing.T, wingA, wingB string) (*Harness, *Harness) {
	t.Helper()
	p := Parties(t, wingA, wingB)
	return p[0], p[1]
}

func openDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
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
	return gdb
}

func newOn(t *testing.T, gdb *gorm.DB, wing string) *Harness {
	t.Helper()
	srv, drawers := newServer(t, gdb)
	return newClient(t, srv, drawers, wing)
}

// newServer builds one MCP server over one palace, exactly as the process does.
func newServer(t *testing.T, gdb *gorm.DB) (*httptest.Server, *palace.Service) {
	t.Helper()
	stream, drawers := newStream(gdb)

	// The OAuth gate's job, minus OAuth: put a resolved tenant on the request
	// context so auth.Bridge can forward it. Token validation is exercised by
	// internal/oauth; standing it up here would test that package again and add
	// a way for every scenario to fail for an unrelated reason.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Resolved PER REQUEST from a header, as the real gate resolves a bearer.
		// Pinning one workspace for the process would have made two tenants on one
		// server impossible to express, which is how the missing team filter hid.
		team := r.Header.Get(teamHeader)
		if team == "" {
			team = TeamID
		}
		role := tenant.Role(r.Header.Get(roleHeader))
		if role == "" {
			role = tenant.RoleAdmin
		}
		ctx := auth.WithTenant(r.Context(), tenant.Tenant{
			TeamID: team, UserID: "user-mcptest", Role: role,
		})
		stream.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)
	return srv, drawers
}

// newLocalServer mounts the same MCP stream behind the production local tenant
// middleware, including its credential-free loopback semantics.
func newLocalServer(t *testing.T, gdb *gorm.DB) (*httptest.Server, *palace.Service) {
	t.Helper()
	stream, drawers := newStream(gdb)
	local := auth.LocalTenant(tenant.Tenant{
		TeamID: TeamID, UserID: "user-mcptest-local", Role: tenant.RoleAdmin,
	}, "")(stream)
	srv := httptest.NewServer(local)
	t.Cleanup(srv.Close)
	return srv, drawers
}

// newStream constructs the production MCP registration and HTTP bridge shared
// by the hosted-edge substitute and the real local-mode edge.
func newStream(gdb *gorm.DB) (http.Handler, *palace.Service) {
	return newStreamWith(gdb, usage.NewService(usage.NewRepo(gdb), caps{}), true)
}

// newStreamWith constructs the production MCP registration and HTTP bridge with
// the deployment's real usage policy and local/hosted surface selection.
func newStreamWith(gdb *gorm.DB, usageSvc *usage.Service, local bool) (http.Handler, *palace.Service) {

	drawers := palace.NewService(palace.NewRepo(gdb), fakeEmbedder{}, sqlitevec.New(gdb), fakeDim)
	mcpSrv := mcpserver.Compose(mcpserver.Deps{
		Skills:   skill.NewService(skill.NewRepo(gdb)),
		Skillset: skillset.NewService(skillset.NewRepo(gdb)),
		Usage:    usageSvc,
		Drawers:  drawers,
		// Wing-scoped reads are the production default (config.SearchScope), and
		// a harness that quietly widened them would prove scoping works while
		// testing a configuration nobody runs.
		ScopeSearchToWing: config.Default().ScopeSearchToWing(),
		Local:             local,
		Workspaces:        nil,
		// The harness deliberately names a version the production resolver can
		// never produce, so a probe that reads one back knows it is talking to a
		// test server rather than to a build someone shipped.
		Version: "test",
	})

	return mcpserver.StreamHTTP(mcpSrv), drawers
}

// AsRole stands up a server and dials it as a registration whose caller holds the
// named role, so a scenario can assert what a least-privileged member may do.
func AsRole(t *testing.T, role tenant.Role) *Harness {
	t.Helper()
	gdb := openDB(t, filepath.Join(t.TempDir(), "palace.db"))
	srv, drawers := newServer(t, gdb)
	// A wing is set for the same reason the other constructors set one: a write
	// without it is refused for lacking a wing, and a role scenario whose refusal
	// comes from the wrong guard proves nothing about roles.
	return newClientRole(t, srv, drawers, "wing_roles", TeamID, role)
}

// newClient dials an existing server as one registration.
func newClient(t *testing.T, srv *httptest.Server, drawers *palace.Service, wing string) *Harness {
	t.Helper()
	return newClientAs(t, srv, drawers, wing, TeamID)
}

// newClientAs dials as a named workspace, so a scenario can put two tenants on
// one server and one database.
func newClientAs(t *testing.T, srv *httptest.Server, drawers *palace.Service, wing, team string) *Harness {
	t.Helper()
	return newClientRole(t, srv, drawers, wing, team, "")
}

// newClientRole is newClientAs plus the caller's role. An empty role leaves the
// header off, which the server reads as admin — so every existing scenario keeps
// the privileges it was written with.
func newClientRole(t *testing.T, srv *httptest.Server, drawers *palace.Service, wing, team string, role tenant.Role) *Harness {
	t.Helper()

	// The wing rides on the registration as a header, exactly as `install` writes
	// it — see mcpprotocol.WingHeader. A harness that stored the wing without sending it
	// would show every registration as unscoped, and the first version of this
	// file did: the positive half of the scoping pair passed and the negative half
	// caught it, which is why the pair exists.
	headers := map[string]string{teamHeader: team}
	if wing != "" {
		headers[mcpprotocol.WingHeader] = wing
	}
	if role != "" {
		headers[roleHeader] = string(role)
	}
	return newClientWithHeaders(t, srv, srv.URL, drawers, wing, team, headers)
}

func newClientWithHeaders(t *testing.T, srv *httptest.Server, endpoint string, drawers *palace.Service, wing, team string, headers map[string]string) *Harness {
	t.Helper()
	opts := []transport.StreamableHTTPCOption{transport.WithHTTPHeaders(headers)}
	cli, err := client.NewStreamableHttpClient(endpoint, opts...)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if err := cli.Start(context.Background()); err != nil {
		t.Fatalf("start client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	if _, err := cli.Initialize(context.Background(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	h := &Harness{Drawers: drawers, Wing: wing, Team: team, cli: cli, srv: srv}

	// A harness serving an empty catalogue would let every scenario pass by
	// calling nothing, so it refuses to be handed out in that state.
	if err := UsableCatalogue(h.ListTools(t)); err != nil {
		t.Fatalf("harness: %v", err)
	}
	return h
}

// usableCatalogue rejects a catalogue that cannot support a scenario.
//
// It is a function taking the result rather than a check inlined at the call
// site, for a reason worth stating: no test can stand up a toolless server, so
// an inlined guard is unfalsifiable — disarming it leaves the suite green, which
// was measured before this was extracted. Given the list, the rule is drivable.
func UsableCatalogue(tools []string, err error) error {
	if err != nil {
		return fmt.Errorf("could not list tools: %w", err)
	}
	if len(tools) == 0 {
		return errors.New("served no tools — every scenario built on this harness would pass vacuously")
	}
	return nil
}

// ListToolDefinitions returns the tool schemas the running server advertises.
// Tests that audit a class of tools use the schemas to discover that class,
// rather than keeping a second list that drifts when a tool is added.
func (h *Harness) ListToolDefinitions(t *testing.T) ([]mcp.Tool, error) {
	t.Helper()
	res, err := h.cli.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	return res.Tools, nil
}

// ListCatalog returns the running server's self-described tool catalogue. It
// comes through am_skillset rather than a package-level registration helper so
// audits exercise the same write/read classification an agent actually sees.
func (h *Harness) ListCatalog(t *testing.T) ([]mcpserver.CatalogEntry, error) {
	t.Helper()
	out, isErr, err := h.Call(t, "am_skillset", map[string]any{})
	if err != nil {
		return nil, err
	}
	if isErr {
		return nil, fmt.Errorf("am_skillset reported an error: %s", out)
	}
	var payload struct {
		Tools []mcpserver.CatalogEntry `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return nil, fmt.Errorf("decode am_skillset catalogue: %w", err)
	}
	return payload.Tools, nil
}

// ListTools returns the tool names the running server advertises.
func (h *Harness) ListTools(t *testing.T) ([]string, error) {
	t.Helper()
	tools, err := h.ListToolDefinitions(t)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names, nil
}

// Call invokes a tool and returns its text content plus whether the tool
// reported an error. A tool error is a RESULT, not a transport failure, so it is
// returned rather than failing the test: refusing is correct behaviour for
// several tools and a scenario needs to assert it.
func (h *Harness) Call(t *testing.T, name string, args map[string]any) (string, bool, error) {
	t.Helper()
	h.called = append(h.called, name)
	res, err := mcpcli.Call(context.Background(), h.cli.CallTool, name, args)
	if err != nil {
		return "", false, err
	}
	var sb string
	for _, c := range res.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			sb += tc.Text
		}
	}
	return sb, res.IsError, nil
}

// MustCall calls a tool and fails the test unless it succeeded.
func (h *Harness) MustCall(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	out, isErr, err := h.Call(t, name, args)
	if err != nil {
		t.Fatalf("%s: transport: %v", name, err)
	}
	if isErr {
		t.Fatalf("%s: tool reported an error: %s", name, out)
	}
	return out
}

// MustRefuse calls a tool and fails unless it reported an error, returning the
// message so a scenario can assert WHAT it refused rather than only that it did.
func (h *Harness) MustRefuse(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	out, isErr, err := h.Call(t, name, args)
	if err != nil {
		t.Fatalf("%s: transport: %v", name, err)
	}
	if !isErr {
		t.Fatalf("%s: expected a refusal, got success: %s", name, out)
	}
	return out
}

// JSON decodes a tool's text payload, for scenarios asserting structure rather
// than substrings.
func (h *Harness) JSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("decode tool output: %v\n%s", err, out)
	}
	return m
}
