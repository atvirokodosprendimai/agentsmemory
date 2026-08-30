package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
)

// stubBuildInfo makes readBuildInfo answer with the given vcs settings for the
// duration of one test. A `go test` binary carries no vcs stamp of its own, so
// this is the only way the dev-build branch is exercised at all.
func stubBuildInfo(t *testing.T, rev string, dirty bool) {
	t.Helper()
	prev := readBuildInfo
	t.Cleanup(func() { readBuildInfo = prev })
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: rev},
			{Key: "vcs.modified", Value: map[bool]string{true: "true", false: "false"}[dirty]},
		}}, true
	}
}

// TestEffectiveReportsAStampedTagVerbatim pins the decision, not just the code:
// the leading "v" survives, because the string is meant to be looked up on the
// releases page and compared tag-against-tag by internal/updatecheck. Stripping
// it would also change the existing `--version` output, which shares this
// resolver — see the Effective doc comment.
func TestEffectiveReportsAStampedTagVerbatim(t *testing.T) {
	stubBuildInfo(t, "0123456789abcdef0123456789abcdef01234567", false)

	if got := Effective("v0.0.102"); got != "v0.0.102" {
		t.Errorf("Effective(%q) = %q; a stamped tag must be reported unchanged, or a client "+
			"comparing it against the releases page is comparing two different forms", "v0.0.102", got)
	}
}

// TestEffectiveNamesTheCommitOnAnUnstampedBuild covers issue #70's second
// acceptance criterion: the effective version must not be the bare word "dev"
// when build info carries a vcs.revision. "dev" identifies no binary, which is
// the whole complaint the version field exists to answer.
func TestEffectiveNamesTheCommitOnAnUnstampedBuild(t *testing.T) {
	const rev = "df4857d0123456789abcdef0123456789abcdef01"
	stubBuildInfo(t, rev, false)

	for _, stamped := range []string{"dev", "", "  "} {
		got := Effective(stamped)
		if got == Dev {
			t.Errorf("Effective(%q) = %q; build info carried a revision and it was thrown away, "+
				"so every local build reports the same uninformative word", stamped, got)
		}
		if !strings.HasPrefix(got, Dev+"-") || !strings.Contains(got, rev[:shortRevLen]) {
			t.Errorf("Effective(%q) = %q; want %s-<commit> naming %s", stamped, got, Dev, rev[:shortRevLen])
		}
		if len(got) > len(Dev)+1+shortRevLen {
			t.Errorf("Effective(%q) = %q; the revision is not shortened to %d characters",
				stamped, got, shortRevLen)
		}
	}
}

// TestEffectiveMarksADirtyTree: a binary built from a modified tree is not the
// commit it names, and reporting the bare hash would claim it is.
func TestEffectiveMarksADirtyTree(t *testing.T) {
	stubBuildInfo(t, "df4857d0123456789abcdef0123456789abcdef01", true)

	got := Effective("dev")
	if !strings.HasSuffix(got, "+dirty") {
		t.Errorf("Effective(\"dev\") = %q on a modified tree; the binary reports a commit it "+
			"does not actually match", got)
	}
}

// TestEffectiveFallsBackToDevWithoutBuildInfo: there is nothing more honest to
// say, and inventing a version would be worse than the word.
func TestEffectiveFallsBackToDevWithoutBuildInfo(t *testing.T) {
	prev := readBuildInfo
	t.Cleanup(func() { readBuildInfo = prev })
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }

	if got := Effective("dev"); got != Dev {
		t.Errorf("Effective(\"dev\") = %q with no build info; want %q", got, Dev)
	}
}
