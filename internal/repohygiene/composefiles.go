package repohygiene

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// composeGlob is the shape a compose file's name has. It is the pattern the five
// gates used to hand to filepath.Glob, kept here so the SELECTION rule and the
// NAME rule stay in one place: a file is a candidate because of its name, and a
// candidate is the project's because git tracks it.
const composeGlob = "docker-compose*.yml"

// ComposeFiles returns the project's compose files as absolute paths, sorted,
// read from the GIT INDEX rather than from the directory.
//
// It exists because five gates in two packages answered "which compose files does
// this project have?" with filepath.Glob over the working directory, and that is a
// different question. An operator's untracked local overlay — docker-compose.local.yml,
// a pattern this project effectively documents, whose own header says "LOCAL override
// — not part of the project, safe to delete" — landed in the gates' universe and made
// two of them permanently red on that host: the README cannot document a file that is
// not in the repository, and the overlay pins RERANK_URL="" precisely because pinning
// a documented knob locally is what an override is FOR.
//
// The consequence was worse than a red suite. scripts/redeploy.sh runs the suite
// before it builds, so on such a host the documented deploy procedure had no path
// that worked: drop the overlay from the compose chain and the deploy silently
// reverts the host's tuning (issue #209, which the chain detection was added to
// prevent), or copy the overlay into the clone and the gates refuse to build at all
// (#296). Two closed doors, both created by reading the directory.
//
// ⚠ THE ORIGINAL REASONING WAS RIGHT AND IS PRESERVED. hygiene_test.go argued the
// expected set should be the directory so that "adding an overlay and forgetting the
// README fails a build rather than being noticed by someone who never had reason to
// look." That holds for a TRACKED overlay, and the git index keeps it holding: a
// compose file committed without its README line still fails, on the commit that adds
// it. What the index removes is only the untracked case, where the gate was asserting
// documentation for a file that by construction cannot be documented.
//
// ⚠ IT DOES NOT FALL BACK TO THE DIRECTORY WHEN GIT CANNOT ANSWER. A fallback would
// restore the defect exactly where it is hardest to see — a host with an overlay AND
// no usable git — and would do it silently. Callers get the error and fail loudly,
// which is the same choice clients/claude-code/plugin_test.go already made when it
// read file modes from the index: it cannot fall back to the working tree either.
func ComposeFiles(root string) ([]string, error) {
	// A deadline because this is a child process: a git that hangs must fail the
	// gate rather than hang the suite until the package timeout kills the binary
	// and leaves the child reparented (the failure internal/testexec exists for).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// -z and NUL splitting because a path may contain anything a filesystem allows;
	// git quotes such names in its default output and the quoting would have to be
	// undone here, which is a parser nobody needs.
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--", composeGlob)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		// ls-files with a pathspec still returns paths in subdirectories, and a
		// compose file nested somewhere else is not what any of these gates mean
		// by "the project's compose files".
		if strings.Contains(name, "/") {
			continue
		}
		files = append(files, filepath.Join(root, name))
	}
	sort.Strings(files)
	return files, nil
}
