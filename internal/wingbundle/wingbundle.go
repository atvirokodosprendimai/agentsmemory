// Package wingbundle is the portable file format one wing of a palace travels
// in: a stream of NDJSON records carrying the wing's drawers, closets and
// intra-wing tunnels as plain text.
//
// The defining property is what a bundle does NOT contain: a wing name. Not on
// a record, not in the manifest, nowhere. A bundle is single-wing by
// construction, so the name would be redundant at best — and at worst it would
// quietly decide where the memories land. Naming the destination is the
// importer's job, taken from an explicit flag (`--as`) or query parameter
// (`?as=`), which is why an import with no target is refused rather than
// defaulted. Exporting is therefore "take this wing's contents", never "take
// this wing", and one bundle can be filed into as many differently-named wings
// as you like.
//
// Two further things are deliberately absent:
//
//   - Vectors. A bundle is text only, and the destination re-embeds in the
//     background. Carrying float32 vectors would multiply the file size several
//     times over AND silently corrupt search when the target palace runs a
//     different embedding model or dimension than the source did. Re-embedding
//     is the same decision the mempalace→SaaS migration made in 2026-06.
//   - Knowledge-graph facts. A KG fact is team-global, not wing-scoped, so
//     there is no subset of the graph that belongs to one wing; including it
//     would sweep every OTHER wing's facts into a bundle labelled as this one.
//
// Hallways and derived (topic/entity) tunnels are omitted for a different
// reason: they are computed from the drawers, so the destination rebuilds them
// from what it received rather than trusting numbers from the source.
package wingbundle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// Format is the version tag written into every manifest. It exists so a reader
// can refuse a file shape it does not understand instead of importing nonsense;
// bump the number if a future change is not backward compatible.
const Format = "agentsmemory-wing/1"

// Record kinds. A bundle is a flat union discriminated by Kind, one JSON object
// per line — the same shape POST /import already consumes, minus the wing.
const (
	KindManifest = "manifest"
	KindDrawer   = "drawer"
	KindCloset   = "closet"
	KindTunnel   = "tunnel"
)

// pageSize bounds how many drawers are read per round while streaming. It keeps
// a 30k-drawer wing off the heap without making the export chatty.
const pageSize = 500

// ErrUnknownWing is returned when the requested wing holds no drawers. It is a
// hard error rather than an empty bundle: an export that silently produces
// nothing is indistinguishable from a successful one until the import lands
// empty somewhere else, which is far more expensive to notice.
var ErrUnknownWing = fmt.Errorf("unknown wing")

// Record is one line of a bundle. Fields are shared across kinds wherever they
// mean the same thing, and every one is omitempty so a bundle stays small and a
// reader never has to distinguish "absent" from "empty".
//
// There is no Wing field, and adding one would defeat the format (see the
// package doc). The destination wing is supplied at import time.
type Record struct {
	Kind string `json:"kind"`

	// manifest (first line)
	Format string `json:"format,omitempty"`
	Total  int    `json:"total,omitempty"`

	// drawer + closet
	Room        string   `json:"room,omitempty"`
	SourceFile  string   `json:"source_file,omitempty"`
	ChunkIndex  int      `json:"chunk_index,omitempty"`
	Content     string   `json:"content,omitempty"`
	Entities    []string `json:"entities,omitempty"`
	FiledAt     string   `json:"filed_at,omitempty"`
	ContentDate string   `json:"content_date,omitempty"`
	Agent       string   `json:"agent,omitempty"` // diary drawers only
	Topic       string   `json:"topic,omitempty"` // diary drawers only

	// closet
	Document string `json:"document,omitempty"`

	// tunnel — rooms only, because both endpoints are inside the exported wing
	SourceRoom string `json:"source_room,omitempty"`
	TargetRoom string `json:"target_room,omitempty"`
	Label      string `json:"label,omitempty"`
}

// Stats reports what an export actually wrote, so a caller can show a summary
// and compare it against the manifest's Total.
type Stats struct {
	Drawers int
	Closets int
	Tunnels int
	// Total is the count written into the manifest, computed before streaming
	// began. It differs from the sum above only if the wing was written to
	// concurrently mid-export.
	Total int
}

// Source is the slice of the palace repository an export reads. It is declared
// here, at the consumer, so this package depends on four methods rather than on
// the whole repository — which also makes it trivially fakeable in tests.
// *palace.Repo satisfies it.
type Source interface {
	Wings(ctx context.Context, teamID string) ([]palace.WingStat, error)
	// ListCurrent, not List: a bundle record carries no validity window, so an
	// exported ended row would be re-imported as CURRENT. Declared at the consumer
	// so the choice is visible here rather than inferred from a call site.
	ListCurrent(ctx context.Context, teamID, wing, room string, limit, offset int) ([]palace.Drawer, error)
	ClosetsByWing(ctx context.Context, teamID, wing string) ([]palace.Closet, error)
	ListTunnels(ctx context.Context, teamID, wing string) ([]palace.Tunnel, error)
}

// Export streams one wing of teamID to w as an NDJSON bundle and reports what it
// wrote. An unknown wing fails with ErrUnknownWing naming the wings that do
// exist, so a typo is corrected in one step instead of producing a valid, empty
// file.
//
// Records are emitted manifest → drawers → closets → tunnels. That order is
// load-bearing on the way back in: the importer's CreateTunnel validates that
// each endpoint room already holds a drawer, so every drawer must precede every
// tunnel.
func Export(ctx context.Context, src Source, teamID, wing string, w io.Writer) (Stats, error) {
	var st Stats
	if teamID == "" || wing == "" {
		return st, fmt.Errorf("wingbundle: teamID and wing are required")
	}

	drawerCount, err := countDrawers(ctx, src, teamID, wing)
	if err != nil {
		return st, err
	}

	closets, err := src.ClosetsByWing(ctx, teamID, wing)
	if err != nil {
		return st, fmt.Errorf("list closets: %w", err)
	}
	tunnels, err := intraWingTunnels(ctx, src, teamID, wing)
	if err != nil {
		return st, err
	}

	// The manifest total is computed up front, over exactly the selection that
	// follows, so a progress bar reading it has a truthful denominator.
	st.Total = drawerCount + len(closets) + len(tunnels)
	enc := json.NewEncoder(w)
	if err := enc.Encode(Record{Kind: KindManifest, Format: Format, Total: st.Total}); err != nil {
		return st, fmt.Errorf("write manifest: %w", err)
	}

	// Drawers first, paged, so a large wing never lands on the heap at once.
	for offset := 0; ; offset += pageSize {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		// ListCurrent for the reason copywing uses it: a bundle record carries no
		// validity window, so an exported ended row is re-imported as CURRENT — the
		// retracted text arrives asserted, with the reason gone. Exporting history
		// needs a format that can carry it (ADR-038 follow-up); until then this
		// exports what the team still asserts.
		page, err := src.ListCurrent(ctx, teamID, wing, "", pageSize, offset)
		if err != nil {
			return st, fmt.Errorf("list drawers: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, d := range page {
			if err := enc.Encode(drawerRecord(d)); err != nil {
				return st, fmt.Errorf("write drawer: %w", err)
			}
			st.Drawers++
		}
		if len(page) < pageSize {
			break
		}
	}

	for _, c := range closets {
		if err := enc.Encode(closetRecord(c)); err != nil {
			return st, fmt.Errorf("write closet: %w", err)
		}
		st.Closets++
	}
	for _, t := range tunnels {
		if err := enc.Encode(tunnelRecord(t)); err != nil {
			return st, fmt.Errorf("write tunnel: %w", err)
		}
		st.Tunnels++
	}
	return st, nil
}

// countDrawers resolves the wing against the team's actual wings, returning its
// drawer count. The lookup doubles as validation — a wing with no drawers is not
// a wing — so the caller gets one indexed aggregate instead of a separate
// existence check.
func countDrawers(ctx context.Context, src Source, teamID, wing string) (int, error) {
	wings, err := src.Wings(ctx, teamID)
	if err != nil {
		return 0, fmt.Errorf("list wings: %w", err)
	}
	names := make([]string, 0, len(wings))
	for _, ws := range wings {
		if ws.Wing == wing {
			return ws.Drawers, nil
		}
		names = append(names, ws.Wing)
	}
	if len(names) == 0 {
		return 0, fmt.Errorf("%w %q: this workspace holds no wings at all", ErrUnknownWing, wing)
	}
	return 0, fmt.Errorf("%w %q — this workspace holds: %s", ErrUnknownWing, wing, strings.Join(names, ", "))
}

// intraWingTunnels returns the explicit tunnels with BOTH endpoints inside wing.
//
// Both halves of that filter matter. Only explicit tunnels are carried, because
// topic and entity tunnels are derived from the drawers and the destination
// recomputes them. And a tunnel with one endpoint outside the wing is dropped
// because the importer's CreateTunnel requires each endpoint room to hold a
// drawer — the far end simply is not in this bundle, so importing it would fail.
//
// In practice this usually yields zero: an explicit tunnel exists to link two
// DIFFERENT wings, so few survive a single-wing selection. That is correct, not
// a bug.
func intraWingTunnels(ctx context.Context, src Source, teamID, wing string) ([]palace.Tunnel, error) {
	all, err := src.ListTunnels(ctx, teamID, wing)
	if err != nil {
		return nil, fmt.Errorf("list tunnels: %w", err)
	}
	kept := make([]palace.Tunnel, 0, len(all))
	for _, t := range all {
		if t.Kind == palace.TunnelExplicit && t.Source.Wing == wing && t.Target.Wing == wing {
			kept = append(kept, t)
		}
	}
	return kept, nil
}

// drawerRecord projects a stored drawer onto its wire form. Wing, ID, TeamID and
// ParentID are all dropped: the first by design, the rest because the
// destination re-mints them under its own id recipe.
func drawerRecord(d palace.Drawer) Record {
	return Record{
		Kind:        KindDrawer,
		Room:        d.Room,
		SourceFile:  d.SourceFile,
		ChunkIndex:  d.ChunkIndex,
		Content:     d.Content,
		Entities:    d.Entities,
		FiledAt:     d.FiledAt,
		ContentDate: d.ContentDate,
		Agent:       d.Agent,
		Topic:       d.Topic,
	}
}

// closetRecord projects a stored closet onto its wire form.
func closetRecord(c palace.Closet) Record {
	return Record{
		Kind:       KindCloset,
		Room:       c.Room,
		SourceFile: c.SourceFile,
		Document:   c.Document,
		Entities:   c.Entities,
		FiledAt:    c.FiledAt,
	}
}

// tunnelRecord projects a stored tunnel onto its wire form, keeping only the
// rooms — both endpoints are in the exported wing, so the wings are implied by
// whatever the import is told to call it.
func tunnelRecord(t palace.Tunnel) Record {
	return Record{
		Kind:       KindTunnel,
		SourceRoom: t.Source.Room,
		TargetRoom: t.Target.Room,
		Label:      t.Label,
	}
}
