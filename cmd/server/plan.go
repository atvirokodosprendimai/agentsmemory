package main

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/urfave/cli/v3"
)

// setPlanCommand attaches a plan to a workspace by slug — the operator override
// behind "give this project unlimited requests". It defaults to the Unlimited
// tier (cap -1), which the metering path treats as uncapped, so it is the one-line
// way to comp an internal or partner workspace off the meter.
//
// Like `share`, it is a superadmin CLI, not an HTTP route: it runs against the same
// local SQLite the server uses, so possessing that database (shell access on the
// host) IS the authorization. There is no network auth because there is no network.
// Passing --plan reverses or re-targets the grant (e.g. --plan personal to un-comp,
// or --plan pro_monthly), so an operator who can grant can also revoke.
func setPlanCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "set-plan",
		Usage: "Attach a plan to a workspace by slug (default: unlimited = uncapped requests)",
		Flags: append(dataFlags(def),
			&cli.StringFlag{Name: "slug", Required: true, Usage: "workspace slug to change"},
			&cli.StringFlag{Name: "plan", Value: "unlimited", Usage: "plan code to attach (e.g. unlimited, personal, pro_monthly)"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			// urfave/cli v3 hands every subcommand the ROOT command's flags, and the
			// root is the serve action — so --monthly-request-cap parses here whatever
			// flag set this command declares, and no flag-surface change can take it
			// away. Refusing it is therefore the only honest option, and this is the
			// command where accepting it silently is worst: set-plan reports a durable
			// plan-cap change while a process override it neither uses nor persists
			// would, at serve time, ignore the plan it just wrote.
			if cap := c.Int("monthly-request-cap"); cap != 0 {
				return fmt.Errorf("--monthly-request-cap (%d) does nothing here: it is a serving policy "+
					"read by the server process, and set-plan writes the workspace's plan. Run it on "+
					"serve (or the mcp CLI), or drop it — this command would otherwise report a cap "+
					"change that the running server overrides", cap)
			}
			return setTeamPlan(ctx, configFromCmd(c, def), c.String("slug"), c.String("plan"))
		},
	}
}

// setTeamPlan resolves a workspace slug and a plan code, then flips the workspace's
// effective plan via tenant.SetTeamPlan — the single teams.plan_id column the
// metering path (PlanForTeam/MonthlyCap) reads, so the change takes effect with no
// other wiring. It reports the before/after plan and cap so the operator sees
// exactly what changed, rendering an uncapped plan as "unlimited" rather than "-1".
func setTeamPlan(ctx context.Context, cfg config.Config, slug, planCode string) error {
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	team, err := svc.tenants.TeamBySlug(ctx, slug)
	if err != nil {
		return fmt.Errorf("workspace %q: %w", slug, err)
	}
	target, err := svc.tenants.PlanByCode(ctx, planCode)
	if err != nil {
		return fmt.Errorf("plan %q: %w", planCode, err)
	}

	// Capture the current plan for a readable before->after line. A workspace with
	// no plan attached (ErrNoPlan) reads as "none" rather than failing the command.
	before := "none"
	if cur, err := svc.tenants.PlanForTeam(ctx, team.ID); err == nil {
		before = fmt.Sprintf("%s (cap %s)", cur.Name, capText(cur.MonthlyRequestCap))
	}

	if err := svc.tenants.SetTeamPlan(ctx, team.ID, target.ID); err != nil {
		return fmt.Errorf("set plan: %w", err)
	}
	log.Printf("set-plan: %s (%s): %s -> %s (cap %s)",
		slug, team.ID, before, target.Name, capText(target.MonthlyRequestCap))
	return nil
}

// capText renders a plan's monthly request cap for the CLI. A cap <= 0 is the
// unlimited sentinel (the enforcement path only meters when the cap is > 0), so it
// reads as "unlimited" — the plain-text counterpart of the dashboard's ∞.
func capText(cap int) string {
	if cap <= 0 {
		return "unlimited"
	}
	return strconv.Itoa(cap)
}
