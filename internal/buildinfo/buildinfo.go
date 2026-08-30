// Package buildinfo resolves the one version string every operator-visible
// surface reports.
//
// It exists because the version was reachable from exactly one place — the CLI's
// --version — while three other surfaces an operator actually looks at reported
// something else or nothing at all: am_status carried no version (issue #70), the
// MCP initialize handshake carried the frozen literal "0.1.0" (issue #106), and
// serve startup could not say whether a newer release existed (issue #115). Each
// of those could have resolved the version for itself; four independent
// resolutions are four chances to disagree about which binary is running, which
// is the exact question all of them exist to answer.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Dev is the value cmd/server's main.version carries when the linker stamped no
// tag. It is a real value rather than the empty string because the Dockerfile
// defaults its VERSION build-arg to it, so an unstamped build arrives here as
// this word rather than as nothing.
const Dev = "dev"

// shortRevLen is how much of a commit hash a dev version carries: enough to name
// a commit unambiguously in this repository, short enough to read in a log line.
const shortRevLen = 12

// Effective returns the version to report, given the value the linker stamped
// into main.version.
//
// A stamped build is reported VERBATIM, leading "v" and all. That is deliberate
// and it is a decision, not an oversight: release.yml stamps ${GITHUB_REF_NAME},
// so what comes back is character-for-character the tag an operator can look up
// on the releases page, and the update check in internal/updatecheck compares a
// tag against a tag instead of against a re-derived form. The published container
// image tag drops the "v" (release.yml uses type=semver,pattern={{version}}), so
// the two forms differ — reconciling them would change the existing --version
// output for anyone already parsing it, and this resolver is shared with that
// flag. M chose verbatim on 2026-08-30.
//
// An UNSTAMPED build is not left as the bare word "dev", which identifies
// nothing. debug.ReadBuildInfo carries vcs.revision for any build made inside a
// repository, so a local binary still names its commit — and says when the tree
// it was built from was dirty, because a dirty build is not the commit it claims.
func Effective(stamped string) string {
	if s := strings.TrimSpace(stamped); s != "" && s != Dev {
		return s
	}
	rev, dirty := vcsRevision()
	if rev == "" {
		return Dev
	}
	if len(rev) > shortRevLen {
		rev = rev[:shortRevLen]
	}
	v := Dev + "-" + rev
	if dirty {
		v += "+dirty"
	}
	return v
}

// readBuildInfo is debug.ReadBuildInfo behind a variable so a test can supply a
// build stamp. It is not indirection for its own sake: a `go test` binary carries
// NO vcs settings, so without this seam the dev-build fallback is unreachable
// from any test and could rot into a branch nothing ever executes — the class of
// defect AGENTS.md §Reachability is about.
var readBuildInfo = debug.ReadBuildInfo

// vcsRevision reports the commit this binary was built from and whether the tree
// was dirty, both empty/false when the build carries no VCS stamp — which is the
// case for `go test` binaries and for a build made outside a repository.
func vcsRevision() (rev string, dirty bool) {
	info, ok := readBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return rev, dirty
}
