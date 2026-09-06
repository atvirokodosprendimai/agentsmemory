package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/urfave/cli/v3"
)

// doctorCommand reports whether the palace's stores still agree with each other.
//
// It is read-only and its verdict is its EXIT CODE, so it belongs in a pre-deploy
// script or a cron rather than in somebody's memory. The defect it exists for
// produced no error, no warning and no log line: a wing merge relabelled drawer
// rows and left every stored payload behind, and because a scoped search filters
// at the index on that payload, 13 memories on a live palace became unreachable
// from the wing they were filed in — while an unscoped search still returned
// them, so nothing looked broken.
func doctorCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "Diagnose silent palace failures without repairing the evidence first",
		Description: "Doctor exists for silent failures that normal requests and health checks can miss.\n" +
			"It opens an existing palace exactly as it is: it does not migrate the database,\n" +
			"reconcile the search index, repair data, or run every mode by default. Select one\n" +
			"or more explicit questions and use the same storage flags as the server.\n\n" +
			"  integrity checks (exit non-zero on a finding):\n" +
			"    --index    do vector payload wings agree with their SQLite drawer rows?\n" +
			"    --schema   are all tables declared by migrations actually present?\n" +
			"    --roles    which active API keys authenticate but are refused every write?\n" +
			"    --corpus   do content keys still match their rows, and does every parent_id,\n" +
			"               anchor and fact provenance still resolve? (an ENDED drawer is not a\n" +
			"               finding — it is the system working; a reference to NOTHING is)\n" +
			"    --rerank   can the configured cross-encoder score RERANK_POOL documents inside\n" +
			"               RERANK_TIMEOUT, or does every search silently fall back to hybrid\n" +
			"               order? (no reranker configured is not a finding)\n\n" +
			"  diagnostic reports (measure a question; they do not declare palace health):\n" +
			"    --graph    what graph would current entity extraction derive from this corpus?\n" +
			"    --windows  which snippet windows compete for QUERY against --drawer?\n\n" +
			"A bare `doctor` refuses to run so that zero checks can never look like a healthy palace.",
		Flags: append(dataFlags(def),
			// --local mirrors serve's, because without it doctor checks a backend
			// nobody runs: `--local` is what switches the search index to chromem,
			// and a self-hosted operator who started the server with it and then
			// ran `doctor --index` was having a bare SQLite store inspected while
			// chromem served every query. The check exited 0 on a broken palace.
			&cli.BoolFlag{Name: "local", Sources: cli.EnvVars(mcpprotocol.LocalEnvVar), Usage: "self-hosted single-workspace mode — must match how the server was started, or a different backend is checked"},
			&cli.StringFlag{Name: "project", Value: "local", Usage: "workspace slug to check"},
			&cli.BoolFlag{Name: "index", Usage: "check that every stored point's wing matches its drawer's"},
			&cli.BoolFlag{Name: "graph", Usage: "report what the derived graph WOULD hold if every drawer were run through the entity extractor now (read-only)"},
			&cli.BoolFlag{Name: "roles", Usage: "count active API keys refused every write: deliberate member roles, missing membership rows, and empty roles"},
			&cli.BoolFlag{Name: "schema", Usage: "check that every table the migrations declare actually exists — catches a goose version recorded without its effect"},
			&cli.BoolFlag{Name: "corpus", Usage: "check that content keys match their rows and that parent_id, anchors and fact provenance still resolve — distinguishes an ENDED drawer (fine) from a reference to nothing (not)"},
			&cli.BoolFlag{Name: "rerank", Usage: "probe the live cross-encoder and report the largest pool it can score inside the timeout — a pool that does not fit degrades every search to hybrid order, visible only as reranked=false"},
			&cli.StringFlag{Name: "windows", Usage: "report every candidate snippet window for this QUERY against --drawer, and which one search returns (read-only)"},
			&cli.StringFlag{Name: "drawer", Usage: "the memory id --windows reports on"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			if !c.Bool("index") && !c.Bool("graph") && !c.Bool("roles") && !c.Bool("schema") &&
				!c.Bool("corpus") && !c.Bool("rerank") && c.String("windows") == "" {
				return fmt.Errorf("nothing to check: pass --index, --corpus, --rerank, --graph, --roles, --schema or --windows")
			}
			cfg := configFromCmd(c, def)
			// The command's own writer, not os.Stdout. Every doctor check already
			// takes an io.Writer and every one was handed the process's stdout, which
			// made the REPORTS — the thing an operator actually reads — unobservable
			// from a test. The dispatch order bug this task fixes was invisible for
			// exactly that reason: nothing could assert which reports appeared.
			out := doctorOutput(c)
			if q := c.String("windows"); q != "" {
				if err := doctorWindows(ctx, cfg, c.String("project"), q, c.String("drawer"), out); err != nil {
					return err
				}
			}
			if c.Bool("graph") {
				if err := doctorGraph(ctx, cfg, c.String("project"), out); err != nil {
					return err
				}
			}
			if c.Bool("roles") {
				if err := doctorRoles(ctx, cfg, out); err != nil {
					return err
				}
			}
			if c.Bool("schema") {
				if err := doctorSchema(ctx, cfg, out); err != nil {
					return err
				}
			}
			// EVERY selected check runs, and the failures are joined at the end.
			//
			// --index used to `return` here, which meant a check added after it never
			// ran when both flags were passed — the classic shape of a capability that
			// is finished and unreachable, and one TestEveryFlagIsRead cannot see
			// because the flag IS read, in a block nothing reaches. Returning early
			// also made a passing --index hide a failing --corpus, so the exit code
			// answered a narrower question than the operator asked.
			var failures []error
			if c.Bool("corpus") {
				if err := doctorCorpus(ctx, cfg, c.String("project"), out); err != nil {
					failures = append(failures, err)
				}
			}
			if c.Bool("rerank") {
				if err := doctorRerank(ctx, cfg, out); err != nil {
					failures = append(failures, err)
				}
			}
			if c.Bool("index") {
				if err := doctorIndex(ctx, cfg, c.String("project"), out); err != nil {
					failures = append(failures, err)
				}
			}
			return errors.Join(failures...)
		},
	}
}

// doctorOutput is where a doctor report goes: the command's writer when one is
// set, and the process's stdout otherwise. cli leaves Writer nil unless a caller
// sets it, so the fallback is what the binary uses in production.
func doctorOutput(c *cli.Command) io.Writer {
	if w := c.Root().Writer; w != nil {
		return w
	}
	return os.Stdout
}

// doctorIndex runs the wing-payload drift check and reports it.
//
// It prints drawer ids and wing names and never memory text: a doctor report is
// pasted into an issue, and the palace it describes is private.
func doctorIndex(ctx context.Context, cfg config.Config, slug string, out io.Writer) error {
	if err := requireExistingDB(cfg.DBPath); err != nil {
		return err
	}
	// reconcile=false: a checker that rebuilt the index first would report on a
	// palace it had just repaired, and could not fail on the fault it exists for.
	svc, err := inspectServices(cfg)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}

	report, err := svc.drawers.IndexDrift(ctx, team.ID)
	if err != nil {
		return err
	}
	return reportDrift(out, report)
}

// reportDrift renders a drift report and returns the verdict as an error, so the
// exit code carries it. Separate from the lookup because the rendering is what
// an operator reads and the lookup needs a database — a report nobody can test
// is a report that quietly stops saying anything.
func reportDrift(out io.Writer, report palace.DriftReport) error {
	pending := ""
	if total := report.Pending.Drawers + report.Pending.Closets; total > 0 {
		pending = fmt.Sprintf(" (%d more await a first embedding, which is a queue and not a fault)", total)
	}
	if report.Clean() {
		fmt.Fprintf(out, "index: %d drawer(s) and %d closet(s) checked, every stored point agrees with its row%s\n",
			report.Checked.Drawers, report.Checked.Closets, pending)
		return nil
	}

	fmt.Fprintf(out, "index: %d drawer(s) and %d closet(s) checked, %d stored point(s) disagree with their row%s\n\n",
		report.Checked.Drawers, report.Checked.Closets, report.Total, pending)
	for _, d := range report.Drifted {
		if d.Missing {
			fmt.Fprintf(out, "  %-16s %s  ABSENT — no point at all, filed in %q\n", d.Store, d.DrawerID, d.Actual)
			continue
		}
		fmt.Fprintf(out, "  %-16s %s  indexed as %q, filed in %q\n", d.Store, d.DrawerID, d.Indexed, d.Actual)
	}
	if report.Truncated() {
		fmt.Fprintf(out, "  … and %d more, not listed. The COUNT above is exact.\n", report.Total-len(report.Drifted))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "A scoped recall filters at the index, on the payload above — so a mislabelled memory is")
	fmt.Fprintln(out, "UNREACHABLE from the wing it is filed in and answers only an unscoped search. An ABSENT one")
	fmt.Fprintln(out, "answers nothing at all: run `agentsmemory sync` to replay it from the source of truth.")
	return fmt.Errorf("%d stored point(s) disagree with their drawer", report.Total)
}

// doctorGraph reports what the derived graph would hold if the entity extractor
// ran over every drawer now.
//
// It changes nothing. It exists because the derived graph is empty on every
// palace populated through the agent write path, the obvious fix is to extract
// on write, and whether that fix WORKS is a property of the corpus rather than
// of the code — mining feeds the extractor long repetitive transcripts and
// agents file short deliberate notes.
func doctorGraph(ctx context.Context, cfg config.Config, slug string, out io.Writer) error {
	if err := requireExistingDB(cfg.DBPath); err != nil {
		return err
	}
	svc, err := inspectDatabaseServices(cfg)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}
	report, err := svc.drawers.GraphPotential(ctx, team.ID)
	if err != nil {
		return err
	}
	return reportGraph(out, report)
}

// reportGraph renders the projection.
//
// The BAR is printed beside the number, always, because the number alone is what
// gets quoted: "39% would carry two entities" reads as a result, and it is only a
// result against a threshold somebody committed to beforehand.
func reportGraph(out io.Writer, report palace.GraphReport) error {
	fmt.Fprintf(out, "graph: %d drawer(s) examined — nothing was written\n\n", report.Drawers)
	fmt.Fprintf(out, "  %-26s %8s %10s %10s %10s\n", "wing", "drawers", ">=1 entity", ">=2", "hallways")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 68))
	for _, w := range report.Wings {
		fmt.Fprintf(out, "  %-26s %8d %10d %10d %10d\n", w.Wing, w.Drawers, w.WithAny, w.WithTwo, w.Hallways)
	}
	fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 68))
	fmt.Fprintf(out, "  %-26s %8d %10d %10d %10d\n\n", "TOTAL", report.Drawers, report.WithAny, report.WithTwo, report.Hallways)

	fmt.Fprintf(out, "  %.1f%% of drawers would carry two or more entities, against a pre-registered bar of %.0f%%: %s\n",
		100*report.ViableShare(), 100*palace.GraphViabilityBar, verdictWord(report.Viable()))
	fmt.Fprintln(out, "  (a hallway needs a PAIR co-occurring in one drawer, so a drawer with one entity adds nothing)")

	for _, w := range report.Wings {
		if len(w.TopEntities) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n  most frequent candidates in %s: %s\n", w.Wing, strings.Join(w.TopEntities, ", "))
	}
	return nil
}

// verdictWord states the decision the bar implies, so the reader does not have
// to compare two numbers to find out what was decided.
func verdictWord(viable bool) string {
	if viable {
		return "CLEARS the bar — extracting on the write path is worth its cost"
	}
	return "BELOW the bar — extracting on the write path would leave the graph empty for a subtler reason"
}

// requireExistingDB refuses to inspect a database that is not there.
//
// openDB CREATES a missing file and the migrations then fill it, so a mistyped
// --db made doctor build an empty palace and report it clean. "The path was
// wrong" and "the palace is healthy" must not be the same output.
func requireExistingDB(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no database at %q — doctor inspects an existing palace and will not "+
			"create one; check --db (or AGENTSMEMORY_DB)", path)
	}
	return nil
}

// doctorWindows reports every candidate snippet window for a query against one
// memory, and which one search returns.
//
// It answers the question ADR-019 turns on, with data rather than intuition: when
// an agent gets the right memory and not the answer, is the answer in a window
// the chooser scored and threw away, or in no window at all? The first is fixable
// by showing more of the memory; the second is synthesis, and showing more buys
// nothing.
func doctorWindows(ctx context.Context, cfg config.Config, slug, query, drawerID string, out io.Writer) error {
	if err := requireExistingDB(cfg.DBPath); err != nil {
		return err
	}
	if drawerID == "" {
		return fmt.Errorf("--windows needs --drawer: a window report is about ONE memory, and picking " +
			"one for you would report on a memory you did not choose")
	}
	svc, err := inspectDatabaseServices(cfg)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}
	d, err := svc.drawers.Get(ctx, team.ID, drawerID)
	if err != nil {
		return err
	}
	rep := palace.WindowReport(d.Content, query, palace.DefaultSnippetChars)

	fmt.Fprintf(out, "memory %s — %d runes, %d-rune window, %d candidate(s)\n\n",
		drawerID[:12], rep.Memory, rep.Window, len(rep.Windows))
	for _, w := range rep.Windows {
		mark := "  "
		if w.Chosen {
			mark = "->" // the one search actually returns
		}
		fmt.Fprintf(out, "%s [%5d,%5d) %d term(s)  %s\n", mark, w.Start, w.End, w.Terms, firstLineOf(w.Text, 88))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "The marked window is what an agent receives. Read the others: if the answer to the")
	fmt.Fprintln(out, "query is in one of them, showing more of the memory fixes it. If it is in none of")
	fmt.Fprintln(out, "them, the answer is not in this memory and more windows would buy nothing.")
	return nil
}

// doctorRoles reports API keys that authenticate but may not write.
//
// The write guard refuses the least-privileged role. Three populations land
// there: a deliberately assigned "member" — which is the dashboard's DEFAULT for
// an invited teammate and is labelled read-only in its own UI — plus the two
// data faults, an absent membership row and an empty role.
//
// All three wrote normally before the guard was armed and are refused after it,
// per call, at write time. Counting only the faults would report a clean palace
// while every read-only teammate's agent stopped filing memories, so this counts
// all three and says which is which.
//
// It takes no --project because the fault belongs to the deployment rather than
// to one workspace.
func doctorRoles(ctx context.Context, cfg config.Config, out io.Writer) error {
	if err := requireExistingDB(cfg.DBPath); err != nil {
		return err
	}
	svc, err := inspectDatabaseServices(cfg)
	if err != nil {
		return err
	}
	refused, err := svc.tenants.RefusedWrites(ctx)
	if err != nil {
		return err
	}
	return reportRefusedWrites(out, refused)
}

// reportRefusedWrites renders the report and returns the verdict as an error so
// the exit code carries it. Split from the lookup for the same reason reportDrift
// is: the rendering is what an operator reads, and a report that needs a database
// to exercise is a report that quietly stops saying anything.
//
// It prints workspace slugs and counts, never key material: a doctor report gets
// pasted into an issue.
func reportRefusedWrites(out io.Writer, refused []tenant.ReadOnlyKeys) error {
	if len(refused) == 0 {
		fmt.Fprintln(out, "roles: every active API key may write")
		return nil
	}

	total, faults := 0, 0
	for _, k := range refused {
		total += k.Total()
		faults += k.Faults()
	}
	fmt.Fprintf(out, "roles: %d active key(s) across %d workspace(s) are refused EVERY write tool\n\n", total, len(refused))
	fmt.Fprintf(out, "  %-26s %8s %8s %8s\n", "workspace", "member", "no row", "empty")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 54))
	for _, k := range refused {
		slug := k.Slug
		if slug == "" {
			slug = k.TeamID // a key whose workspace row is gone still counts
		}
		fmt.Fprintf(out, "  %-26s %8d %8d %8d\n", slug, k.Member, k.Missing, k.Empty)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Each of these authenticates and reads normally, and is REFUSED on am_add_drawer,")
	fmt.Fprintln(out, "am_diary_write, am_kg_add and every other write tool — per call, at write time.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  member  a role someone CHOSE, and the default for an invited teammate. If their")
	fmt.Fprintln(out, "          agent is meant to file memories, promote them to writer before upgrading.")
	if faults > 0 {
		fmt.Fprintln(out, "  no row  / empty  no current code path creates either: this is historical data.")
		fmt.Fprintln(out, "          Give each one the role it should have had.")
	}
	return fmt.Errorf("%d active key(s) are refused every write tool", total)
}
