package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/urfave/cli/v3"
)

// TestEveryModelACommandDefaultsToIsProvisionedOrDocumented pins the gap between
// a capability that is finished and one an operator can actually run.
//
// ⚠ THE SHIPPED STACK CANNOT RUN kg-extract, AND NOTHING TOLD THE OPERATOR. The
// ollama overlay pulls the embedder and nothing else, and an embedder cannot
// answer /api/generate at all. So an operator who mined 919 drawers found an
// empty knowledge graph, ran the one command that fills it, and got
// `model not found`. Reported 2026-08-31.
//
// ⚠ AND THE MISSING HALF WAS THE COMMAND, NOT THE MODEL. The first version of
// this test checked only that the model name appeared somewhere — which
// .env.example has said for eval since 2026-08-19, so it passed before the
// documentation that motivated it existed. `kg-extract` itself appeared in no
// operator-facing document: not the README, not either env example. A model an
// operator cannot connect to a command is not a documented capability.
//
// This is §Reachability at the deployment layer: the code is correct and the one
// line that makes it findable was never written. The universe comes from the live
// command tree, so a new generative command joins the check on the commit that
// adds it.
func TestEveryModelACommandDefaultsToIsProvisionedOrDocumented(t *testing.T) {
	root := repoRoot(t)
	var docs strings.Builder
	for _, rel := range []string{"README.md", ".env.example", ".env.docker.example"} {
		if src, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
			docs.Write(src)
		}
	}
	composeFiles, _ := filepath.Glob(filepath.Join(root, "docker-compose*.yml"))
	for _, path := range composeFiles {
		if src, err := os.ReadFile(path); err == nil {
			docs.Write(src)
		}
	}
	operatorFacing := docs.String()

	generative := map[string]string{} // command name -> its default model
	for _, cmd := range rootCommand(config.Default()).Commands {
		for _, f := range cmd.Flags {
			sf, ok := f.(*cli.StringFlag)
			if !ok || sf.Name != "gen-model" {
				continue
			}
			generative[cmd.Name] = sf.Value
		}
	}
	if len(generative) == 0 {
		t.Fatal("no command declares a gen-model flag, so this gate reads a shape that no " +
			"longer exists and would pass over anything")
	}

	for name, model := range generative {
		if !strings.Contains(operatorFacing, name) {
			t.Errorf("`%s` needs a generative model and is named in NO operator-facing document "+
				"— an operator has no way to discover the command exists, let alone that it "+
				"needs a model the compose overlay does not pull", name)
		}
		if !strings.Contains(operatorFacing, model) {
			t.Errorf("`%s` defaults to the generative model %q, which no compose file pulls and "+
				"no operator-facing document names. The command is finished, correct, and "+
				"unrunnable on the stack this project ships, with `model not found` as the only "+
				"explanation the operator gets", name, model)
		}
	}
	t.Logf("generative commands covered: %v", generative)
}
