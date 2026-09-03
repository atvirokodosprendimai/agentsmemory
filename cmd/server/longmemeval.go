// longmemeval.go implements `agentsmemory longmemeval`: does following our own
// memory-writing advice make an agent ANSWER better?
//
// Every other eval here scores ranking against a corpus derived from our own
// drawers. This one scores judged answer accuracy over a (write-policy ×
// query-policy) grid on LongMemEval-S — a corpus written by people who have
// never seen this codebase, which is the property ADR-032 ruled a self-derived
// one can never acquire.
//
// Two properties are what make it an instrument rather than a way to confirm the
// skills, and both live in internal/longmemeval rather than here: every cell
// shares one context budget, and the baseline is the verbatim session rather
// than the policy we hope wins. This file is the composition root only — flags,
// registry lookup, wiring, output. No policy or scoring logic belongs here.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/gen"
	"github.com/atvirokodosprendimai/agentsmemory/internal/longmemeval"
	"github.com/urfave/cli/v3"
)

// longmemevalCommand builds the `longmemeval` subcommand.
//
// The --write and --query usage strings are DERIVED from the registries, so
// --help cannot list a policy that does not exist or omit one that does. That is
// rung 3 of this repository's reachability discipline, and
// TestLongmemevalHelpListsEveryRegisteredPolicy fails if this stops calling the
// registry and starts formatting its own list.
func longmemevalCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "longmemeval",
		Usage: "Score judged answer accuracy over a (write-policy x query-policy) grid on LongMemEval-S",
		// serveFlags carries the storage and embedder settings configFromCmd reads.
		// Declaring only this command's own flags left configFromCmd with nothing to
		// read, so the command could not be pointed at a database or an embedder at
		// all — and no gate saw it, because every flag it DID declare was read.
		Flags: append(serveFlags(def),
			&cli.StringFlag{Name: "data", Usage: "path to the LongMemEval-S JSON file", Required: true},
			&cli.StringFlag{Name: "wing", Usage: "base name for the scratch scopes this run writes", Value: "longmemeval_scratch"},
			&cli.StringSliceFlag{Name: "write", Usage: longmemeval.WritePolicyUsage(), Value: []string{"verbatim"}},
			&cli.StringSliceFlag{Name: "query", Usage: longmemeval.QueryPolicyUsage(), Value: []string{"verbatim"}},
			&cli.IntFlag{Name: "n", Usage: "how many questions to run (stratified by question type)", Value: 20},
			&cli.IntFlag{Name: "seed", Usage: "subset seed; recorded in the results file so a later run is comparable", Value: 42},
			// ⚠Runes, not tokens: this repository has no tokenizer, and naming the
			// invariant after a unit nothing here can compute would make the
			// instrument's central property unauditable. ADR-047 property 1 records
			// what the approximation costs and how the run bounds it.
			// ⚠24000, not a round guess: measured 2026-09-01 over 2,051 real
			// LongMemEval-S sessions, the MEDIAN session is 9,808 characters and p90
			// is 16,884. The first default here was 6000 — below the median — so the
			// verbatim baseline could not fit a single record and scored 0 by
			// construction, which would have made every other policy look good
			// against a baseline that cannot play. A budget must hold at least one
			// baseline record or the comparison is degenerate.
			&cli.IntFlag{Name: "context-runes", Value: 24000,
				Usage: "shared context budget per cell, in runes. Must hold at least one whole session (median 9.8k, p90 16.9k) or the verbatim baseline assembles nothing"},
			&cli.IntFlag{Name: "search-limit", Usage: "memories retrieved per search", Value: 20},
			&cli.StringFlag{Name: "out", Usage: "write the results file here", Value: "longmemeval.cells.json"},
			&cli.StringFlag{Name: "gen-url", Usage: "generative endpoint for the reader and judge", Sources: cli.EnvVars("EVAL_GEN_URL")},
			// Defaults to the model the other two generative commands default to, and
			// the one the README tells an operator to pull. A different default would
			// leave this command unrunnable on the stack this project ships, with
			// `model not found` as the only explanation the operator gets — which is
			// the failure TestEveryModelACommandDefaultsToIsProvisionedOrDocumented
			// exists to prevent, and which it caught on this command.
			&cli.StringFlag{Name: "gen-model", Value: "qwen2.5-coder:7b", Sources: cli.EnvVars("EVAL_GEN_MODEL"),
				Usage: "model that reads the assembled memories AND judges the answers. Held fixed across the whole grid, so a cell delta is the policy rather than the model; a stronger general-purpose model judges prose better than a coder model, and the run records which one was used"},
			&cli.StringFlag{Name: "gen-api-key", Usage: "bearer token for the generative endpoint", Sources: cli.EnvVars("EVAL_GEN_API_KEY")},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return runLongmemeval(ctx, def, c)
		},
	}
}

// runLongmemeval wires the dataset, the store and one model into RunGrid.
func runLongmemeval(ctx context.Context, def config.Config, c *cli.Command) error {
	ds, err := longmemeval.Load(c.String("data"))
	if err != nil {
		return err
	}
	sel := longmemeval.Subset(ds, c.Int("n"), int64(c.Int("seed")))

	svc, err := buildServices(configFromCmd(c, def))
	if err != nil {
		return err
	}
	// One workspace, resolved the way every other local command resolves it. A
	// database holding someone else's workspaces must not be written into by a
	// scratch run, which is what EnsureLocalWorkspace refuses.
	team, err := svc.tenants.EnsureLocalWorkspace(ctx)
	if err != nil {
		return fmt.Errorf("local workspace: %w", err)
	}

	// ONE model for the reader and the judge, held fixed across the whole grid:
	// the cell delta is then the policy. ADR-047 property 3.
	model := &gen.Client{
		URL:    c.String("gen-url"),
		Model:  c.String("gen-model"),
		APIKey: c.String("gen-api-key"),
		HTTP:   &http.Client{Timeout: 5 * time.Minute},
	}
	endpointKind := "ollama"
	if model.OpenAIShaped() {
		endpointKind = "v1-url"
	}

	commit, _ := buildStamp()
	cells, err := longmemeval.RunGrid(ctx, svc.drawers, sel,
		c.StringSlice("write"), c.StringSlice("query"),
		longmemeval.GridOptions{
			Wing:           c.String("wing"),
			TeamID:         team.TeamID,
			ContextRunes:   c.Int("context-runes"),
			SearchLimit:    c.Int("search-limit"),
			Reader:         model,
			Judge:          model,
			DatasetPath:    ds.Path,
			DatasetSHA256:  ds.SHA256,
			ModelID:        c.String("gen-model"),
			EndpointKind:   endpointKind,
			RankingProfile: svc.drawers.RankingProfile(),
			Commit:         commit,
		})
	if err != nil {
		return err
	}

	blob, err := cells.JSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.String("out"), blob, 0o600); err != nil {
		return err
	}
	for _, cell := range cells.Cells {
		// MRR is printed beside the hit rate because the rate SATURATES — measured
		// on the real corpus, every policy scored 1.000 — so a summary that showed
		// only the rate would report "no difference" over a grid where the rank had
		// moved. It was in the results file and not on the console for one run, and
		// that is exactly long enough to draw the wrong conclusion from a terminal.
		fmt.Printf("%-16s %-14s judged %.3f (%d/%d)  hit %.3f  mrr %.3f  budget %d runes  skipped %d\n",
			cell.Write, cell.Query,
			cell.Accuracy(), cell.Correct, cell.Scored,
			cell.RetrievalRate(), cell.RetrievalMRR,
			cell.BudgetRunesUsed, cell.MemoriesSkipped)
	}
	fmt.Printf("wrote %s\n", c.String("out"))
	return nil
}
