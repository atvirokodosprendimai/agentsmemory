package repohygiene

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// TestEveryTrackedTextFileIsCheckedOutWithLFEndings sweeps every tracked file and
// asks GIT what a checkout would do with it, rather than reading the bytes.
//
// ⚠ TWO DESIGN CHOICES, AND EACH ONE EXISTS BECAUSE THE OBVIOUS VERSION IS BLIND.
//
// It asks git rather than reading the files, because on a POSIX host every source
// file is LF whatever the attributes say. A check over the bytes would pass on
// Linux and macOS while the property it claims was absent — and the hosts where it
// matters are exactly the ones that never run the suite. `git check-attr` reports
// what a CHECKOUT would do and answers identically on every platform.
//
// It SWEEPS rather than probing named files, and that half was found in review.
// The first version named one file per attribute rule — a .go, a .sh, a .yml — and
// argued that one file per rule proves the rule applies. It does, and that is the
// wrong question: a probe list drawn from the COVERED set can never report an
// UNCOVERED one. It was already incomplete when written. `.env.example` and
// `.env.docker.example` matched none of the eight patterns and are read by
// TestEveryModelACommandDefaultsToIsProvisionedOrDocumented and by
// documentedEnvVars; so were `.templ`, `.ts`, `go.mod`, and `Dockerfile`, which is
// extensionless and could never have matched any `*.ext` pattern at all. That is
// this repository's own "a list kept beside the truth" defect, one level up from
// the hard-coded matchers the change was fixing.
//
// The class this closes (#163): with core.autocrlf=true, Git for Windows' default,
// and no .gitattributes, a Windows working tree carries CRLF files. Gates that read
// source hard-code "\n" and false-alarm there on properties that hold. A CRLF
// shebang is worse than a false alarm — the interpreter path gains a \r and the
// script will not run — and clients/claude-code/extensions/agentsmemory.ts is a
// shipped go:embed asset, so CRLF there reaches an operator.
func TestEveryTrackedTextFileIsCheckedOutWithLFEndings(t *testing.T) {
	root := repoRoot(t)

	tracked, err := testexec.Command(t, "git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var paths []string
	for _, p := range strings.Split(string(tracked), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) < 100 {
		t.Fatalf("git listed %d tracked files — too few for this to be the repository, so the "+
			"sweep below would be vacuous rather than clean", len(paths))
	}

	// One batched call rather than one per file: check-attr --stdin answers a few
	// hundred lookups in a single process, which is what makes sweeping cheap
	// enough to be the default.
	cmd := testexec.Command(t, "git", "-C", root, "check-attr", "text", "eol", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git check-attr: %v", err)
	}

	// -z output is a flat NUL-separated stream of (path, attribute, value).
	fields := strings.Split(string(out), "\x00")
	attrs := map[string]map[string]string{}
	for i := 0; i+2 < len(fields); i += 3 {
		path, attr, value := fields[i], fields[i+1], fields[i+2]
		if attrs[path] == nil {
			attrs[path] = map[string]string{}
		}
		attrs[path][attr] = value
	}
	if len(attrs) == 0 {
		t.Fatal("check-attr returned nothing parseable, so this gate examined no file at all")
	}

	bad := 0
	for _, p := range paths {
		a := attrs[p]
		// A file declared binary (-text) has no line endings to normalise, and
		// forcing eol on one is how text=auto's guess corrupts things.
		if a["text"] == "unset" {
			continue
		}
		if a["eol"] != "lf" {
			bad++
			if bad <= 20 { // enough to act on; the count below carries the rest
				t.Errorf("%s: eol=%q text=%q, want eol=lf.\n"+
					"  A Windows checkout with core.autocrlf=true materialises this file as CRLF. "+
					"If a gate reads it, the gate false-alarms there on a property that holds; if an "+
					"interpreter runs it, the shebang gains a \\r and it does not run at all; if it "+
					"ships (go:embed), the CRLF reaches an operator.", p, a["eol"], a["text"])
			}
		}
	}
	if bad > 20 {
		t.Errorf("%d tracked files carry no eol rule; the first 20 are listed above", bad)
	}
}
