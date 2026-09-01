package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// captureRequest runs one Embed against a stub Ollama and returns the decoded
// request body, so the assertions are about what went ON THE WIRE. The defect
// this file guards is invisible in Go: a field set on a struct that never
// reaches Ollama looks identical to a field that does.
func captureRequest(t *testing.T, numThread int) map[string]any {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("the request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2]]}`))
	}))
	defer srv.Close()

	e := New(srv.URL, "bge-m3", 5*time.Second, numThread)
	if _, err := e.Embed(context.Background(), []string{"one"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	return got
}

// TestAConfiguredThreadCapReachesOllama pins the half of the #149 fix that
// nothing else can see.
//
// A cgroup quota is not a thread setting: llama.cpp sizes its pool from the
// HOST's core count, so a container capped at N CPUs starts a pool for the whole
// machine and spends the quota being descheduled. Measured 2026-08-31 on a
// 16-core host with a quota of 12: 4.1s at the default 16 threads against 0.054s
// at 12, and 0.172s at ONE — the cliff is at threads > quota, so lowering the cap
// without this option makes embedding slower rather than lighter.
//
// The assertion is on the marshalled request because the option only does
// anything if Ollama receives it. A test on the Embedder's field would pass over
// a client that builds the option and drops it before the POST.
func TestAConfiguredThreadCapReachesOllama(t *testing.T) {
	got := captureRequest(t, 12)
	opts, ok := got["options"].(map[string]any)
	if !ok {
		t.Fatalf("the request carries no options object, so the configured cap never reaches "+
			"llama.cpp and the container stays throttled: %v", got)
	}
	if n, _ := opts["num_thread"].(float64); int(n) != 12 {
		t.Errorf("num_thread = %v, want 12 — the pool must match the quota, not the host", opts["num_thread"])
	}
}

// TestAnUnconfiguredEmbedderSendsNoThreadOption is the other direction, and it
// is not symmetry for its own sake: sending num_thread:0 would mean "zero
// threads" to a reader of the payload, and an operator running Ollama on the
// host with no quota should keep llama.cpp's own choice, which is already right
// there. An option that is always present cannot express "not configured".
func TestAnUnconfiguredEmbedderSendsNoThreadOption(t *testing.T) {
	got := captureRequest(t, 0)
	if _, present := got["options"]; present {
		t.Errorf("an unconfigured embedder sent %v; a host install with no CPU quota must leave "+
			"llama.cpp's own sizing alone", got["options"])
	}
}
