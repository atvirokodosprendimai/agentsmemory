// Package updatecheck tells a self-hosted operator that the build they are
// running has been superseded.
//
// It exists because a fixed bug or a security patch can sit unreleased-to-this-
// host indefinitely and silently: the only signal that a newer build exists is
// somewhere the server never looks, so an operator learns about it by going to
// GitHub and remembering to (issue #115). The repository already carried the
// version; the missing half was the comparison and one line of output.
//
// Everything here FAILS OPEN. A notification is a nicety, and ADR-025's position
// is that an external dependency failing drops the nicety rather than the
// service — so an offline host, a rate-limited API, a slow response and a
// malformed payload all resolve to the same thing as "you are up to date":
// silence. There is no error path an operator can be bothered by, deliberately,
// because a warning about a failed update check is strictly worse than no update
// check at all.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/buildinfo"
)

// DefaultAPIURL is the public releases endpoint for this repository. It needs no
// token — the repository is public — which is why this check adds no config
// field and no env var: there is nothing for an operator to set.
const DefaultAPIURL = "https://api.github.com/repos/atvirokodosprendimai/agentsmemory/releases/latest"

// releasesPage is where the notice sends someone who wants the release notes.
const releasesPage = "https://github.com/atvirokodosprendimai/agentsmemory/releases"

// maxBody caps how much of the API response is read. The payload of interest is
// one short string; the cap is what keeps a hostile or misdirected endpoint from
// turning a startup nicety into unbounded memory.
const maxBody = 1 << 20

// Notice returns the single line to print when a newer release exists, or the
// empty string when nothing should be printed.
//
// The empty string covers every case that is not "there is definitely something
// newer": the current build is up to date or ahead, the current build is not a
// release at all (a dev build has no meaningful tag to compare, and telling a
// developer to upgrade to the tag they are working past is noise), the network
// is unreachable, the API is rate-limited, or the response does not parse. The
// caller therefore needs no error handling — see the package comment.
//
// The client is passed in rather than taken from http.DefaultClient so the
// caller owns the timeout, which is the property that keeps this off the startup
// path, and so a test can drive it against httptest without touching the network.
func Notice(ctx context.Context, client *http.Client, apiURL, current string) string {
	latest, ok := latestTag(ctx, client, apiURL)
	if !ok {
		return ""
	}
	if !newer(latest, current) {
		return ""
	}
	return fmt.Sprintf("update available: %s (you are on %s) — %s", latest, current, releasesPage)
}

// latestTag fetches the tag name of the newest release, reporting ok=false for
// every failure mode rather than an error, because no caller has anything useful
// to do with one.
func latestTag(ctx context.Context, client *http.Client, apiURL string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", false
	}
	// The versioned Accept header is what GitHub's API documents; without it the
	// endpoint is free to change shape under us on its own schedule.
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false // 403 rate-limited, 404 no releases yet, 5xx — all silent
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&payload); err != nil {
		return "", false
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return "", false
	}
	return tag, true
}

// newer reports whether the latest tag is strictly ahead of the current version.
//
// It compares the numeric components only, so the leading "v" that release.yml
// stamps and the tags carry is irrelevant on both sides — which is what lets
// buildinfo.Effective report the tag verbatim without this comparison caring
// which form it took. A version that does not parse is never "older": an
// unrecognised current build (a dev build, a fork's own tag) yields no notice
// rather than a wrong one.
func newer(latest, current string) bool {
	l, ok := parse(latest)
	if !ok {
		return false
	}
	c, ok := parse(current)
	if !ok {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parse reads a "v1.2.3"-shaped version into its three numeric components.
// Anything else — the word "dev", a dev-<revision> string, a pre-release suffix,
// a truncated tag — is rejected, because a comparison this function cannot make
// confidently must produce silence rather than a guess.
func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != len(out) {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// IsRelease reports whether a version string is a release tag this check can
// compare — i.e. whether an update check is meaningful for this build at all.
// Exported so a caller can skip the network round trip entirely on a dev build
// instead of making a request whose answer it would discard.
func IsRelease(current string) bool {
	if strings.HasPrefix(current, buildinfo.Dev) {
		return false
	}
	_, ok := parse(current)
	return ok
}
