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
//
// Transport shape: the handler reads the WHOLE request body before it writes a
// byte of the response (no full-duplex streaming). A CDN/reverse proxy in front
// of the SaaS — Cloudflare in production — does not support full-duplex: the
// first streamed response byte makes it close the still-uploading request body,
// which silently truncates the import. So the client sends the export in bounded
// BATCHES (each a self-contained POST it can finish well under the proxy's read
// timeout), and finally one POST with ?recompute=1 that rebuilds the derived
// graph once. Every record id is deterministic, so batches and re-runs upsert
// rather than duplicate — a timed-out batch or finalize is simply retried.
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

// maxImportBodyBytes caps the NDJSON body an authenticated-but-untrusted client
// may stream. The stream is processed incrementally (one record + a bounded
// batch at a time), so this bounds the TOTAL upload rather than per-record
// memory; a body over the cap surfaces as a fatal decode error reported in the
// response summary. Sized to admit very large palaces (hundreds of thousands of
// chunked drawers at ~1–2 KB each) while refusing the absurd.
const maxImportBodyBytes = 256 << 20 // 256 MiB

// maxPendingTunnels bounds the deferred-tunnel buffer. Tunnels are applied after
// drawers (CreateTunnel needs the endpoint rooms to exist); since every drawer
// precedes every tunnel in the export, the buffer can be drained mid-stream once
// it reaches this size instead of growing to EOF.
const maxPendingTunnels = 512

// Drawers is the subset of palace.Service the importer needs. Declaring it at the
// consumer (Go's "accept interfaces" guidance) keeps the package decoupled from
// the concrete service and trivially fakeable in tests.
//
// Absorb* write rows ONLY (no embedding); the background embed worker builds the
// vectors afterwards. This is what lets an import finish in seconds rather than
// blocking on the embedder per batch. PendingCount reports how many rows are still
// awaiting that background embedding, surfaced to the client on finalize.
type Drawers interface {
	AbsorbDrawers(ctx context.Context, teamID string, in []palace.ImportDrawer) (int, error)
	AbsorbClosets(ctx context.Context, teamID string, in []palace.ImportCloset) (int, error)
	KGAdd(ctx context.Context, teamID, subject, predicate, object, validFrom, validTo, sourceCloset, sourceFile, sourceDrawerID string) (palace.KGAddResult, error)
	CreateTunnel(ctx context.Context, teamID string, in palace.TunnelInput, now string) (palace.Tunnel, error)
	RecomputeGraph(ctx context.Context, teamID, wing string, prune bool) (palace.RecomputeResult, error)
	PendingCount(ctx context.Context, teamID string) (int, error)
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

// Result is the summary of one ingest: the running counts, returned to an HTTP
// client as the response body and to the offline CLI as a value to print. Done
// marks it final.
type Result struct {
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
	// Pending is how many of the tenant's rows still await background embedding,
	// set on the finalize request so the client can report "indexing N".
	Pending int `json:"pending,omitempty"`
}

// serve runs one import request: authorize, meter, then read the WHOLE body,
// re-file every record, optionally rebuild the derived graph, and write a single
// JSON summary. It deliberately does not write the response while reading the
// request (see the package doc): doing so would let a non-full-duplex proxy
// truncate the upload. Because the body is fully consumed before we respond, a
// real status code is safe to choose — unlike the old streaming handler, which
// had to commit to a 200 up front.
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
	// Meter each request (each batch and the finalize). A migration is many small
	// POSTs now rather than one stream, but each is bounded and the per-request
	// embed load is real, so metering per request is both fair and still trivially
	// under any monthly cap; an over-cap project is refused (already-filed batches
	// persist and an idempotent re-run finishes after an upgrade).
	st, err := metering.Allow(r.Context(), t.TeamID)
	if err != nil {
		http.Error(w, "usage metering failed", http.StatusInternalServerError)
		return
	}
	if !st.Allowed {
		// st.CapRejection(), not a literal: under a deployment-fixed cap "upgrade
		// the project's plan" names an action that cannot move the enforced cap, and
		// on the self-hosted install this endpoint serves there is no plan to buy.
		// The MCP rejection learned that; this one did not, because the sentence was
		// written twice.
		http.Error(w, st.CapRejection(), http.StatusTooManyRequests)
		return
	}
	// Bound the upload before reading a byte of it. A body over the cap fails the
	// decoder; finish() reports it in the summary.
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBodyBytes)

	// recompute=1 finalizes a batched migration: after the last batch of records
	// is filed (across prior requests), the client sends one request that rebuilds
	// the derived graph (hallways + entity/topic tunnels) for the tenant. It is
	// idempotent, so a timed-out finalize is just retried. A small single-shot
	// migration can also set it on its only request to file and finalize at once.
	recompute := r.URL.Query().Get("recompute") == "1"

	// ?as=<wing> files every record of this request into that one wing, whatever
	// the bundle itself says. It is what makes a wing bundle portable: the export
	// carries no wing at all (see internal/wingbundle), so the operator names the
	// destination here. The value reaches the database as a wing label, so it is
	// validated exactly like any agent-supplied name — a bad one is a 400, not a
	// creatively-named wing. Omitted, the records keep whatever wing they carry,
	// which is what the mempalace migration bundles rely on.
	targetWing := ""
	if raw := r.URL.Query().Get("as"); raw != "" {
		clean, err := palace.SanitizeName(raw, "as")
		if err != nil {
			http.Error(w, "invalid target wing: "+err.Error(), http.StatusBadRequest)
			return
		}
		targetWing = clean
	}

	res := Ingest(r.Context(), drawers, t.TeamID, targetWing, r.Body, recompute)

	// The whole body is consumed, so the response is a single buffered summary.
	w.Header().Set("Content-Type", "application/json")
	b, err := json.Marshal(res)
	if err != nil {
		http.Error(w, "encode summary failed", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(append(b, '\n'))
}

// Ingest reads an NDJSON bundle from body and files every record under teamID,
// returning what it filed. A non-empty targetWing relabels every record into
// that one wing, ignoring whatever wing the bundle carried (an empty one leaves
// each record where it says it belongs). recompute additionally rebuilds the
// tenant's derived graph once the stream is drained.
//
// This is the single ingest path behind both transports — the HTTP handler above
// and the offline `agentsmemory wing import` CLI — so a bundle lands identically
// whether it arrives over the network or straight off the disk.
func Ingest(ctx context.Context, drawers Drawers, teamID, targetWing string, body io.Reader, recompute bool) Result {
	rn := &runner{drawers: drawers, teamID: teamID, targetWing: targetWing, start: time.Now()}
	rn.run(ctx, body, recompute)
	return rn.p
}

// runner carries the per-ingest state: the target tenant, the optional wing
// every record is relabelled into, running counters, and the buffers that batch
// drawers/closets/tunnels. It accumulates a single summary (p) returned once the
// whole body is read — there is no incremental writer, because the handler does
// not stream.
type runner struct {
	drawers    Drawers
	teamID     string
	targetWing string // non-empty ⇒ file everything here, whatever the bundle says
	start      time.Time

	p Result

	pendingDrawers []palace.ImportDrawer
	pendingClosets []palace.ImportCloset
	pendingTunnels []record // applied last: CreateTunnel needs the rooms to exist
	wings          map[string]struct{}
}

// run decodes the NDJSON stream and dispatches each record by kind. It uses a
// json.Decoder reading successive values, so an arbitrarily large drawer content
// is handled without a line-length cap. A malformed value is fatal (a Decoder
// cannot reliably resync), but the exporter emits well-formed JSON, so in
// practice the body completes; the partial import is idempotent, so a retry
// simply finishes it. recompute requests the one-time graph rebuild in finish().
func (rn *runner) run(ctx context.Context, body io.Reader, recompute bool) {
	rn.wings = map[string]struct{}{}
	dec := json.NewDecoder(bufio.NewReaderSize(body, 1<<16))
	for {
		// Stop promptly if the client hung up — there is no point embedding and
		// recomputing for a connection that will never read the result.
		if err := ctx.Err(); err != nil {
			rn.finish(ctx, err, recompute)
			return
		}
		var rec record
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			rn.finish(ctx, err, recompute)
			return
		}
		rn.dispatch(ctx, rec)
	}
	rn.finish(ctx, nil, recompute)
}

// dispatch routes one record. Drawers and closets batch; KG facts apply inline
// (cheap, relational, no drawer dependency); tunnels are deferred to the end
// because CreateTunnel validates that both endpoint rooms already hold a drawer.
func (rn *runner) dispatch(ctx context.Context, rec record) {
	switch rec.Kind {
	case "manifest":
		// Optional first line; carries the grand total for a progress denominator.
		// The batched client tracks its own totals, so this is informational only.
		rn.p.Total = rec.Total
	case "drawer", "diary":
		wing := rn.wingFor(rec.Wing)
		if wing != "" {
			rn.wings[wing] = struct{}{}
		}
		rn.pendingDrawers = append(rn.pendingDrawers, palace.ImportDrawer{
			Wing: wing, Room: rec.Room, SourceFile: rec.SourceFile, ChunkIndex: rec.ChunkIndex,
			Content: rec.Content, Entities: rec.Entities, FiledAt: rec.FiledAt,
			ContentDate: rec.ContentDate, Agent: rec.Agent, Topic: rec.Topic,
		})
		if len(rn.pendingDrawers) >= batchSize {
			rn.flushDrawers(ctx)
		}
	case "closet":
		rn.pendingClosets = append(rn.pendingClosets, palace.ImportCloset{
			Wing: rn.wingFor(rec.Wing), Room: rec.Room, SourceFile: rec.SourceFile,
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
	case "tunnel":
		rn.pendingTunnels = append(rn.pendingTunnels, rec)
		if len(rn.pendingTunnels) >= maxPendingTunnels {
			// Every drawer precedes every tunnel in the export, so the endpoint
			// rooms already exist — drain now rather than buffering to EOF.
			rn.flushDrawers(ctx)
			rn.flushClosets(ctx)
			rn.applyTunnels(ctx)
		}
	default:
		rn.p.Skipped++
	}
}

// wingFor picks the wing a record lands in: the ingest's target wing when one was
// named, otherwise whatever the record itself carried. A wing-less bundle
// (internal/wingbundle) supplies nothing, so without a target its records would
// land in the empty wing — which is exactly why every caller that accepts such a
// bundle demands a target up front rather than defaulting one.
func (rn *runner) wingFor(recordWing string) string {
	if rn.targetWing != "" {
		return rn.targetWing
	}
	return recordWing
}

// flushDrawers embeds and stores the buffered drawers, then clears the buffer.
func (rn *runner) flushDrawers(ctx context.Context) {
	if len(rn.pendingDrawers) == 0 {
		return
	}
	n, err := rn.drawers.AbsorbDrawers(ctx, rn.teamID, rn.pendingDrawers)
	if err != nil {
		rn.p.Error = "drawer import: " + err.Error()
	}
	rn.p.Drawers += n
	rn.p.Skipped += len(rn.pendingDrawers) - n
	rn.pendingDrawers = rn.pendingDrawers[:0]
}

// flushClosets embeds and stores the buffered closets, then clears the buffer.
func (rn *runner) flushClosets(ctx context.Context) {
	if len(rn.pendingClosets) == 0 {
		return
	}
	n, err := rn.drawers.AbsorbClosets(ctx, rn.teamID, rn.pendingClosets)
	if err != nil {
		rn.p.Error = "closet import: " + err.Error()
	}
	rn.p.Closets += n
	rn.p.Skipped += len(rn.pendingClosets) - n
	rn.pendingClosets = rn.pendingClosets[:0]
}

// applyTunnels creates the deferred explicit tunnels now that every drawer is
// stored (so the endpoint rooms exist). A tunnel into a room that never received
// a drawer is skipped, not fatal.
func (rn *runner) applyTunnels(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, rec := range rn.pendingTunnels {
		// Both endpoints are relabelled, not just one. A wing bundle only ever
		// carries tunnels whose two ends are inside the exported wing, so under a
		// target wing both sides must follow it — rewriting one end would point the
		// tunnel at a room in a wing this import never created.
		_, err := rn.drawers.CreateTunnel(ctx, rn.teamID, palace.TunnelInput{
			SourceWing: rn.wingFor(rec.SourceWing), SourceRoom: rec.SourceRoom,
			TargetWing: rn.wingFor(rec.TargetWing), TargetRoom: rec.TargetRoom, Label: rec.Label,
		}, now)
		if err != nil {
			rn.p.Skipped++
		} else {
			rn.p.Tunnels++
		}
	}
	rn.pendingTunnels = nil
}

// finish drains the buffers, applies tunnels, and — only when this request asked
// to finalize (recompute) — rebuilds the derived graph. It then stamps the
// summary. fatal, when non-nil, is a decode error that ended ingestion early; the
// partial import still stands (and is idempotent), so a retry finishes it.
func (rn *runner) finish(ctx context.Context, fatal error, recompute bool) {
	rn.flushDrawers(ctx)
	rn.flushClosets(ctx)
	rn.applyTunnels(ctx)

	// Hallways and entity/topic tunnels are derived, so they are rebuilt from the
	// imported drawers rather than carried over the wire — but only on the client's
	// finalize request, so a 60-batch migration rebuilds once at the end instead of
	// 60 times. Best-effort: a recompute failure does not fail an otherwise-good
	// migration. Skip it if the client has already hung up — the data stands and a
	// re-run (or a retried finalize) rebuilds the graph.
	if recompute && ctx.Err() == nil {
		if res, err := rn.drawers.RecomputeGraph(ctx, rn.teamID, "", false); err == nil {
			rn.p.Hallways = res.Hallways
		}
		// Report how many rows are still queued for background embedding, so the
		// client can tell the user the migration's text has landed but search is
		// still catching up. Best-effort: a count error must not fail the import.
		if n, err := rn.drawers.PendingCount(ctx, rn.teamID); err == nil {
			rn.p.Pending = n
		}
	}

	rn.p.Done = true
	rn.p.ElapsedMs = time.Since(rn.start).Milliseconds()
	if fatal != nil && rn.p.Error == "" {
		rn.p.Error = "stream ended early: " + fatal.Error()
	}
}
