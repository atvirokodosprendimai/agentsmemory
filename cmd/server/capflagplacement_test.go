package main

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"

	"github.com/urfave/cli/v3"
)

// capOverrideFlag is the serving policy under test.
const capOverrideFlag = "monthly-request-cap"

// meteringCommands are the command paths whose OWN flag set may declare the cap
// override, each with the reason it earns it. Listing without a reason is what
// AGENTS.md refuses, so the reason is the review.
var meteringCommands = map[string]string{
	"agentsmemory":       "the root command IS the serve action (bare run + Docker CMD), so it enforces the cap on every metered request",
	"agentsmemory serve": "the explicit serving entry point, same action as the root",
	"agentsmemory mcp":   "--token here resolves a tenant and meters the call for HTTP parity, so the override changes what this command does",
}

// TestTheCapOverrideIsOnlyDeclaredWhereItIsEnforced walks the REAL command tree
// and fails when a command declares the cap override in its own flag set without
// metering anything.
//
// It was added because --monthly-request-cap lived in dataFlags, which every
// command that opens the database reuses — doctor, wing export, set-plan and
// eight more. Those construct a usage service and never meter a request through
// it, so the accepted option changed no result they produce: this repo's
// documented reachability failure in its configuration form, where parsing into a
// Config field is mistaken for having an effect in the mode that is running
// (ADR-006).
//
// ⚠ WHAT THIS TEST CANNOT DO, stated because the obvious reading of a green run
// is wrong: it does NOT establish that `doctor --monthly-request-cap=5` is
// rejected. urfave/cli v3 hands every subcommand the ROOT command\'s flags, and
// the root must carry this one because the root is the serve action — so the flag
// parses on every subcommand no matter which flag set declares it, and
// `doctor --addr=:9999` parses for the identical reason. Measured on a built
// binary, before and after the move. What the move buys is that no command\'s own
// surface claims a policy it does not apply; where accepting it silently is
// actively misleading, the command refuses it (see set-plan).
func TestTheCapOverrideIsOnlyDeclaredWhereItIsEnforced(t *testing.T) {
	var declaring []string
	var walk func(path string, cmd *cli.Command)
	walk = func(path string, cmd *cli.Command) {
		for _, f := range cmd.Flags {
			for _, name := range f.Names() {
				if name == capOverrideFlag {
					declaring = append(declaring, path)
				}
			}
		}
		for _, sub := range cmd.Commands {
			walk(path+" "+sub.Name, sub)
		}
	}
	root := rootCommand(config.Default())
	walk(root.Name, root)

	if len(declaring) == 0 {
		t.Fatal("no command declares --" + capOverrideFlag + " — an operator cannot set it at all, " +
			"and this check has stopped checking anything")
	}
	sort.Strings(declaring)
	for _, path := range declaring {
		if _, ok := meteringCommands[path]; !ok {
			t.Errorf("%q declares --%s and meters nothing, so the option it accepts changes no result "+
				"it produces. Put the serving policy on the metering surface, or add %q to "+
				"meteringCommands WITH the reason it enforces the cap",
				path, capOverrideFlag, path)
		}
	}
	for path, why := range meteringCommands {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%q is exempted with no written reason", path)
		}
	}
}

// TestSetPlanRefusesAServingOverride is the half the placement test cannot cover.
//
// Because root flags reach every subcommand, `set-plan --monthly-request-cap=N`
// parses whatever flag set declares it — and set-plan is where accepting it
// silently does real harm: it reports a durable plan-cap change while the process
// override it neither uses nor persists would, at serve time, ignore the plan it
// just wrote. An operator reads a confident before->after line and gets neither.
func TestSetPlanRefusesAServingOverride(t *testing.T) {
	cmd := rootCommand(config.Default())
	err := cmd.Run(context.Background(), []string{
		"agentsmemory", "set-plan", "--slug", "acme", "--" + capOverrideFlag, "7",
	})
	if err == nil {
		t.Fatal("set-plan accepted --" + capOverrideFlag + ": it would report a plan-cap change that " +
			"the running server overrides, with nothing telling the operator so")
	}
	if !strings.Contains(err.Error(), capOverrideFlag) {
		t.Errorf("set-plan failed for some other reason, so this proves nothing about the override: %v", err)
	}
}
