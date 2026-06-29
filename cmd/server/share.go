package main

import (
	"context"
	"fmt"
	"log"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/urfave/cli/v3"
)

// shareCommand copies a wing's memory from one workspace into another — the
// solo-to-team path: you started alone, now there's a team workspace (a new
// account, enterprise plan), and you want that project's wing in it WITHOUT moving
// the rest of your memory. It reuses the stored vectors (SQLite is the source of
// truth), so nothing is re-embedded; records are re-keyed for the destination, so
// re-running refreshes rather than duplicates.
func shareCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "share",
		Usage: "Copy a wing's memory (drawers + closets) from one workspace into another, reusing stored vectors (no re-embedding)",
		Flags: append(dataFlags(def),
			&cli.StringFlag{Name: "from", Required: true, Usage: "source workspace slug"},
			&cli.StringFlag{Name: "to", Required: true, Usage: "destination workspace slug"},
			&cli.StringSliceFlag{Name: "wing", Required: true, Usage: "wing to copy (repeat --wing for several)"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return shareWings(ctx, configFromCmd(c, def), c.String("from"), c.String("to"), c.StringSlice("wing"))
		},
	}
}

// shareWings resolves the two workspace slugs and copies each named wing from the
// source tenant to the destination tenant.
func shareWings(ctx context.Context, cfg config.Config, fromSlug, toSlug string, wings []string) error {
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	from, err := svc.tenants.TeamBySlug(ctx, fromSlug)
	if err != nil {
		return fmt.Errorf("source workspace %q: %w", fromSlug, err)
	}
	to, err := svc.tenants.TeamBySlug(ctx, toSlug)
	if err != nil {
		return fmt.Errorf("destination workspace %q: %w", toSlug, err)
	}
	if from.ID == to.ID {
		return fmt.Errorf("source and destination are the same workspace (%q)", fromSlug)
	}

	// A point-in-time snapshot: it pages the source wing, so it copies what is
	// present (and embedded) at run time. Run it with the source wing quiesced for a
	// complete copy; re-running is idempotent and reconciles any later changes.
	log.Printf("share: %s -> %s, wing(s): %v", fromSlug, toSlug, wings)
	totalSkipped := 0
	for _, wing := range wings {
		res, err := svc.drawers.CopyWing(ctx, from.ID, to.ID, wing)
		if err != nil {
			return fmt.Errorf("copy wing %q: %w", wing, err)
		}
		log.Printf("share: wing %q copied — drawers=%d closets=%d skipped=%d",
			wing, res.Drawers, res.Closets, res.Skipped)
		totalSkipped += res.Skipped
		if res.Skipped > 0 {
			// Skipped = records whose vector is not yet built in the source (still
			// pending background embedding). They were NOT copied — this is a partial
			// copy, so make it loud rather than a buried counter.
			log.Printf("share: WARNING wing %q: %d record(s) skipped (no vector yet in the source). "+
				"Let the source finish embedding, then re-run share to copy them (idempotent).", wing, res.Skipped)
		}
	}
	if totalSkipped > 0 {
		return fmt.Errorf("share completed with %d record(s) skipped (source not fully embedded) — re-run after the source finishes indexing", totalSkipped)
	}
	log.Printf("share: done (target %q now searchable for the copied wing(s))", toSlug)
	return nil
}
