package main

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// TestTheDuplicatedRerankPoolDefaultMatchesItsSource pins config.Default()'s
// RerankPool to palace.DefaultRerankPool, the constant it is a copy of.
//
// The copy is deliberate and its comment says why — internal/config is kept free
// of a dependency on internal/palace, so the number is typed twice rather than
// imported. What was missing is the half that makes a deliberate copy safe: NOTHING
// compared them. Measured 2026-09-07 by raising palace.DefaultRerankPool to 11 and
// leaving config at 10 — `go test ./...` exit 0, whole suite green, over a server
// whose documented default and whose actual default disagreed. config_test.go's
// existing check compares cfg.RerankPool to config.Default().RerankPool, which is
// the same package answering its own question and cannot see the drift.
//
// This test lives in cmd/server because that is the composition root: it is the one
// package that already imports both, so the gate costs no new dependency edge —
// which is the whole reason the duplicate exists.
//
// ⚠ A COPY WITH NO COMPARISON IS NOT A COPY, IT IS A SECOND SOURCE OF TRUTH. The
// direction that bites is silent: raising the palace constant is what an operator
// tuning latency would do, and the config default is what --help prints and what an
// unset flag resolves to, so the two diverge exactly when someone is paying
// attention to the number.
func TestTheDuplicatedRerankPoolDefaultMatchesItsSource(t *testing.T) {
	if got, want := config.Default().RerankPool, palace.DefaultRerankPool; got != want {
		t.Errorf("config.Default().RerankPool = %d, palace.DefaultRerankPool = %d.\n"+
			"  internal/config duplicates this constant to stay free of a dependency on "+
			"internal/palace, so the two must be compared somewhere or the copy drifts in "+
			"silence: the flag default an operator sees would stop being the pool the "+
			"search actually uses.", got, want)
	}
}
