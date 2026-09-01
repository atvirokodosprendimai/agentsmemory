package main

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
)

// TestAFractionalCPUQuotaStillStartsTheServer pins the boot path against Docker's
// own value space.
//
// docker-compose.ollama.yml passes AGENTSMEMORY_OLLAMA_CPUS to the server so the
// thread pool cannot drift from the quota, and a Docker CPU quota is
// FRACTIONAL — Docker's own refusal names a range "from 0.01 to N.00". Parsed as
// an integer, a valid `cpus: 0.5` made the flag unparseable and the server exited
// before serving anything: a fix for slow embedding turned into a boot failure.
// Caught in review before it shipped.
func TestAFractionalCPUQuotaStillStartsTheServer(t *testing.T) {
	for in, want := range map[string]int{
		"0.5":  1, // half a core still runs one thread
		"0.01": 1,
		"2":    2,
		"12":   12,
		"2.9":  2, // floor: a thread is not divisible
	} {
		if got := threadsFromQuota(in); got != want {
			t.Errorf("threadsFromQuota(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestAnUnusableThreadValueMeansUnset covers the other direction. A negative or
// unparseable value must read as "let llama.cpp choose" rather than as a thread
// count, and it must not stop the server: the option is an optimisation, and
// refusing to boot over one trades a slow server for no server.
func TestAnUnusableThreadValueMeansUnset(t *testing.T) {
	for _, in := range []string{"", "0", "-1", "-0.5", "auto", "two"} {
		if got := threadsFromQuota(in); got != 0 {
			t.Errorf("threadsFromQuota(%q) = %d, want 0 — a value that is not a usable count must "+
				"leave llama.cpp's own sizing alone rather than sending a nonsense option", in, got)
		}
	}
}

// TestAnExplicitThreadCountOutranksTheQuota pins the precedence the documentation
// promises. The quota is a FALLBACK: the overlay passes the limit it applies so an
// operator need not repeat it, but an operator who names a thread count in
// .env.docker has said something more specific, and the overlay must not outrank
// their file — which is the whole of #154 restated one knob over.
func TestAnExplicitThreadCountOutranksTheQuota(t *testing.T) {
	cfg := config.Default()
	cfg.OllamaNumThread = 6
	cfg.OllamaCPUQuota = "2"
	if got := threadsFor(cfg); got != 6 {
		t.Errorf("threads = %d with an explicit 6 beside a quota of 2, want 6 — the derived "+
			"fallback overruled the operator's own value", got)
	}
	cfg.OllamaNumThread = 0
	if got := threadsFor(cfg); got != 2 {
		t.Errorf("threads = %d with no explicit count and a quota of 2, want 2 — the quota is "+
			"the fallback and it did not apply", got)
	}
}
