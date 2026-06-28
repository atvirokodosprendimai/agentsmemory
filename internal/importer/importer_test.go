package importer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
)

// fakeDrawers records what the handler routed where, so a test can assert each
// NDJSON kind reached the right service method.
type fakeDrawers struct {
	drawers, closets, kg, tunnels, recomputes int
}

func (f *fakeDrawers) ImportDrawers(_ context.Context, _ string, in []palace.ImportDrawer) (int, error) {
	f.drawers += len(in)
	return len(in), nil
}

func (f *fakeDrawers) ImportClosets(_ context.Context, _ string, in []palace.ImportCloset) (int, error) {
	f.closets += len(in)
	return len(in), nil
}

func (f *fakeDrawers) KGAdd(_ context.Context, _, _, _, _, _, _, _, _, _ string) (palace.KGAddResult, error) {
	f.kg++
	return palace.KGAddResult{}, nil
}

func (f *fakeDrawers) CreateTunnel(_ context.Context, _ string, _ palace.TunnelInput, _ string) (palace.Tunnel, error) {
	f.tunnels++
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
{"kind":"drawer","wing":"forumchat","room":"backend","content":"the hub fans out messages"}
{"kind":"diary","wing":"wing_claude","room":"diary","agent":"claude","topic":"general","content":"SESSION:built import"}
{"kind":"closet","wing":"forumchat","room":"backend","source_file":"notes.md","document":"built the hub|hub;ws|->d1"}
{"kind":"kg","subject":"hub","predicate":"fans_out","object":"messages","valid_from":"2026-01-01"}
{"kind":"tunnel","source_wing":"forumchat","source_room":"backend","target_wing":"wing_claude","target_room":"diary","label":"shared work"}
`

// authedRequest builds a POST /import with the bundle body and a tenant already
// on the context (as the gate would leave it).
func authedRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(body))
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
	var last progress
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
