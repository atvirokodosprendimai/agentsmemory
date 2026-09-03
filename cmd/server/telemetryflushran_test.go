package main

import (
	"context"
	"errors"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"
	"github.com/urfave/cli/v3"
)

// TestTheFlushActuallyRuns pins that withTelemetry calls the shutdown it was
// handed, which nothing else in this package does.
//
// ⚠ THE SIBLING TEST LOOKS LIKE THIS COVER AND IS NOT.
// TestAStalledCollectorCannotHoldACommandOpen pins the BOUND, and pins it well —
// a real stall through two layers of provider shutdown. But its assertion is "the
// command is not held open", and **no flush at all satisfies that trivially**. So
// the two mutations are not symmetric:
//
//	remove the timeout, keep the flush -> command hangs ~60s  -> CAUGHT
//	remove the flush entirely          -> command exits fast  -> NOT caught
//
// Measured 2026-09-02 on this branch: deleting the whole defer left
// `go test ./cmd/server` green in 9s. The second failure is the one this PR exists
// to fix, and its symptom is silent span loss — indistinguishable from a command
// that was never traced.
func TestTheFlushActuallyRuns(t *testing.T) {
	t.Run("shutdown is called after the action returns", func(t *testing.T) {
		var flushed, actionRan bool
		restore := stubTelemetrySetup(t, func(context.Context) error {
			if !actionRan {
				t.Error("flushed BEFORE the action ran — spans the action creates would be lost")
			}
			flushed = true
			return nil
		})
		defer restore()

		err := runWithTelemetry(t, func(ctx context.Context, c *cli.Command) error {
			actionRan = true
			return nil
		})
		if err != nil {
			t.Fatalf("action: %v", err)
		}
		if !flushed {
			t.Error("the shutdown was never called — the OTLP path batches on a timer, so a " +
				"short-lived command returns before its spans leave")
		}
	})

	// ⚠ THE DEFER TAKES ITS DEADLINE FROM context.Background(), NOT FROM ctx, and
	// that is deliberate: a flush must still run when the action's own context is
	// already dead, which is the ordinary case the moment anything cancels it.
	// Deriving the deadline from a cancelled parent expires it instantly and drops
	// exactly the spans the defer exists to save.
	t.Run("shutdown is called even when the action's context is already cancelled", func(t *testing.T) {
		var flushed bool
		restore := stubTelemetrySetup(t, func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				t.Errorf("the flush context is already done (%v) — its deadline came from the "+
					"cancelled parent rather than from Background", err)
			}
			flushed = true
			return nil
		})
		defer restore()

		// ⚠ CANCEL THE CONTEXT withTelemetry ITSELF WAS GIVEN, not a child of it.
		// An earlier version of this subtest cancelled an inner context and left the
		// parent alive, so a flush deadline derived from `ctx` was still valid and
		// the mutation Background()->ctx SURVIVED while this test claimed to pin it.
		err := runWithTelemetryCancellable(t, func(ctx context.Context, c *cli.Command, cancel context.CancelFunc) error {
			cancel()
			<-ctx.Done()
			return nil
		})
		if err != nil {
			t.Fatalf("action: %v", err)
		}
		if !flushed {
			t.Error("no flush after an action whose context was cancelled")
		}
	})

	t.Run("a shutdown error does not change the command's exit status", func(t *testing.T) {
		restore := stubTelemetrySetup(t, func(context.Context) error {
			return errors.New("collector refused")
		})
		defer restore()

		if err := runWithTelemetry(t, func(context.Context, *cli.Command) error { return nil }); err != nil {
			t.Errorf("a failed flush changed the command's result: %v — ADR-025 makes this "+
				"instrument-health, so it is logged and the command's own status stands", err)
		}
	})
}

// stubTelemetrySetup swaps the package seam for one returning the given shutdown,
// and returns the restore. The endpoint is non-empty so the real Setup's "off"
// short-circuit cannot be what makes a case pass.
func stubTelemetrySetup(t *testing.T, shutdown func(context.Context) error) func() {
	t.Helper()
	prev := telemetrySetup
	telemetrySetup = func(context.Context, telemetry.Config) (func(context.Context) error, error) {
		return shutdown, nil
	}
	return func() { telemetrySetup = prev }
}

// runWithTelemetryCancellable is runWithTelemetry with the action handed the
// cancel for the context the COMMAND is running under, so a test can kill the
// parent rather than a child of it.
func runWithTelemetryCancellable(t *testing.T, action func(context.Context, *cli.Command, context.CancelFunc) error) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	def := config.Default()
	def.OTELEndpoint = "http://127.0.0.1:1/v1/traces"
	cmd := &cli.Command{
		Name:  "probe",
		Flags: dataFlags(def),
		Action: withTelemetry(def, func(inner context.Context, c *cli.Command) error {
			return action(inner, c, cancel)
		}),
	}
	return cmd.Run(ctx, []string{"probe"})
}

// runWithTelemetry drives the real withTelemetry wrapper over a command carrying
// a real --otel-endpoint, so the value resolves through configFromCmd exactly as
// it does in production.
func runWithTelemetry(t *testing.T, action cli.ActionFunc) error {
	t.Helper()
	def := config.Default()
	def.OTELEndpoint = "http://127.0.0.1:1/v1/traces"
	cmd := &cli.Command{
		Name:   "probe",
		Flags:  dataFlags(def),
		Action: withTelemetry(def, action),
	}
	return cmd.Run(context.Background(), []string{"probe"})
}
