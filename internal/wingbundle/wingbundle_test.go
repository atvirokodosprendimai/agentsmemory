package wingbundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// fakeSource is an in-memory Source: enough of the repository to drive Export
// without a database, so these tests pin the FORMAT rather than the storage.
type fakeSource struct {
	wings   []palace.WingStat
	drawers map[string][]palace.Drawer
	closets map[string][]palace.Closet
	tunnels []palace.Tunnel
}

func (f *fakeSource) Wings(context.Context, string) ([]palace.WingStat, error) {
	return f.wings, nil
}

// ListCurrent honours limit/offset because the paging loop in Export depends on
// it: a stub that ignored them would loop forever or silently truncate.
//
// It also SKIPS ended rows, so the double is honest about the one property the
// real ListCurrent was chosen for. A stub that returned everything would let a
// bundle carrying retracted text pass — and the format has no validity window, so
// that text would arrive in the destination asserted as current.
func (f *fakeSource) ListCurrent(_ context.Context, _, wing, _ string, limit, offset int) ([]palace.Drawer, error) {
	current := make([]palace.Drawer, 0, len(f.drawers[wing]))
	for _, d := range f.drawers[wing] {
		if d.ValidTo == "" {
			current = append(current, d)
		}
	}
	if offset >= len(current) {
		return nil, nil
	}
	return current[offset:min(offset+limit, len(current))], nil
}

func (f *fakeSource) ClosetsByWing(_ context.Context, _, wing string) ([]palace.Closet, error) {
	return f.closets[wing], nil
}

// ListTunnels mimics the repo's wing filter: it returns tunnels TOUCHING the
// wing (either endpoint), which is exactly why Export has to narrow further.
func (f *fakeSource) ListTunnels(_ context.Context, _, wing string) ([]palace.Tunnel, error) {
	var out []palace.Tunnel
	for _, t := range f.tunnels {
		if t.Source.Wing == wing || t.Target.Wing == wing {
			out = append(out, t)
		}
	}
	return out, nil
}

// seeded builds a two-wing palace: wing_a has drawers, a closet, and tunnels of
// every interesting shape; wing_b exists so cross-wing links have a far end.
func seeded() *fakeSource {
	return &fakeSource{
		wings: []palace.WingStat{
			{Wing: "wing_a", Drawers: 2, Rooms: 2},
			{Wing: "wing_b", Drawers: 1, Rooms: 1},
		},
		drawers: map[string][]palace.Drawer{
			"wing_a": {
				{ID: "d1", TeamID: "t1", Wing: "wing_a", Room: "decisions", SourceFile: "x.md",
					Content: "why we chose sqlite", Entities: []string{"SQLite"}, FiledAt: "2026-08-18T10:00:00Z"},
				{ID: "d2", TeamID: "t1", Wing: "wing_a", Room: "diary", Content: "SESSION:...",
					Agent: "claude", Topic: "session", FiledAt: "2026-08-18T11:00:00Z"},
			},
			"wing_b": {{ID: "d3", TeamID: "t1", Wing: "wing_b", Room: "notes", Content: "elsewhere"}},
		},
		closets: map[string][]palace.Closet{
			"wing_a": {{ID: "c1", TeamID: "t1", Wing: "wing_a", Room: "decisions", SourceFile: "x.md",
				Document: "index of x.md", Entities: []string{"SQLite"}}},
		},
		tunnels: []palace.Tunnel{
			// kept: explicit, both ends inside wing_a
			{ID: "t-in", Kind: palace.TunnelExplicit, Label: "inside",
				Source: palace.Endpoint{Wing: "wing_a", Room: "decisions"},
				Target: palace.Endpoint{Wing: "wing_a", Room: "diary"}},
			// dropped: explicit but leaves the wing — its far end is not in the bundle
			{ID: "t-out", Kind: palace.TunnelExplicit, Label: "crosses",
				Source: palace.Endpoint{Wing: "wing_a", Room: "decisions"},
				Target: palace.Endpoint{Wing: "wing_b", Room: "notes"}},
			// dropped: derived, so the destination recomputes it
			{ID: "t-derived", Kind: palace.TunnelEntity, Label: "SQLite",
				Source: palace.Endpoint{Wing: "wing_a", Room: "decisions"},
				Target: palace.Endpoint{Wing: "wing_a", Room: "diary"}},
		},
	}
}

// decode reads a bundle back into records, failing the test on malformed lines.
func decode(t *testing.T, b []byte) []Record {
	t.Helper()
	var out []Record
	dec := json.NewDecoder(bytes.NewReader(b))
	for {
		var rec Record
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode bundle: %v", err)
		}
		out = append(out, rec)
	}
	return out
}

// TestExportCarriesNoWing is the format's defining property: no record may carry
// a field naming a wing, and no value may leak the source wing's name. If this
// fails, an import can no longer be told where to land — the file decides
// instead of the operator.
//
// It inspects decoded keys and values rather than grepping the raw bytes,
// because the manifest's own format tag ("agentsmemory-wing/1") legitimately
// contains the substring.
func TestExportCarriesNoWing(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Export(context.Background(), seeded(), "t1", "wing_a", &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode bundle: %v", err)
		}
		for k, v := range raw {
			if strings.Contains(strings.ToLower(k), "wing") {
				t.Errorf("record carries a wing-naming field %q = %v", k, v)
			}
			if s, ok := v.(string); ok && strings.Contains(s, "wing_a") {
				t.Errorf("field %q leaks the source wing name: %q", k, s)
			}
		}
	}
}

// TestExportManifestTotalMatchesPayload pins the progress denominator to the
// truth. A stale total (counted over the unfiltered palace, say) makes an import
// look stuck at 40% forever.
func TestExportManifestTotalMatchesPayload(t *testing.T) {
	var buf bytes.Buffer
	st, err := Export(context.Background(), seeded(), "t1", "wing_a", &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	recs := decode(t, buf.Bytes())
	if len(recs) == 0 || recs[0].Kind != KindManifest {
		t.Fatalf("first record = %+v, want a manifest", recs[0])
	}
	if recs[0].Format != Format {
		t.Errorf("manifest format = %q, want %q", recs[0].Format, Format)
	}
	if got, want := len(recs)-1, recs[0].Total; got != want {
		t.Errorf("payload records = %d, manifest total = %d — they must agree", got, want)
	}
	if st.Drawers+st.Closets+st.Tunnels != st.Total {
		t.Errorf("stats %+v do not sum to Total", st)
	}
}

// TestExportKeepsOnlyIntraWingExplicitTunnels covers the rule that a hand-rolled
// jq filter gets wrong: a tunnel leaving the selection fails on import (its far
// endpoint room holds no drawer), and a derived tunnel is rebuilt, not carried.
func TestExportKeepsOnlyIntraWingExplicitTunnels(t *testing.T) {
	var buf bytes.Buffer
	st, err := Export(context.Background(), seeded(), "t1", "wing_a", &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if st.Tunnels != 1 {
		t.Fatalf("tunnels exported = %d, want 1 (the intra-wing explicit one)", st.Tunnels)
	}
	for _, rec := range decode(t, buf.Bytes()) {
		if rec.Kind != KindTunnel {
			continue
		}
		if rec.Label != "inside" {
			t.Errorf("exported tunnel %q, want the intra-wing one", rec.Label)
		}
		if rec.SourceRoom != "decisions" || rec.TargetRoom != "diary" {
			t.Errorf("tunnel rooms = %q→%q, want decisions→diary", rec.SourceRoom, rec.TargetRoom)
		}
	}
}

// TestExportPreservesDrawerFields guards the fields a memory is worthless
// without — the diary's agent/topic especially, since a diary drawer that loses
// them stops threading across sessions.
func TestExportPreservesDrawerFields(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Export(context.Background(), seeded(), "t1", "wing_a", &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}
	var diary *Record
	for _, rec := range decode(t, buf.Bytes()) {
		if rec.Kind == KindDrawer && rec.Room == "diary" {
			r := rec
			diary = &r
		}
	}
	if diary == nil {
		t.Fatal("diary drawer missing from bundle")
	}
	if diary.Agent != "claude" || diary.Topic != "session" {
		t.Errorf("diary agent/topic = %q/%q, want claude/session", diary.Agent, diary.Topic)
	}
	if diary.FiledAt == "" {
		t.Error("filed_at dropped; the memory loses its place in time")
	}
}

// TestExportUnknownWingFails is the "never produce a valid empty file" rule: a
// typo must stop the export and name the wings that exist.
func TestExportUnknownWingFails(t *testing.T) {
	var buf bytes.Buffer
	_, err := Export(context.Background(), seeded(), "t1", "wing_typo", &buf)
	if !errors.Is(err, ErrUnknownWing) {
		t.Fatalf("err = %v, want ErrUnknownWing", err)
	}
	if !strings.Contains(err.Error(), "wing_a") {
		t.Errorf("error %q does not name the wings that exist", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes for an unknown wing; want nothing", buf.Len())
	}
}

// TestExportPagesLargeWing walks past one page boundary so the paging loop's
// terminating condition is exercised rather than assumed.
func TestExportPagesLargeWing(t *testing.T) {
	src := seeded()
	big := make([]palace.Drawer, 0, pageSize+7)
	for i := range cap(big) {
		big = append(big, palace.Drawer{
			ID: fmt.Sprintf("d%d", i), Wing: "wing_big", Room: "bulk",
			Content: fmt.Sprintf("memory %d", i),
		})
	}
	src.wings = append(src.wings, palace.WingStat{Wing: "wing_big", Drawers: len(big), Rooms: 1})
	src.drawers["wing_big"] = big

	var buf bytes.Buffer
	st, err := Export(context.Background(), src, "t1", "wing_big", &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if st.Drawers != len(big) {
		t.Errorf("exported %d drawers, want %d — paging dropped records", st.Drawers, len(big))
	}
}
