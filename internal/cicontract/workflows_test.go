// Package cicontract pins the release and preview-image promises made by the
// repository's GitHub Actions workflows.
package cicontract

import (
	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPRImageWorkflowPublishesDigestDerivedPRTagWithoutLatest(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/pr-image.yml")
	requireText(t, workflow,
		"pull_request:",
		"workflow_dispatch:",
		"github.event.pull_request.head.repo.full_name == github.repository",
		"refs/pull/${pr_number}/head",
		`expected_sha="${INPUT_HEAD_SHA,,}"`,
		`[[ ! "$expected_sha" =~ ^[0-9a-f]{40}$ ]]`,
		`image="ghcr.io/${GITHUB_REPOSITORY,,}-pr"`,
		"push-by-digest=true,name-canonical=true,push=true",
		`tag="pr-${pr_number}-sha256-${digest#sha256:}"`,
		"docker buildx imagetools create --prefer-index=false",
		`test "$tag_digest" = "$digest"`,
		`canonical="${image}@${digest}"`,
		"GITHUB_STEP_SUMMARY",
	)

	for _, forbidden := range []string{
		"docker/metadata-action",
		"type=raw,value=latest",
		"type=semver",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("PR image workflow contains %q, which can couple previews to a moving release tag", forbidden)
		}
	}
}

func TestPRImageCleanupDeletesOnlyExpiredDigestDerivedPRTags(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/pr-image-cleanup.yml")
	requireText(t, workflow,
		"schedule:",
		"workflow_dispatch:",
		"packages: write",
		`cutoff="$(date -u -d '7 days ago'`,
		`package="${GITHUB_REPOSITORY#*/}-pr"`,
		"scripts/select-expired-pr-images.jq",
		`done < "$expired"`,
		`/versions/${version_id}"`,
		`jq -s -e --arg cutoff "$cutoff"`,
		`/orgs/${owner}/packages/container/${package}/versions/${version_id}`,
		"--method DELETE",
	)
}

func TestExpiredPRImageSelectorRejectsProtectedAndFreshVersions(t *testing.T) {
	selector := filepath.Join("..", "..", "scripts", "select-expired-pr-images.jq")
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq is not installed; GitHub's ubuntu runner exercises this selector")
	}

	const fixture = `[
  {"id":1,"created_at":"2026-08-16T00:00:00Z","metadata":{"container":{"tags":["pr-24-sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}}},
  {"id":2,"created_at":"2026-08-18T00:00:00Z","metadata":{"container":{"tags":["pr-24-sha256-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"]}}},
  {"id":3,"created_at":"2026-08-16T00:00:00Z","metadata":{"container":{"tags":["latest"]}}},
  {"id":4,"created_at":"2026-08-16T00:00:00Z","metadata":{"container":{"tags":["pr-24-sha256-cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","0.0.92"]}}},
  {"id":5,"created_at":"2026-08-16T00:00:00Z","metadata":{"container":{"tags":[]}}}
]`
	cmd := testexec.Command(t, jq, "-r", "--arg", "cutoff", "2026-08-17T00:00:00Z", "-f", selector)
	cmd.Stdin = strings.NewReader(fixture)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run cleanup selector: %v\n%s", err, out)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(fields) != 3 || fields[0] != "1" {
		t.Fatalf("selected %q, want only expired PR-only version 1", out)
	}
}

func TestHostedComposeAcceptsCanonicalImageDigest(t *testing.T) {
	compose := readRepoFile(t, "docker-compose.prod.yml")
	requireText(t, compose,
		"AGENTSMEMORY_IMAGE",
		`${AGENTSMEMORY_IMAGE:-ghcr.io/atvirokodosprendimai/agentsmemory:${AGENTSMEMORY_IMAGE_TAG:-latest}}`,
	)
}

func TestBuildWorkflowRunsContractAxisGate(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/build.yml")
	requireText(t, workflow,
		"go test ./...",
		"-tags contractaxis",
		"./internal/mcptest",
	)
}

// TestReleasedImageIsPublishedForArm64 fails when a release stops publishing a
// linux/arm64 variant of the container image.
//
// Until this landed, `platforms:` was absent from the release build entirely, so
// buildx produced a single manifest for whatever the runner happened to be —
// linux/amd64 — and an arm64 host could not pull a released image AT ALL. Not
// "slowly", not "degraded": `docker pull` failed outright with no matching
// manifest, and the only route left was building from source on the host. That
// is invisible from inside CI, because every job is green whether or not the
// artifact can run anywhere but the runner.
//
// The QEMU setup is pinned alongside the platform list because the two are one
// change, not two: the Dockerfile's runtime stage installs packages, so its
// layer executes on the target architecture and an arm64 build without an
// emulator fails with "exec format error". Pinning only the platform list would
// let a later edit drop the emulator and take the arm64 image down again, which
// is the same finished-and-unreachable shape this package exists to catch.
func TestReleasedImageIsPublishedForArm64(t *testing.T) {
	workflow := withoutComments(readRepoFile(t, ".github/workflows/release.yml"))
	requireText(t, workflow,
		"platforms: linux/amd64,linux/arm64",
		"docker/setup-qemu-action",
	)
}

// TestPRImageIsPublishedForArm64 fails when a preview image stops covering
// linux/arm64.
//
// A preview exists to be deployed somewhere before it merges, and the workflow's
// own step summary tells a maintainer to run the published digest on their host.
// When that host is arm64 and the preview is amd64-only, the instructions the
// workflow prints are not true, which is worse than not printing them.
func TestPRImageIsPublishedForArm64(t *testing.T) {
	workflow := withoutComments(readRepoFile(t, ".github/workflows/pr-image.yml"))
	requireText(t, workflow,
		"platforms: linux/amd64,linux/arm64",
		"docker/setup-qemu-action",
	)
}

// TestDockerfileCrossCompilesRatherThanEmulates fails when the build stage stops
// being pinned to the building machine, or stops targeting the requested
// architecture.
//
// These two lines are a pair and are only correct together. Pinning the builder
// to $BUILDPLATFORM without consuming $TARGETARCH puts an amd64 binary inside
// every platform of the manifest — the arm64 entry then exists, passes every
// check above, and dies at exec time on the user's machine. Consuming
// $TARGETARCH without pinning the builder is merely slow: the Go toolchain runs
// under emulation and a one-minute compile becomes many.
//
// So the gate asserts both halves, and reads the file with its comments
// stripped — see withoutComments for the measurement that justifies that, which
// is narrower than the reason first written here.
func TestDockerfileCrossCompilesRatherThanEmulates(t *testing.T) {
	dockerfile := withoutComments(readRepoFile(t, "Dockerfile"))
	requireText(t, dockerfile,
		"FROM --platform=$BUILDPLATFORM golang:",
		"ARG TARGETOS",
		"ARG TARGETARCH",
		"GOOS=${TARGETOS} GOARCH=${TARGETARCH}",
	)
}

// TestCIRunsTheRaceDetector fails when a CI path stops running the race
// detector.
//
// Nothing in this repository ran `-race` until this landed: not a workflow, not
// an ADR acceptance fence, not a Makefile. The suite was measured race-clean at
// the time, so this closes an ENFORCEMENT gap rather than fixing a defect — the
// distinction matters because it says what the gate is for. It is not here to
// keep today's suite passing; it is here so that the first change to introduce
// an unsynchronised access fails in CI instead of in production. The gap was
// found by a human asking "do we always run tests with -race?" during review of
// a change that added the first goroutine anyone had reason to ask about, and
// two review passes over that change had not thought to ask.
//
// Both paths are pinned. build.yml is the always-on net (every push, every PR,
// and version tags too); release.yml carries its own copy because the workflows
// are independent by design and a tag publishes from there regardless of what
// the sibling is doing. pr-image.yml is deliberately NOT included: it builds the
// same commit that build.yml already race-tests on the pull_request trigger, so
// a third run would buy nothing and cost a full emulated image build.
//
// The explicit -timeout is pinned as part of the same contract, because losing
// it does not degrade the gate — it BREAKS the build. go test allows 10 minutes
// per package and the instrumented suite exceeds that on a CI runner: the first
// run of this step died at exactly 600s with "test timed out", with no data race
// anywhere in the log. Anyone deleting the flag would reproduce that, so the
// flag is contract rather than tuning.
func TestCIRunsTheRaceDetector(t *testing.T) {
	for _, workflow := range []string{
		".github/workflows/build.yml",
		".github/workflows/release.yml",
	} {
		content := withoutComments(readRepoFile(t, workflow))
		if !strings.Contains(content, "go test -race") {
			t.Errorf("%s does not run the race detector; an unsynchronised access "+
				"would reach production without any CI path objecting", workflow)
			continue
		}
		if !strings.Contains(content, "-timeout=") {
			t.Errorf("%s runs -race without an explicit -timeout; the default is 10 "+
				"minutes per package and the instrumented suite exceeds it on a runner, "+
				"so the job fails with a timeout that reads like a hung test", workflow)
		}
	}
}

// withoutComments drops whole-line comments so a contract check reads
// configuration rather than the prose describing it.
//
// MEASURED, because the first version of this comment claimed something wider
// and was wrong. The claim tested was "explanatory prose naming the wiring would
// satisfy a raw substring check once the wiring is deleted". That is FALSE here
// today: with every checked fragment deleted, none of the surviving comments in
// these four files contains any of them, so stripping catches nothing on the
// current tree.
//
// What it does catch was measured separately and is the ordinary way a setting
// gets turned off: COMMENTING THE LINE OUT. Prefixing release.yml's platform
// list with '#' genuinely disables arm64 publishing, and a raw substring gate
// stays GREEN on it, because the disabled line still contains its own text. With
// comments stripped the same mutation goes red. That is the whole justification
// — defence against a disabled line reading as an enabled one, not against prose.
//
// It removes only lines whose first non-blank character is '#', never a '#'
// appearing later in a line: these files are full of shell parameter expansions
// such as ${digest#sha256:} that a naive strip would corrupt into a false
// failure. Whole-line comments are where explanatory prose actually lives, which
// is the only thing this needs to remove.
func withoutComments(content string) string {
	var kept []string
	for line := range strings.SplitSeq(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func requireText(t *testing.T, content string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(content, fragment) {
			t.Errorf("missing workflow contract %q", fragment)
		}
	}
}
