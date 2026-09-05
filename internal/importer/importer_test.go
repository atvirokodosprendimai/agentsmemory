package importer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
)

// fakeDrawers records what the handler routed where, so a test can assert each
// NDJSON kind reached the right service method. It also keeps the records
// themselves, which is what lets a test check the wing each one landed in.
type fakeDrawers struct {
	drawers, closets, kg, tunnels, recomputes int

	gotDrawers []palace.ImportDrawer
	gotClosets []palace.ImportCloset
	gotTunnels []palace.TunnelInput
}

func (f *fakeDrawers) AbsorbDrawers(_ context.Context, _ string, in []palace.ImportDrawer) (int, error) {
	f.drawers += len(in)
	f.gotDrawers = append(f.gotDrawers, in...)
	return len(in), nil
}

func (f *fakeDrawers) AbsorbClosets(_ context.Context, _ string, in []palace.ImportCloset) (int, error) {
	f.closets += len(in)
	f.gotClosets = append(f.gotClosets, in...)
	return len(in), nil
}

// PendingCount reports the absorbed rows as still awaiting background embedding,
// which is what the finalize summary surfaces to the client.
func (f *fakeDrawers) PendingCount(_ context.Context, _ string) (int, error) {
	return f.drawers + f.closets, nil
}

func (f *fakeDrawers) KGAdd(_ context.Context, _, _, _, _, _, _, _, _, _ string) (palace.KGAddResult, error) {
	f.kg++
	return palace.KGAddResult{}, nil
}

func (f *fakeDrawers) CreateTunnel(_ context.Context, _ string, in palace.TunnelInput, _ string) (palace.Tunnel, error) {
	f.tunnels++
	f.gotTunnels = append(f.gotTunnels, in)
	return palace.Tunnel{}, nil
}

func (f *fakeDrawers) RecomputeGraph(_ context.Context, _, _ string, _ bool) (palace.RecomputeResult, error) {
	f.recomputes++
	return palace.RecomputeResult{Hallways: 3}, nil
}

// allowAll meters every import as permitted; denyAll refuses (over-cap).
type allowAll struct{}

func (allowAll) Allow(_ context.Context, _ string) (usage.Status, error) {
	return usage.Status{Allowed: true, Used: 1, Cap: 10000}, nil
}

type denyAll struct{}

func (denyAll) Allow(_ context.Context, _ string) (usage.Status, error) {
	return usage.Status{Allowed: false, Used: 10000, Cap: 10000}, nil
}

const bundle = `{"kind":"manifest","total":4}
{"kind":"drawer","wing":"acme","room":"backend","content":"the hub fans out messages"}
{"kind":"diary","wing":"wing_claude","room":"diary","agent":"claude","topic":"general","content":"SESSION:built import"}
{"kind":"closet","wing":"acme","room":"backend","source_file":"notes.md","document":"built the hub|hub;ws|->d1"}
{"kind":"kg","subject":"hub","predicate":"fans_out","object":"messages","valid_from":"2026-01-01"}
{"kind":"tunnel","source_wing":"acme","source_room":"backend","target_wing":"wing_claude","target_room":"diary","label":"shared work"}
`

// authedRequest builds a POST /import with the bundle body and a tenant already
// on the context (as the gate would leave it). recompute=1 makes it a single-shot
// migration: file every record AND rebuild the derived graph in one request (the
// same flag the batched client sends on its final finalize call).
func authedRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/import?recompute=1", strings.NewReader(body))
	ctx := auth.WithTenant(r.Context(), tenant.Tenant{TeamID: "team-1"})
	return r.WithContext(ctx)
}

func TestImportRoutesEveryKind(t *testing.T) {
	fd := &fakeDrawers{}
	h := Handler(fd, allowAll{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(bundle))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// The final NDJSON line is the summary; parse it.
	var last Result
	sc := bufio.NewScanner(bytes.NewReader(rec.Body.Bytes()))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &last); err != nil {
			t.Fatalf("progress line %q: %v", line, err)
		}
	}
	if !last.Done {
		t.Fatal("expected a final done=true summary line")
	}
	if last.Drawers != 2 {
		t.Errorf("drawers = %d, want 2 (drawer + diary)", last.Drawers)
	}
	if last.Closets != 1 {
		t.Errorf("closets = %d, want 1", last.Closets)
	}
	if last.KGFacts != 1 {
		t.Errorf("kg_facts = %d, want 1", last.KGFacts)
	}
	if last.Tunnels != 1 {
		t.Errorf("tunnels = %d, want 1", last.Tunnels)
	}
	if last.Hallways != 3 {
		t.Errorf("hallways = %d, want 3 (from recompute)", last.Hallways)
	}
	if fd.recomputes != 1 {
		t.Errorf("recomputes = %d, want exactly 1 at the end", fd.recomputes)
	}
	// Absorbed rows are reported as pending background embedding (2 drawers + 1 closet).
	if last.Pending != 3 {
		t.Errorf("pending = %d, want 3 (rows awaiting background embedding)", last.Pending)
	}
}

func TestImportUnauthenticated(t *testing.T) {
	h := Handler(&fakeDrawers{}, allowAll{})
	// No tenant on the context — the gate resolved nothing.
	r := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(bundle))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestImportOverCap(t *testing.T) {
	h := Handler(&fakeDrawers{}, denyAll{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(bundle))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
}

// winglessBundle is what internal/wingbundle produces: no wing on any record, so
// the destination is entirely the importer's to decide.
const winglessBundle = `{"kind":"manifest","format":"agentsmemory-wing/1","total":3}
{"kind":"drawer","room":"decisions","content":"why we chose sqlite"}
{"kind":"closet","room":"decisions","source_file":"x.md","document":"index of x.md"}
{"kind":"tunnel","source_room":"decisions","target_room":"diary","label":"inside"}
`

// asRequest builds a POST /import?as=<wing> carrying body, with the tenant the
// gate would have resolved already on the context.
func asRequest(body, as string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/import?recompute=1&as="+as, strings.NewReader(body))
	return r.WithContext(auth.WithTenant(r.Context(), tenant.Tenant{TeamID: "team-1"}))
}

// TestImportAsRelabelsEveryRecord is the core of the "name the destination at
// import time" contract: with ?as=, every drawer, closet and BOTH tunnel
// endpoints land in that one wing, no matter what the bundle claimed. The legacy
// multi-wing bundle is used deliberately — if any record kept its own wing, a
// tunnel would end up pointing into a wing this import never created.
func TestImportAsRelabelsEveryRecord(t *testing.T) {
	fd := &fakeDrawers{}
	rec := httptest.NewRecorder()
	Handler(fd, allowAll{}).ServeHTTP(rec, asRequest(bundle, "wing_abc"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for _, d := range fd.gotDrawers {
		if d.Wing != "wing_abc" {
			t.Errorf("drawer %q landed in wing %q, want wing_abc", d.Content, d.Wing)
		}
	}
	for _, c := range fd.gotClosets {
		if c.Wing != "wing_abc" {
			t.Errorf("closet landed in wing %q, want wing_abc", c.Wing)
		}
	}
	for _, tn := range fd.gotTunnels {
		if tn.SourceWing != "wing_abc" || tn.TargetWing != "wing_abc" {
			t.Errorf("tunnel endpoints = %q/%q, want both wing_abc", tn.SourceWing, tn.TargetWing)
		}
	}
	if len(fd.gotDrawers) != 2 || len(fd.gotClosets) != 1 || len(fd.gotTunnels) != 1 {
		t.Errorf("routed %d drawers, %d closets, %d tunnels; want 2/1/1",
			len(fd.gotDrawers), len(fd.gotClosets), len(fd.gotTunnels))
	}
}

// TestImportWinglessBundleLandsInTarget is the end-to-end shape of the feature:
// a bundle that names no wing at all is filed entirely into the wing the caller
// asked for.
func TestImportWinglessBundleLandsInTarget(t *testing.T) {
	fd := &fakeDrawers{}
	rec := httptest.NewRecorder()
	Handler(fd, allowAll{}).ServeHTTP(rec, asRequest(winglessBundle, "wing_restored"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(fd.gotDrawers) != 1 || fd.gotDrawers[0].Wing != "wing_restored" {
		t.Fatalf("drawers = %+v, want one in wing_restored", fd.gotDrawers)
	}
	if len(fd.gotClosets) != 1 || fd.gotClosets[0].Wing != "wing_restored" {
		t.Fatalf("closets = %+v, want one in wing_restored", fd.gotClosets)
	}
	if len(fd.gotTunnels) != 1 {
		t.Fatalf("tunnels = %+v, want one", fd.gotTunnels)
	}
	// The bundle carried rooms only; the wing on both ends comes from ?as=.
	if tn := fd.gotTunnels[0]; tn.SourceWing != "wing_restored" || tn.TargetWing != "wing_restored" ||
		tn.SourceRoom != "decisions" || tn.TargetRoom != "diary" {
		t.Errorf("tunnel = %+v, want wing_restored/decisions → wing_restored/diary", tn)
	}
}

// TestImportWithoutAsPreservesRecordWing pins the backward-compatible path: the
// shipped mempalace migration bundles carry their own wings and must keep
// landing exactly where they say, so omitting ?as= must change nothing.
func TestImportWithoutAsPreservesRecordWing(t *testing.T) {
	fd := &fakeDrawers{}
	rec := httptest.NewRecorder()
	Handler(fd, allowAll{}).ServeHTTP(rec, authedRequest(bundle))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	wings := map[string]bool{}
	for _, d := range fd.gotDrawers {
		wings[d.Wing] = true
	}
	if !wings["acme"] || !wings["wing_claude"] {
		t.Errorf("drawer wings = %v, want the bundle's own acme + wing_claude", wings)
	}
	if tn := fd.gotTunnels[0]; tn.SourceWing != "acme" || tn.TargetWing != "wing_claude" {
		t.Errorf("tunnel endpoints = %q/%q, want the bundle's own acme/wing_claude",
			tn.SourceWing, tn.TargetWing)
	}
}

// TestImportRejectsInvalidTargetWing covers the untrusted-input edge: ?as= is
// attacker-supplied and becomes a stored wing label, so it goes through the same
// validator as any agent-supplied name and a bad one is refused before a single
// record is filed.
func TestImportRejectsInvalidTargetWing(t *testing.T) {
	for _, as := range []string{"../etc", "wing/abc", "wing_a\x00b", "  ", strings.Repeat("w", 200)} {
		t.Run(as, func(t *testing.T) {
			fd := &fakeDrawers{}
			rec := httptest.NewRecorder()
			// Build the URL by hand: url.Values would escape the traversal away.
			r := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(bundle))
			q := r.URL.Query()
			q.Set("as", as)
			r.URL.RawQuery = q.Encode()
			r = r.WithContext(auth.WithTenant(r.Context(), tenant.Tenant{TeamID: "team-1"}))

			Handler(fd, allowAll{}).ServeHTTP(rec, r)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d for as=%q, want 400", rec.Code, as)
			}
			if fd.drawers != 0 || fd.closets != 0 || fd.tunnels != 0 {
				t.Errorf("filed records despite a rejected target wing: %+v", fd)
			}
		})
	}
}

// TestImportWireWithPythonPusher is a real cross-language round-trip: the Python
// CLI uploads the bundle in bounded, length-delimited batches (plus a finalize
// request) to a live server wrapping the handler, proving the two deliverables
// agree on the wire format. It is skipped when python3 or the script is
// unavailable so `go test ./...` never depends on them.
func TestImportWireWithPythonPusher(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	script := filepath.Join("..", "..", "clients", "migrate", "mempalace_export.py")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("exporter script not found: %v", err)
	}

	fd := &fakeDrawers{}
	// Wrap the handler with the tenant the auth gate would otherwise inject.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithTenant(r.Context(), tenant.Tenant{TeamID: "team-1"})
		Handler(fd, allowAll{}).ServeHTTP(w, r.WithContext(ctx))
	}))
	defer srv.Close()

	bundlePath := filepath.Join(t.TempDir(), "bundle.ndjson")
	if err := os.WriteFile(bundlePath, []byte(bundle), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	cmd := testexec.Command(t, py, script, "--file", bundlePath, "--push",
		"--server", srv.URL, "--token", "test-token")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python push failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "done.") {
		t.Errorf("expected a 'done.' summary in pusher output, got:\n%s", out)
	}
	// The in-process fake recorded exactly what the Python client uploaded across
	// its batch(es) and the finalize request.
	if fd.drawers != 2 || fd.closets != 1 || fd.kg != 1 || fd.tunnels != 1 {
		t.Errorf("routed drawers=%d closets=%d kg=%d tunnels=%d, want 2/1/1/1",
			fd.drawers, fd.closets, fd.kg, fd.tunnels)
	}
}
