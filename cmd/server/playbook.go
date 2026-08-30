// playbook.go implements `agentsmemory playbook`: read the global wake-up
// playbook out of a palace, and replace it with the one compiled into this
// binary.
//
// ⚠ IT EXISTS BECAUSE A LOCAL INSTALL HAD NO ROUTE TO ITS OWN PLAYBOOK AT ALL.
// Before this command the row had exactly two writers, and on a self-hosted
// server neither of them reached the operator:
//
//   - seedGlobalSkillset (main.go) writes only when the row is ABSENT, and that
//     is correct — it must never clobber a superadmin's authored text on restart.
//   - the dashboard editor (internal/web/skillset.go) requires a signed-in
//     superadmin, and serveLocal mounts only /mcp and /import, so in --local mode
//     the dashboard is not served at all.
//
// No MCP tool writes it either: am_update_skill is for skills, not the playbook.
// So a palace seeded once kept serving that text for as long as it lived, while
// the playbook compiled into the binary moved on underneath it. Measured
// 2026-08-30: a local row seeded on 2026-08-19 was still being served after the
// routing it lacked had shipped, merged, been rebuilt and reinstalled. The only
// way to change it was to stop the server and edit SQLite by hand from a
// throwaway container, because the image ships no sqlite3. That is not an
// operating procedure, and this command is what replaces it.
//
// ⚠ DELIBERATELY NOT A `doctor` FLAG. doctor's own description promises it "does
// not migrate the database, reconcile the search index, repair data, or run every
// mode by default". A repair verb hung off it would make that sentence false, and
// the sentence is the reason doctor can be run against production without
// thinking. Repair lives in its own command, like `drawer` and `wing`.
//
// Authorization is the same as `inspect`, `share` and `set-plan`: this runs
// against the SQLite the server uses, so possessing that database — shell access
// on the host — IS the authorization. There is no HTTP route.
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skillset"

	"github.com/urfave/cli/v3"
)

// seededBy is the provenance stamp this command writes, and it is deliberately
// the empty string — the same value seedGlobalSkillset leaves.
//
// updated_by is what separates "this text came from the binary" from "a human
// wrote this here", and that distinction is the whole of the --force guard below.
// Stamping a marker of our own would make every reseeded row look authored, so
// the second reseed would refuse for a reason the first one created.
const seededBy = ""

func playbookCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "playbook",
		Usage: "Show the global wake-up playbook, or reseed it from this binary",
		Description: "Without --reseed this is read-only: it prints the stored playbook's version,\n" +
			"size and provenance so you can see whether it matches the binary.\n\n" +
			"--reseed replaces the stored text with the one compiled into this binary. It\n" +
			"REFUSES when the row was authored by a human — a non-empty updated_by, which\n" +
			"is what the dashboard editor writes — because overwriting a superadmin's text\n" +
			"is the accident this guard exists to prevent. Pass --force to override that\n" +
			"refusal deliberately; --force does nothing else and is required for nothing else.",
		Flags: append(dataFlags(def),
			&cli.BoolFlag{Name: "reseed", Usage: "replace the stored playbook with the one compiled into this binary"},
			&cli.BoolFlag{Name: "force", Usage: "with --reseed, overwrite even a human-authored playbook (a non-empty updated_by)"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return runPlaybook(ctx, configFromCmd(c, def), c.Bool("reseed"), c.Bool("force"))
		},
	}
}

// runPlaybook reports the stored playbook and, with reseed set, replaces it.
//
// The write goes through skillset.Repo.Set — the same path the dashboard editor
// uses — rather than SQL, so the version bump and the updated_at stamp keep being
// decided in one place. A second writer that reimplemented them would drift.
func runPlaybook(ctx context.Context, cfg config.Config, reseed, force bool) error {
	// ⚠ THE READ-ONLY RUN MUST NOT MIGRATE. buildServices applies migrations and
	// reconciles the vector backend; inspectServices leaves both stores alone.
	// The first version used buildServices on BOTH branches, so an operator asking
	// only "does the stored playbook differ from this binary" mutated authoritative
	// and derived state to find out — with the command's own help calling the
	// default read-only. Found in review 2026-08-30.
	open := inspectServices
	if reseed {
		open = buildServices
	}
	svc, err := open(cfg)
	if err != nil {
		return err
	}
	repo := skillset.NewRepo(svc.gdb)

	stored, err := repo.Get(ctx)
	switch {
	case errors.Is(err, skillset.ErrNotSet):
		// Not an error: a palace that has never been seeded is the state the
		// server's own seed handles on boot, and saying so is more useful than
		// failing. Reseeding from here is still allowed — it is how you fill the
		// row without waiting for a restart.
		fmt.Println("stored   (none — this palace has never been seeded)")
	case err != nil:
		return fmt.Errorf("read the stored playbook: %w", err)
	default:
		fmt.Printf("stored   version %d, %d runes, updated %s\n", stored.Version, len([]rune(stored.Content)), stored.UpdatedAt)
		fmt.Printf("author   %s\n", authorText(stored.UpdatedBy))
	}
	fmt.Printf("binary   %d runes\n", len([]rune(skillset.DefaultPlaybook)))

	if !reseed {
		// Say what would change, so a read-only run answers the question the
		// operator actually has — "is this palace behind?" — rather than leaving
		// them to eyeball two rune counts.
		if err == nil && stored.Content == skillset.DefaultPlaybook {
			fmt.Println("\nthe stored playbook already matches this binary; --reseed would change nothing")
			return nil
		}
		fmt.Println("\nthe stored playbook DIFFERS from this binary — run again with --reseed to replace it")
		return nil
	}

	return reseedInto(ctx, repo, force)
}

// playbookStore is the slice of skillset.Repo the reseed needs, declared at the
// consumer so a test can drive the real decision against a real repo without
// this file growing a fake.
type playbookStore interface {
	Get(ctx context.Context) (skillset.Skillset, error)
	Set(ctx context.Context, content, updatedBy string) (skillset.Skillset, error)
	// SetIfSeeded is the unforced path: it writes only while the row is still
	// seeded, so a concurrent authored edit is refused rather than overwritten.
	SetIfSeeded(ctx context.Context, content string) (bool, error)
}

// reseedInto holds the guard and the write, in one place, so the test drives THIS
// decision rather than a copy of it. A test that reimplemented "refuse when
// authored" would stay green with the real refusal deleted, which is the shape
// this repository has been bitten by before.
func reseedInto(ctx context.Context, repo playbookStore, force bool) error {
	stored, err := repo.Get(ctx)
	authored := err == nil && stored.UpdatedBy != ""
	if err != nil && !errors.Is(err, skillset.ErrNotSet) {
		return fmt.Errorf("read the stored playbook: %w", err)
	}
	if authored && !force {
		return fmt.Errorf("the stored playbook was authored by %q, not seeded — refusing to overwrite it; "+
			"pass --force if replacing it with the binary's text is what you mean", stored.UpdatedBy)
	}

	// ⚠ THE UNFORCED WRITE IS A COMPARE-AND-SWAP, not a decision followed by a
	// write. Reading UpdatedBy and then calling Set left a window in which a
	// dashboard edit landed and was overwritten by the command whose whole promise
	// is that it will not do that. The database makes the decision now: the update
	// applies only while updated_by is still empty, and a zero row count means
	// somebody authored it in the meantime.
	if err == nil && !force {
		ok, serr := repo.SetIfSeeded(ctx, skillset.DefaultPlaybook)
		if serr != nil {
			return fmt.Errorf("reseed the playbook: %w", serr)
		}
		if !ok {
			return fmt.Errorf("the stored playbook was authored while this command was running, so " +
				"nothing was changed — re-read it and decide again, or pass --force")
		}
		after, gerr := repo.Get(ctx)
		if gerr != nil {
			return fmt.Errorf("read the reseeded playbook back: %w", gerr)
		}
		fmt.Printf("\nreseeded version %d, %d runes, updated %s\n",
			after.Version, len([]rune(after.Content)), after.UpdatedAt)
		return nil
	}

	next, err := repo.Set(ctx, skillset.DefaultPlaybook, seededBy)
	if err != nil {
		return fmt.Errorf("reseed the playbook: %w", err)
	}
	fmt.Printf("\nreseeded version %d, %d runes, updated %s\n", next.Version, len([]rune(next.Content)), next.UpdatedAt)
	return nil
}

// authorText renders the provenance stamp, naming the seeded case rather than
// printing an empty field a reader has to interpret.
func authorText(updatedBy string) string {
	if updatedBy == "" {
		// ⚠ "SEEDED" MEANS "not authored through the dashboard", which after a
		// forced reseed also covers a row this command replaced — that write
		// deliberately restores the empty stamp so the next reseed is not refused
		// for a reason the previous one created. It is not a claim that no human
		// ever ran anything against this row.
		return "(seeded — not edited through the dashboard)"
	}
	return updatedBy
}
