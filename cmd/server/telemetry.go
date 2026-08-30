// telemetry.go wires OpenTelemetry to the CLI at exactly one place, and routes
// every command through it.
//
// Issue #53: --otel-endpoint is declared in dataFlags, so it appears in the help
// of a dozen commands, while telemetry.Setup was called from run() and runEval()
// only. On every other command the flag parsed, landed in cfg.OTELEndpoint, and
// no TracerProvider or MeterProvider was installed — so every span became a
// nonRecordingSpan and nothing was recorded or exported, with no signal that the
// flag had been seen. `agentsmemory mcp` was the worst one to lose: it runs the
// production handlers, so it is the out-of-band way to reproduce a ranking
// complaint, and README sells `stdout` for exactly that.
//
// The remedy is the shape productionMCPServer already uses and ADR-006 asks for:
// ONE seam every entry point routes through, rather than a per-command call
// somebody has to remember to add. A subcommand written tomorrow is instrumented
// by wrapTelemetry walking the tree, not by a reviewer noticing. The alternative
// considered and rejected was a gate that charges each command for installing a
// provider itself — it needs a per-command exemption list, since `wing`, `share`
// and `kg-extract` do no instrumented work, and this repository's own defect
// list is what a list kept beside the truth costs.
package main

import (
	"context"
	"log"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"github.com/urfave/cli/v3"
)

// withTelemetry returns action with an OpenTelemetry provider installed for its
// whole run and flushed afterwards, so any span the command creates is exported
// instead of dropped.
//
// A nil action is returned unchanged: parent commands like `wing` carry
// subcommands and no action of their own, and wrapping nil would turn urfave's
// "show the help" behaviour into a call through a nil function.
//
// Export failure is deliberately not fatal. ADR-025: a collector that refuses is
// instrument-health, not a reason to refuse the work, so the command runs
// untraced and says so.
func withTelemetry(def config.Config, action cli.ActionFunc) cli.ActionFunc {
	if action == nil {
		return nil
	}
	return func(ctx context.Context, c *cli.Command) error {
		// Through configFromCmd, not c.String, so --otel-endpoint resolves its
		// value, its env var and its default exactly like every other setting.
		// An endpoint of "" is the honest answer for a command that offers no
		// such flag: telemetry.Setup treats it as off and returns a no-op
		// shutdown without touching the global providers, so a command with
		// nothing to export pays nothing.
		endpoint := configFromCmd(c, def).OTELEndpoint
		shutdown, err := telemetry.Setup(ctx, telemetry.Config{Endpoint: endpoint})
		if err != nil {
			log.Printf("otel: setup failed (%v) — running without traces", err)
			return action(ctx, c)
		}
		// Flush before the process exits. The OTLP path batches on a 2s timer, so
		// a short-lived subcommand that returned straight to main would drop its
		// spans on the floor — which is indistinguishable from the bug above.
		defer func() { _ = shutdown(context.Background()) }()
		if endpoint != "" {
			log.Printf("otel: exporting traces to %s", endpoint)
		}
		return action(ctx, c)
	}
}

// wrapTelemetry routes every action in cmd's tree, cmd's own included, through
// withTelemetry.
//
// Walking the tree is the point rather than an implementation detail: it makes
// instrumentation a property of being IN the command tree, which is a thing
// rootCommand's own test can check, instead of a line each new subcommand must
// remember to write. TestEveryActionInTheCommandTreeIsWrapped reads the real
// tree and fails on the first action that is not.
func wrapTelemetry(cmd *cli.Command, def config.Config) {
	cmd.Action = withTelemetry(def, cmd.Action)
	for _, sub := range cmd.Commands {
		wrapTelemetry(sub, def)
	}
}
