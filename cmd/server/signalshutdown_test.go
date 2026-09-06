package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

// Issue #140. Nothing flushed telemetry on SIGINT or SIGTERM, and three things
// had to be true for it to work — none of which was: `main` ran on
// context.Background so no cancellation ever arrived; nothing anywhere in
// cmd/server mentioned signal handling; and serveHTTP blocked in http.Serve,
// which ignores its context entirely. The serving process — the one emitting by
// far the most spans — dropped whatever the OTLP batcher had accumulated since
// its last 2s tick, on the ordinary container and systemd stop path, silently.
//
// These tests pin the return path rather than the flush: withTelemetry's deferred
// shutdown is already covered, and it can only run if the action RETURNS.

// TestServeHTTPReturnsWhenItsContextIsCancelled is the one that would have failed
// before the change and passes now.
func TestServeHTTPReturnsWhenItsContextIsCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, ln, http.NotFoundHandler()) }()

	// Serve must actually be up first, or a return could mean "never started".
	waitUntilServing(t, ln.Addr().String())

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a cancelled serve returned %v; a signal is an ordinary stop and must not "+
				"look like a failure to the supervisor", err)
		}
	case <-time.After(shutdownGrace + 5*time.Second):
		t.Fatal("serveHTTP did not return after its context was cancelled — this is issue #140: " +
			"the action never returns, so withTelemetry's deferred flush never runs and the last " +
			"batch of spans dies with the process")
	}
}

// TestACancelledServeReachesTheTelemetryFlush is the composition. The test above
// proves serveHTTP returns; this proves the return travels through the seam that
// flushes, which is the only reason the return is worth having.
func TestACancelledServeReachesTheTelemetryFlush(t *testing.T) {
	flushed := make(chan struct{}, 1)
	restore := stubTelemetrySetup(t, func(context.Context) error {
		flushed <- struct{}{}
		return nil
	})
	defer restore()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	err = runWithTelemetryCancellable(t, func(ctx context.Context, _ *cli.Command, cancel context.CancelFunc) error {
		go func() {
			waitUntilServing(t, ln.Addr().String())
			cancel()
		}()
		return serveHTTP(ctx, ln, http.NotFoundHandler())
	})
	if err != nil {
		t.Errorf("the cancelled serving action returned %v", err)
	}
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Error("the telemetry shutdown never ran for a serving action stopped by cancellation; " +
			"that is the whole of issue #140 — the spans exist, the exporter is configured, and " +
			"the batch dies unflushed")
	}
}

// TestACancelledServeDrainsAnInFlightRequest holds the word "gracefully" to
// something. Shutdown with an already-cancelled context returns instantly and
// cuts every live connection, which looks identical from the outside to a clean
// stop — so the drain context is derived from Background, and this is what says
// so in a way that fails if somebody "simplifies" it back to ctx.
func TestACancelledServeDrainsAnInFlightRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	entered := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "finished")
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, ln, slow) }()

	type result struct {
		body string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			got <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		got <- result{body: string(b), err: err}
	}()

	<-entered // the handler is running; now stop the server under it
	cancel()

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("the in-flight request failed during shutdown: %v — the drain context is "+
				"derived from Background precisely so this cannot happen", r.err)
		}
		if r.body != "finished" {
			t.Errorf("the in-flight request returned %q, not the handler's full response", r.body)
		}
	case <-time.After(shutdownGrace + 5*time.Second):
		t.Fatal("the in-flight request never completed")
	}
	select {
	case <-done:
	case <-time.After(shutdownGrace + 5*time.Second):
		t.Fatal("serveHTTP did not return after draining")
	}
}

// TestTheSecondSignalIsNotSwallowed reads main's own source, because the property
// is about a HANDLER REGISTRATION and no in-process test can send itself a
// SIGTERM without ending the test binary.
//
// ⚠ THE DEFERRED stop() IS NOT ENOUGH ON ITS OWN, and the first draft of this
// change shipped a comment claiming it was. signal.NotifyContext keeps the
// handler installed until stop() runs, and a deferred one runs when main returns
// — so throughout the drain a second SIGTERM is caught and DISCARDED. An operator
// who sends it because the first looked ignored gets nothing. Calling stop() as
// soon as the context is cancelled restores the default disposition.
func TestTheSecondSignalIsNotSwallowed(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	var notifies, restoresAfterCancel bool
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "NotifyContext" {
				notifies = true
			}
			// A goroutine that waits for the context and then calls stop.
			gs, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			var waits, stops bool
			ast.Inspect(gs, func(inner ast.Node) bool {
				switch v := inner.(type) {
				case *ast.SelectorExpr:
					if v.Sel.Name == "Done" {
						waits = true
					}
				case *ast.CallExpr:
					if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "stop" {
						stops = true
					}
				}
				return true
			})
			if waits && stops {
				restoresAfterCancel = true
			}
			return true
		})
	}
	if !notifies {
		t.Fatal("main does not install a signal-aware context, so SIGTERM kills the process " +
			"outright and the telemetry flush never runs (issue #140)")
	}
	if !restoresAfterCancel {
		t.Error("main installs the signal handler and never restores the default disposition " +
			"after the first signal, so a SECOND SIGTERM is swallowed for the whole drain. " +
			"A deferred stop() does not do this: it runs when main returns, which is after the " +
			"window that matters")
	}
}

// waitUntilServing dials until the listener answers, so a test cannot mistake
// "returned because it never started" for "returned because it was told to".
func waitUntilServing(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("nothing answered on %s", addr)
}
