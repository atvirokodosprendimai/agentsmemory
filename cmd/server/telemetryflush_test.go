package main

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"

	"github.com/urfave/cli/v3"
)

// TestAStalledCollectorCannotHoldACommandOpen pins the bound on the telemetry
// flush, and it exists because the fail-open test next door cannot see it.
//
// TestTelemetrySetupFailureDoesNotFailTheCommand passes "://not-a-url", which
// fails inside telemetry.Setup — so it returns down the error branch before any
// provider exists, and the deferred shutdown it never reaches is exactly the code
// path that can hang. That gap was found reviewing PR #138: a collector whose URL
// is perfectly valid, and which accepts a connection and then says nothing, is
// the case neither the setup branch nor a healthy collector exercises.
//
// Unbounded, the cost is not a slow test but a minute of an operator's time.
// telemetry.Setup's shutdown closes the metric provider and then the trace
// provider, sequentially, each honouring the context it is handed; OTel's default
// export timeout is 30s, so two of them run back to back on a command that has
// already finished its work and printed its answer.
//
// The stall is built rather than mocked, because what is under test is the
// deadline reaching a real exporter through two layers of provider shutdown. A
// fake that returned instantly would pass with the bound removed, which is the
// one thing this test has to notice.
func TestAStalledCollectorCannotHoldACommandOpen(t *testing.T) {
	// Accepts the connection, then holds it open and never writes a response —
	// the shape that defeats a connection-refused check and forces the exporter
	// to wait out its own timeout.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				<-stop
				_ = conn.Close()
			}()
		}
	}()

	var ran bool
	action := withTelemetry(config.Default(), func(ctx context.Context, c *cli.Command) error {
		ran = true
		return nil
	})
	cmd := &cli.Command{
		Name:   "probe",
		Flags:  dataFlags(config.Default()),
		Action: action,
	}

	// A URL the exporter constructs happily and can connect to. Only the response
	// never comes.
	endpoint := "http://" + ln.Addr().String()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- cmd.Run(context.Background(), []string{"probe", "--otel-endpoint", endpoint})
	}()

	// Generous against the bound and far below what an unbounded flush costs, so
	// the test is not timing-sensitive in either direction: it fails only if the
	// deadline is gone entirely, never because a loaded machine ran slow.
	const allowed = 20 * time.Second
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a stalled collector must not fail the command: %v", err)
		}
	case <-time.After(allowed):
		t.Fatalf("the command was still running %s after its action finished.\n"+
			"  withTelemetry's deferred shutdown is unbounded again: telemetry.Setup shuts the\n"+
			"  metric and trace providers down one after the other, each with OTel's 30s default\n"+
			"  export timeout, so a collector that accepts and stalls costs about a minute.\n"+
			"  Pass a context bounded by telemetryFlushTimeout to shutdown.", allowed)
	}

	if !ran {
		t.Error("action did not run")
	}
	// The elapsed time is reported rather than asserted at the bound. Asserting a
	// floor here would pin OTel's retry behaviour, which is not this repository's
	// to promise; the ceiling above is the whole claim.
	t.Logf("command returned in %s (flush bound is %s)", time.Since(start).Round(time.Millisecond), telemetryFlushTimeout)
}
