// drawer.go implements `agentsmemory drawer erase`: the operator's path to
// destroying ONE memory.
//
// It exists because ADR-038 took erasure off the agent surface. An agent that
// finds a memory wrong now retracts it — the text survives, ended, with a reason —
// and that is right for every case except one: a memory nobody is allowed to keep.
// A leaked secret, a name filed by mistake, a paste that carried a token. A store
// that cannot forget those is not deployable, so the verb still exists; it just
// requires the database file, which is the same authorization `wing delete`,
// `inspect` and `share` already use.
//
// The separation is the point. An agent has a reason to retract dozens of times a
// month and no reason to erase; an operator erases rarely and deliberately. Giving
// both to the same caller is what let a model reach for a delete when it meant a
// correction.
package main

import (
	"context"
	"fmt"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"

	"github.com/urfave/cli/v3"
)

// drawerCommand groups the operator-only drawer verbs.
func drawerCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "drawer",
		Usage: "Operator actions on a single memory",
		Commands: []*cli.Command{
			drawerEraseCommand(def),
		},
	}
}

// drawerEraseCommand permanently destroys one memory, every chunk of it.
func drawerEraseCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "erase",
		Usage: "Permanently destroy one memory — every chunk, its vectors and its anchors",
		Description: "This is NOT the way to correct a memory. An agent retracts with\n" +
			"am_invalidate_drawer or supersedes with am_update_drawer, and either keeps the old\n" +
			"text readable with a reason attached — which is what a reader six months from now\n" +
			"needs. Erase exists for the memory nobody is allowed to keep: a leaked secret, a\n" +
			"name filed by mistake. It cannot be undone and nothing records that it happened.\n\n" +
			"--confirm must repeat the id exactly. Take it from what you were asked to erase,\n" +
			"not from the --id argument you just typed.",
		Flags: append(dataFlags(def),
			projectFlag(),
			&cli.StringFlag{Name: "id", Required: true, Usage: "any chunk's id of the memory to erase"},
			&cli.StringFlag{Name: "confirm", Required: true, Usage: "repeat the id to confirm the erase"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return eraseDrawer(ctx, configFromCmd(c, def), c.String("project"), c.String("id"), c.String("confirm"))
		},
	}
}

// eraseDrawer resolves the workspace and destroys the memory, reporting what went.
func eraseDrawer(ctx context.Context, cfg config.Config, slug, id, confirm string) error {
	if confirm != id {
		// Refused BEFORE the workspace is opened, so a mistyped confirmation costs
		// nothing and reads the same whether or not the id exists.
		return fmt.Errorf("--confirm %q does not match --id %q; nothing was erased", confirm, id)
	}
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}

	// Read it first, and PRINT it. An erase is unrecoverable and unlogged, so the
	// one moment the operator can still notice they named the wrong memory is
	// before it goes — and the terminal scrollback is then the only trace that
	// anything was there at all.
	d, err := svc.drawers.Get(ctx, team.ID, id)
	if err != nil {
		return err
	}
	fmt.Printf("erasing from %q: wing=%q room=%q\n%s\n", team.Slug, d.Wing, d.Room, d.Content)

	n, err := svc.drawers.Delete(ctx, team.ID, id)
	if err != nil {
		return err
	}
	fmt.Printf("erased %d chunk(s). This is not recorded anywhere; nothing will say the memory existed.\n", n)
	return nil
}
