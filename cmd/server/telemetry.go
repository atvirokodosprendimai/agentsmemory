// telemetry.go wires OpenTelemetry to the CLI at exactly one place, and routes
// every command through it.
//
// Issue #53: --otel-endpoint is declared in dataFlags, so it appears in the help
// of every command carrying those flags, while telemetry.Setup was called from
// run() and runEval() only. (No count is written here on purpose — an earlier
// draft of this comment and of AGENTS.md both froze one, and review of PR #138
// found the AGENTS.md figure already stale in the section that bans frozen
// counts. TestEveryActionInTheCommandTreeIsWrapped names the live set when it
// fails.)
// On every other command the flag parsed, landed in cfg.OTELEndpoint, and
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
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"github.com/urfave/cli/v3"
)

// telemetryFlushTimeout bounds how long a finished command will wait for its
// spans to reach the collector before giving up and exiting.
//
// The number is chosen against what it is protecting, not against a round
// figure. The OTLP path batches on a 2s timer, so the flush this bounds is
// normally one export of one batch — sub-second against a healthy collector, and
// five seconds is generous for it. What it must not do is inherit OTel's own
// default: the metric and trace providers shut down one after the other, each
// with a 30s export timeout, so an unbounded flush against a collector that
// accepts a connection and then stalls costs a minute on a command that has
// already done its work and printed its answer.
//
// It is deliberately NOT configurable. A knob here would be one more setting an
// operator can set to something that reintroduces the hang, and this repository's
// own defect list is what unread and half-wired knobs cost.
const telemetryFlushTimeout = 5 * time.Second

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
		//
		// ⚠ BOUNDED, and from Background rather than from ctx. Two separate
		// reasons, and dropping either one reintroduces a hang:
		//
		// Bounded, because telemetry.Setup's shutdown closes the metric and trace
		// providers SEQUENTIALLY and each honours the context it is given — so an
		// unbounded one lets a syntactically valid but unresponsive collector hold
		// a short-lived command for two OTel export timeouts back to back. Review
		// of PR #138 caught this: it was already true when two commands installed
		// providers, and this PR is what put it on every command in the tree.
		//
		// From Background, because a flush must still run when the action's own
		// context is already dead — which is the ordinary case the moment anything
		// cancels ctx. Deriving the flush deadline from a cancelled parent expires
		// it instantly and drops exactly the spans this defer exists to save.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
			defer cancel()
			if err := shutdown(ctx); err != nil {
				// ADR-025 again: a flush that could not complete is
				// instrument-health. Say so and let the command's own exit
				// status stand.
				log.Printf("otel: flush failed (%v) — some spans were not exported", err)
			}
		}()
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
