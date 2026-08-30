package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck"
)

// updateCheckTimeout bounds the whole round trip to GitHub. It is short because
// nothing waits on this — the check runs in a goroutine beside a server that is
// already listening — and a check still running minutes into a process's life is
// answering a question nobody is still asking.
const updateCheckTimeout = 5 * time.Second

// announceUpdate prints one line when a newer release exists, and nothing at all
// otherwise. Run it in a goroutine: it makes a network call, and the listening
// line must never be gated on GitHub being reachable.
//
// It is deliberately silent on every failure — offline, rate-limited, malformed,
// timed out — and on dev builds, which have no tag to compare. See the package
// comment of internal/updatecheck for why an update check that can complain is
// worse than none: this is a nicety, and ADR-025's position is that an external
// dependency failing drops the nicety rather than degrading the service.
//
// A dev build skips the request entirely rather than making one whose answer it
// would discard, which is also what keeps `go test ./cmd/server` off the network.
func announceUpdate(ctx context.Context, current string) {
	if !updatecheck.IsRelease(current) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()

	client := &http.Client{Timeout: updateCheckTimeout}
	if notice := updatecheck.Notice(ctx, client, updatecheck.DefaultAPIURL, current); notice != "" {
		log.Print(notice)
	}
}
