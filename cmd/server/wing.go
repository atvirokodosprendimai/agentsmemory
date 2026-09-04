// wing.go implements `agentsmemory wing export`, `wing import` and `wing delete`:
// moving one wing of a palace in and out of a portable file, and removing one.
//
// They all talk to the SQLite database directly rather than to a running server,
// which is what makes them the answer for `--local`. A self-hosted install has
// no dashboard to click and often no token to present, but it always has the
// database file — so possessing that file IS the authorization, exactly as it
// already is for `inspect`, `share` and `set-plan`. They also work against a
// server that is currently running: SQLite in WAL mode admits a second reader,
// and an import writes rows the running server picks up.
//
// Export and import are deliberately asymmetric about wings. Export takes `--wing`
// and produces a file that names no wing at all; import takes `--as` and decides
// where that file lands. That is the whole feature: a bundle is contents, not a
// place, so the same file can be restored beside its original, renamed on the
// way into a fresh palace, or forked into several wings.
//
// Delete is the pair's counterweight, and export is its undo: a bundle written
// before a delete is the only way back, which is why the command says so.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/importer"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle"

	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

// The repository is the export's data source; asserting it here makes the
// coupling a compile error rather than a runtime surprise if either side moves.
var _ wingbundle.Source = (*palace.Repo)(nil)

// wingCommand groups whole-wing operations under one verb, so
// `agentsmemory wing --help` reads as a single feature rather than unrelated
// commands that happen to share a subject.
func wingCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "wing",
		Usage: "Move one wing in or out of a portable bundle file, or delete one",
		Commands: []*cli.Command{
			wingExportCommand(def),
			wingImportCommand(def),
			wingDeleteCommand(def),
		},
	}
}

// projectFlag names the workspace both subcommands act on. It defaults to the
// single workspace `--local` provisions, so a self-hoster never types it; on a
// multi-tenant database it selects one workspace by the slug the dashboard shows.
func projectFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "project",
		Value: tenant.LocalSlug,
		Usage: "workspace slug to act on (defaults to the single --local workspace; run `agentsmemory projects` to list them)",
	}
}

// wingExportCommand writes one wing to a bundle file or to stdout.
func wingExportCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "export",
		Usage: "Write one wing's drawers, closets and intra-wing tunnels to a portable bundle",
		Description: "The bundle records no wing name — `wing import --as <name>` decides where it lands.\n" +
			"It is text only: the destination re-embeds, so a bundle stays portable across embedding models.",
		Flags: append(dataFlags(def),
			projectFlag(),
			&cli.StringFlag{Name: "wing", Required: true, Usage: "wing to export"},
			&cli.StringFlag{Name: "out", Usage: "file to write (default: stdout, so the bundle can be piped)"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return exportWing(ctx, configFromCmd(c, def), c.String("project"), c.String("wing"), c.String("out"))
		},
	}
}

// wingImportCommand files a bundle into a named wing.
func wingImportCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "File a bundle into a wing, named by --as",
		Description: "--as is required. A bundle carries no wing of its own, so importing without naming a\n" +
			"destination would file every memory into an unnamed wing — an import that appears to\n" +
			"succeed and leaves the memories somewhere nobody looks.",
		Flags: append(dataFlags(def),
			projectFlag(),
			&cli.StringFlag{Name: "file", Required: true, Usage: "bundle file to import (- for stdin)"},
			&cli.StringFlag{Name: "as", Required: true, Usage: "wing to file every record into (created if absent)"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return importWing(ctx, configFromCmd(c, def), c.String("project"), c.String("file"), c.String("as"))
		},
	}
}

// wingDeleteCommand permanently removes one wing.
func wingDeleteCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "delete",
		Usage: "Permanently delete one wing: its drawers, closets, hallways and tunnels",
		Description: "--confirm must repeat the wing name. Nothing about a delete is recoverable and its\n" +
			"size is unbounded, so a delete that is one typo away from emptying the wrong wing\n" +
			"has to be spelled out twice. Run `agentsmemory wing export` first if the memories\n" +
			"might be wanted back — a bundle is the only way to restore them.",
		Flags: append(dataFlags(def),
			projectFlag(),
			&cli.StringFlag{Name: "wing", Required: true, Usage: "wing to delete"},
			&cli.StringFlag{Name: "confirm", Required: true, Usage: "repeat the wing name to confirm the delete"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return deleteWing(ctx, configFromCmd(c, def), c.String("project"), c.String("wing"), c.String("confirm"))
		},
	}
}

// deleteWing resolves the workspace and purges the wing, reporting what went.
func deleteWing(ctx context.Context, cfg config.Config, slug, wing, confirm string) error {
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}

	res, err := svc.drawers.DeleteWing(ctx, team.ID, wing, confirm)
	if err != nil {
		return err
	}

	fmt.Printf("deleted wing %q from %q: %d drawers, %d closets, %d hallways, %d tunnels\n",
		res.Wing, team.Slug, res.Drawers, res.Closets, res.Hallways, res.Tunnels)
	if res.Drawers == 0 && res.Closets == 0 {
		// A wing is born on first write and dies with its last drawer, so naming one
		// that never existed deletes nothing and looks identical to success.
		fmt.Printf("note: wing %q held nothing — check the name against `agentsmemory inspect`.\n", res.Wing)
	}
	// Say the quiet part out loud, as export does: these tunnels reached wings that
	// still exist, and their disappearance would otherwise look like a second bug.
	if res.Tunnels > 0 {
		fmt.Printf("note: %d tunnel(s) went with it — a tunnel connects two wings and cannot outlive either one.\n", res.Tunnels)
	}
	// SQLite is always the source of truth, so the rows and their vectors are gone
	// no matter which backend this command opened. A separate search index (chromem,
	// qdrant) is only purged when this command is pointed at it too — and --local
	// serves chromem while this command defaults to sqlite, so the mismatch is the
	// normal case rather than an exotic one. The leftovers are inert (a hit whose
	// drawer is gone is skipped), but silence here reads as a half-finished delete.
	if res.Drawers > 0 || res.Closets > 0 {
		fmt.Printf("note: purged the source of truth via --vector-backend %q. A server running another\n"+
			"index (--local serves chromem) keeps inert leftovers there until it is restarted or re-synced;\n"+
			"searches skip them, because the drawers they point at are gone.\n", cfg.VectorBackend)
	}
	return nil
}

// exportWing resolves the workspace, streams the wing to dest, and reports what
// it wrote on stderr — never stdout, which may be carrying the bundle itself.
func exportWing(ctx context.Context, cfg config.Config, slug, wing, out string) error {
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}

	w := io.Writer(os.Stdout)
	if out != "" {
		// 0600: a bundle is the workspace's memories in plain text, so it is
		// readable by its owner alone rather than by every account on the host.
		f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("create %s: %w", out, err)
		}
		defer f.Close()
		w = f
	}

	st, err := wingbundle.Export(ctx, palace.NewRepo(svc.gdb, svc.gdb), team.ID, wing, w)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "exported wing %q from %q: %d drawers, %d closets, %d tunnels",
		wing, team.Slug, st.Drawers, st.Closets, st.Tunnels)
	if out != "" {
		fmt.Fprintf(os.Stderr, " → %s", out)
	}
	fmt.Fprintln(os.Stderr)
	// Say the quiet part out loud: a single-wing selection legitimately drops
	// every tunnel that leaves the wing, and a silent zero looks like data loss.
	if st.Tunnels == 0 {
		fmt.Fprintln(os.Stderr, "note: no tunnels carried — an explicit tunnel links two different wings, so it cannot travel in a single-wing bundle.")
	}
	return nil
}

// importWing files a bundle into the wing named by as. The bundle is read whole
// through the same ingest path the HTTP endpoint uses, so a file lands
// identically whether it arrives over the network or off the disk.
func importWing(ctx context.Context, cfg config.Config, slug, file, as string) error {
	// Validate before opening anything: a bad wing name should cost nothing and
	// must never reach the database as a label.
	wing, err := palace.SanitizeName(as, "--as")
	if err != nil {
		return err
	}

	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}

	src := io.ReadCloser(os.Stdin)
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("open %s: %w", file, err)
		}
		src = f
	}
	defer src.Close()

	// recompute=true: this is a single-shot import, so the derived graph is
	// rebuilt once at the end rather than left stale until something else runs.
	res := importer.Ingest(ctx, svc.drawers, team.ID, wing, src, true)
	if res.Error != "" {
		return fmt.Errorf("import into %q: %s", wing, res.Error)
	}

	fmt.Printf("imported into wing %q of %q: %d drawers, %d closets, %d tunnels, %d hallways rebuilt\n",
		wing, team.Slug, res.Drawers, res.Closets, res.Tunnels, res.Hallways)
	if res.Skipped > 0 {
		fmt.Printf("skipped %d record(s) the palace refused\n", res.Skipped)
	}
	// An import writes rows only; the embedding worker lives in the server. Left
	// unsaid, this reads as a bug — everything imported, nothing findable.
	if res.Pending > 0 {
		fmt.Printf("%d row(s) await embedding — start the server (`agentsmemory --local`) and its\n"+
			"background worker will index them; until then they are stored but not searchable.\n", res.Pending)
	}
	return nil
}

// resolveProject turns a workspace slug into its team, naming the recovery path
// on a miss. An unknown slug is fatal rather than "create it": both subcommands
// move real memories, and inventing a workspace to hold them would bury the typo.
func resolveProject(ctx context.Context, svc *services, slug string) (tenant.Team, error) {
	team, err := svc.tenants.TeamBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tenant.Team{}, fmt.Errorf("no workspace with slug %q (run `agentsmemory projects` to list them)", slug)
		}
		return tenant.Team{}, fmt.Errorf("workspace %q: %w", slug, err)
	}
	return team, nil
}
