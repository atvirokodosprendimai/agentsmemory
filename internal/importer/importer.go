// Package importer is the HTTP ingest endpoint for migrating a whole mempalace
// into the agentsmemory SaaS. A user runs the read-only exporter against their
// local palace and streams the resulting NDJSON to POST /import with their
// project's Bearer token; this handler re-files every record under the resolved
// tenant and streams progress back.
//
// It is a separate surface from both the MCP server (agents, JSON-RPC) and the
// dashboard (humans, sessions): a migration is a bulk, long-running, token-
// authenticated data load, so it gets a dedicated streaming endpoint rather than
// being squeezed into a per-call MCP tool. Tenant resolution is identical to
// /mcp — the same OAuth/Bearer gate fronts both — so this handler only reads the
// tenant the gate already put on the request context.
package importer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
)

// batchSize is how many drawers/closets accumulate before one embed+store flush.
// It bounds the embed call (and memory) while keeping the migration moving in
// steady increments rather than one giant terminal write.
const batchSize = 64

// Drawers is the subset of palace.Service the importer needs. Declaring it at the
// consumer (Go's "accept interfaces" guidance) keeps the package decoupled from
// the concrete service and trivially fakeable in tests.
type Drawers interface {
	ImportDrawers(ctx context.Context, teamID string, in []palace.ImportDrawer) (int, error)
	ImportClosets(ctx context.Context, teamID string, in []palace.ImportCloset) (int, error)
	KGAdd(ctx context.Context, teamID, subject, predicate, object, validFrom, validTo, sourceCloset, sourceFile, sourceDrawerID string) (palace.KGAddResult, error)
	CreateTunnel(ctx context.Context, teamID string, in palace.TunnelInput, now string) (palace.Tunnel, error)
	RecomputeGraph(ctx context.Context, teamID, wing string, prune bool) (palace.RecomputeResult, error)
}

// Metering is the slice of usage.Service the importer uses: a migration is
// metered as ONE request (not one per drawer) so a single import cannot drain a
// month's quota, while an over-cap project is still refused.
type Metering interface {
	Allow(ctx context.Context, teamID string) (usage.Status, error)
}

// Handler builds the POST /import http.Handler. It must be mounted behind the
// same auth gate as /mcp so the Bearer token is resolved to a tenant on the
// request context before this runs.
func Handler(drawers Drawers, metering Metering) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, drawers, metering)
	})
}

// record is the flat NDJSON union: one JSON object per line, discriminated by
// Kind. Fields are shared across kinds where they mean the same thing (wing/room/
// source_file/entities/filed_at), so the exporter writes a small, obvious shape.
type record struct {
	Kind string `json:"kind"`

	// drawer (incl. diary) + closet
	Wing        string   `json:"wing"`
	Room        string   `json:"room"`
	SourceFile  string   `json:"source_file"`
	ChunkIndex  int      `json:"chunk_index"`
	Content     string   `json:"content"`
	Entities    []string `json:"entities"`
	FiledAt     string   `json:"filed_at"`
	ContentDate string   `json:"content_date"`
	Agent       string   `json:"agent"` // diary only
	Topic       string   `json:"topic"` // diary only

	// closet
	Document string `json:"document"`

	// kg fact
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	ValidFrom string `json:"valid_from"`
	ValidTo   string `json:"valid_to"`

	// explicit tunnel
	SourceWing string `json:"source_wing"`
	SourceRoom string `json:"source_room"`
	TargetWing string `json:"target_wing"`
	TargetRoom string `json:"target_room"`
	Label      string `json:"label"`

	// manifest (first line): the totals so the client can show a real progress bar
	Total int `json:"total"`
}

// progress is one streamed status line: running counts the client renders as it
// reads the response body. Done marks the final summary line.
type progress struct {
	Drawers   int    `json:"drawers"`
	Closets   int    `json:"closets"`
	KGFacts   int    `json:"kg_facts"`
	Tunnels   int    `json:"tunnels"`
	Skipped   int    `json:"skipped"`
	Total     int    `json:"total,omitempty"`
	Done      bool   `json:"done,omitempty"`
	Hallways  int    `json:"hallways,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// serve runs the import: authorize, meter once, then stream-decode the body and
// re-file each record, emitting progress lines as it goes.
func serve(w http.ResponseWriter, r *http.Request, drawers Drawers, metering Metering) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// The gate resolves the Bearer to a tenant; no tenant means no/!invalid token.
	t, ok := auth.TenantFrom(r.Context())
	if !ok {
		http.Error(w, "unauthenticated: present a valid Bearer token", http.StatusUnauthorized)
		return
	}
	// Meter the whole migration as a single request.
	st, err := metering.Allow(r.Context(), t.TeamID)
	if err != nil {
		http.Error(w, "usage metering failed", http.StatusInternalServerError)
		return
	}
	if !st.Allowed {
		http.Error(w, "monthly request cap reached — upgrade the project's plan", http.StatusTooManyRequests)
		return
	}

	// From here we commit to a streaming 200: record-level problems are reported
	// in the body, not via the status code (it is already sent).
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)

	imp := &runner{drawers: drawers, teamID: t.TeamID, w: w, start: time.Now()}
	if f, ok := w.(http.Flusher); ok {
		imp.flush = f.Flush
	}
	imp.run(r.Context(), r.Body)
}

// runner carries the per-request import state: the target tenant, the output
// writer, running counters, and the buffers that batch drawers/closets/tunnels.
type runner struct {
	drawers Drawers
	teamID  string
	w       io.Writer
	flush   func()
	start   time.Time

	total int // from the manifest, for the progress denominator
	p     progress

	pendingDrawers []palace.ImportDrawer
	pendingClosets []palace.ImportCloset
	pendingTunnels []record // applied last: CreateTunnel needs the rooms to exist
	wings          map[string]struct{}
}

// run decodes the NDJSON stream and dispatches each record by kind. It uses a
// json.Decoder reading successive values, so an arbitrarily large drawer content
// is handled without a line-length cap. A malformed value is fatal (a Decoder
// cannot reliably resync), but the exporter emits well-formed JSON, so in
// practice the stream completes; the partial import is idempotent, so a retry
// simply finishes it.
func (rn *runner) run(ctx context.Context, body io.Reader) {
	rn.wings = map[string]struct{}{}
	dec := json.NewDecoder(bufio.NewReaderSize(body, 1<<16))
	for {
		var rec record
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			rn.finish(ctx, err)
			return
		}
		rn.dispatch(ctx, rec)
	}
	rn.finish(ctx, nil)
}

// dispatch routes one record. Drawers and closets batch; KG facts apply inline
// (cheap, relational, no drawer dependency); tunnels are deferred to the end
// because CreateTunnel validates that both endpoint rooms already hold a drawer.
func (rn *runner) dispatch(ctx context.Context, rec record) {
	switch rec.Kind {
	case "manifest":
		rn.total = rec.Total
		rn.p.Total = rec.Total
	case "drawer", "diary":
		if w := rec.Wing; w != "" {
			rn.wings[w] = struct{}{}
		}
		rn.pendingDrawers = append(rn.pendingDrawers, palace.ImportDrawer{
			Wing: rec.Wing, Room: rec.Room, SourceFile: rec.SourceFile, ChunkIndex: rec.ChunkIndex,
			Content: rec.Content, Entities: rec.Entities, FiledAt: rec.FiledAt,
			ContentDate: rec.ContentDate, Agent: rec.Agent, Topic: rec.Topic,
		})
		if len(rn.pendingDrawers) >= batchSize {
			rn.flushDrawers(ctx)
		}
	case "closet":
		rn.pendingClosets = append(rn.pendingClosets, palace.ImportCloset{
			Wing: rec.Wing, Room: rec.Room, SourceFile: rec.SourceFile,
			Document: rec.Document, Entities: rec.Entities, FiledAt: rec.FiledAt,
		})
		if len(rn.pendingClosets) >= batchSize {
			rn.flushClosets(ctx)
		}
	case "kg":
		// confidence/source_drawer_id are not carried: KGAdd stamps confidence 1.0
		// and the drawer ids are re-minted on import, so a stale source ref would
		// not resolve. The temporal window (valid_from/valid_to) — the part that
		// matters for time-travel queries — is preserved.
		if _, err := rn.drawers.KGAdd(ctx, rn.teamID, rec.Subject, rec.Predicate, rec.Object,
			rec.ValidFrom, rec.ValidTo, "", rec.SourceFile, ""); err != nil {
			rn.p.Skipped++ // a malformed fact must not abort the migration
		} else {
			rn.p.KGFacts++
		}
		rn.maybeEmit()
	case "tunnel":
		rn.pendingTunnels = append(rn.pendingTunnels, rec)
	default:
		rn.p.Skipped++
	}
}

// flushDrawers embeds and stores the buffered drawers, then clears the buffer.
func (rn *runner) flushDrawers(ctx context.Context) {
	if len(rn.pendingDrawers) == 0 {
		return
	}
	n, err := rn.drawers.ImportDrawers(ctx, rn.teamID, rn.pendingDrawers)
	if err != nil {
		rn.p.Error = "drawer import: " + err.Error()
	}
	rn.p.Drawers += n
	rn.p.Skipped += len(rn.pendingDrawers) - n
	rn.pendingDrawers = rn.pendingDrawers[:0]
	rn.emit()
}

// flushClosets embeds and stores the buffered closets, then clears the buffer.
func (rn *runner) flushClosets(ctx context.Context) {
	if len(rn.pendingClosets) == 0 {
		return
	}
	n, err := rn.drawers.ImportClosets(ctx, rn.teamID, rn.pendingClosets)
	if err != nil {
		rn.p.Error = "closet import: " + err.Error()
	}
	rn.p.Closets += n
	rn.pendingClosets = rn.pendingClosets[:0]
	rn.emit()
}

// applyTunnels creates the deferred explicit tunnels now that every drawer is
// stored (so the endpoint rooms exist). A tunnel into a room that never received
// a drawer is skipped, not fatal.
func (rn *runner) applyTunnels(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, rec := range rn.pendingTunnels {
		_, err := rn.drawers.CreateTunnel(ctx, rn.teamID, palace.TunnelInput{
			SourceWing: rec.SourceWing, SourceRoom: rec.SourceRoom,
			TargetWing: rec.TargetWing, TargetRoom: rec.TargetRoom, Label: rec.Label,
		}, now)
		if err != nil {
			rn.p.Skipped++
		} else {
			rn.p.Tunnels++
		}
	}
	rn.pendingTunnels = nil
}

// finish drains the buffers, applies tunnels, rebuilds the derived graph, and
// writes the final summary line. fatal, when non-nil, is a stream decode error
// that ended ingestion early; the partial import still stands (and is idempotent).
func (rn *runner) finish(ctx context.Context, fatal error) {
	rn.flushDrawers(ctx)
	rn.flushClosets(ctx)
	rn.applyTunnels(ctx)

	// Hallways and entity/topic tunnels are derived, so they are rebuilt from the
	// imported drawers rather than carried over the wire. Best-effort: a recompute
	// failure does not fail an otherwise-good migration.
	if res, err := rn.drawers.RecomputeGraph(ctx, rn.teamID, "", false); err == nil {
		rn.p.Hallways = res.Hallways
	}

	rn.p.Done = true
	rn.p.ElapsedMs = time.Since(rn.start).Milliseconds()
	if fatal != nil && rn.p.Error == "" {
		rn.p.Error = "stream ended early: " + fatal.Error()
	}
	rn.emit()
}

// progressEvery throttles inline progress lines (KG facts) so a long fact stream
// does not write a line per fact.
const progressEvery = 200

// maybeEmit writes a progress line only every progressEvery records, keeping the
// inline (per-fact) path from flooding the response.
func (rn *runner) maybeEmit() {
	if (rn.p.Drawers+rn.p.Closets+rn.p.KGFacts)%progressEvery == 0 {
		rn.emit()
	}
}

// emit writes one progress line and flushes it so the client sees streaming
// progress rather than a buffered dump at the end.
func (rn *runner) emit() {
	b, err := json.Marshal(rn.p)
	if err != nil {
		return
	}
	_, _ = rn.w.Write(append(b, '\n'))
	if rn.flush != nil {
		rn.flush()
	}
}
