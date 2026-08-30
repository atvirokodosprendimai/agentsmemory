package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// releaseAPI stands in for GitHub's latest-release endpoint, answering with the
// status and body a test names.
func releaseAPI(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestNoticeAnnouncesANewerRelease is the case the feature exists for: a patched
// release is out and the running host has no other way to learn it.
func TestNoticeAnnouncesANewerRelease(t *testing.T) {
	url := releaseAPI(t, http.StatusOK, `{"tag_name":"v0.0.102"}`)

	notice := Notice(context.Background(), http.DefaultClient, url, "v0.0.101")
	if notice == "" {
		t.Fatal("a newer release produced no notice, which is the whole of issue #115")
	}
	for _, want := range []string{"v0.0.102", "v0.0.101", releasesPage} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice does not name %q, so the operator cannot act on it:\n%s", want, notice)
		}
	}
}

// TestNoticeIsSilentWhenNothingIsNewer covers up-to-date and ahead-of-latest.
// A host running a build newer than the published release — a release candidate,
// a local build of main — must not be told to downgrade.
func TestNoticeIsSilentWhenNothingIsNewer(t *testing.T) {
	url := releaseAPI(t, http.StatusOK, `{"tag_name":"v0.0.101"}`)

	for _, current := range []string{"v0.0.101", "v0.0.102", "v0.1.0", "v1.0.0"} {
		if notice := Notice(context.Background(), http.DefaultClient, url, current); notice != "" {
			t.Errorf("Notice(current=%q) = %q; want silence", current, notice)
		}
	}
}

// TestNoticeComparesNumericallyNotLexically: "v0.0.9" sorts after "v0.0.102" as
// text, and a string comparison would tell every host on the newest build to
// upgrade to an ancient one.
func TestNoticeComparesNumericallyNotLexically(t *testing.T) {
	url := releaseAPI(t, http.StatusOK, `{"tag_name":"v0.0.9"}`)

	if notice := Notice(context.Background(), http.DefaultClient, url, "v0.0.102"); notice != "" {
		t.Errorf("Notice = %q; v0.0.9 is older than v0.0.102 — this is a string compare, not a "+
			"version compare", notice)
	}
}

// TestNoticeFailsOpen is the ADR-025 property: every way the check can go wrong
// resolves to the same output as "you are up to date". A startup nicety must
// never be able to complain — see the package comment.
func TestNoticeFailsOpen(t *testing.T) {
	unreachable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unreachableURL := unreachable.URL
	unreachable.Close() // nothing is listening now

	cases := []struct {
		name string
		url  string
	}{
		{"rate limited", releaseAPI(t, http.StatusForbidden, `{"message":"API rate limit exceeded"}`)},
		{"no releases yet", releaseAPI(t, http.StatusNotFound, `{"message":"Not Found"}`)},
		{"server error", releaseAPI(t, http.StatusInternalServerError, ``)},
		{"malformed json", releaseAPI(t, http.StatusOK, `not json at all`)},
		{"empty tag", releaseAPI(t, http.StatusOK, `{"tag_name":""}`)},
		{"unparsable tag", releaseAPI(t, http.StatusOK, `{"tag_name":"nightly"}`)},
		{"offline", unreachableURL},
		{"not a url", "://nonsense"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if notice := Notice(context.Background(), http.DefaultClient, tc.url, "v0.0.101"); notice != "" {
				t.Errorf("Notice = %q; want silence — a failed update check must be indistinguishable "+
					"from an up-to-date one", notice)
			}
		})
	}
}

// TestNoticeRespectsADeadline: the caller owns the timeout, and that is what
// keeps the check off the startup path. A hung endpoint must return, silently.
func TestNoticeRespectsADeadline(t *testing.T) {
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blocked:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan string, 1)
	go func() { done <- Notice(ctx, srv.Client(), srv.URL, "v0.0.101") }()
	select {
	case notice := <-done:
		if notice != "" {
			t.Errorf("Notice = %q on a timed-out request; want silence", notice)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Notice ignored its context deadline — on the startup path this would hang the banner")
	}
}

// TestIsReleaseRejectsDevBuilds. A dev build has no tag to compare, and telling a
// developer to upgrade to the release they are working past is noise. The caller
// uses this to skip the request entirely, which is also what keeps the server's
// own test run off the network.
func TestIsReleaseRejectsDevBuilds(t *testing.T) {
	for _, v := range []string{"dev", "dev-df4857d01234", "dev-df4857d01234+dirty", "", "nightly", "v0.0"} {
		if IsRelease(v) {
			t.Errorf("IsRelease(%q) = true; a build with no comparable tag must skip the check", v)
		}
	}
	for _, v := range []string{"v0.0.101", "0.0.101"} {
		if !IsRelease(v) {
			t.Errorf("IsRelease(%q) = false; a release build must be checked", v)
		}
	}
}
