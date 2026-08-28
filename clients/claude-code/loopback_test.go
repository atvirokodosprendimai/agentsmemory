package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

// TestALoopbackEndpointIsRecognisedByItsHostNotItsSpelling is the security half
// of the no-token-for-loopback rule.
//
// ⚠ THE OBVIOUS IMPLEMENTATION IS WRONG AND LOOKS RIGHT.
// strings.Contains(url, "localhost") accepts http://localhost.example.invalid/mcp,
// which is a REMOTE host, and this function decides when it is safe to send no
// credential at all. A generous match here silently strips authentication from
// somebody else's server.
func TestALoopbackEndpointIsRecognisedByItsHostNotItsSpelling(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
		why  string
	}{
		{"http://localhost:8080/mcp", true, "the --local default"},
		{"http://127.0.0.1:8080/mcp", true, "the same machine by address"},
		{"http://[::1]:8080/mcp", true, "IPv6 loopback"},
		{"http://127.9.9.9:8080/mcp", true, "all of 127/8 is loopback"},
		{"https://aiagentmemory.dev/mcp", false, "the hosted service"},
		{"http://localhost.example.invalid/mcp", false,
			"a REMOTE host whose name merely starts with localhost — a substring match accepts this"},
		{"https://not-localhost.example.invalid/mcp", false,
			"a REMOTE host whose name merely contains localhost"},
		{"http://10.0.0.5:8080/mcp", false, "a private address is not this machine"},
		{"", false, "no endpoint at all"},
		{"://nonsense", false, "an unparseable endpoint is not trusted"},
	} {
		if got := isLoopbackEndpoint(tc.url); got != tc.want {
			t.Errorf("isLoopbackEndpoint(%q) = %v, want %v — %s", tc.url, got, tc.want, tc.why)
		}
	}
}

// TestNoTokenMeansNoAuthorizationHeader drives dialMCP against a real server and
// reads what arrived.
//
// An empty token used to produce `Authorization: Bearer ` — a credential that was
// offered and is blank. No server here is tested against that shape, and the case
// that DOES work is the one a --local registration already uses: no header at all.
// Asserting on what the server received is the only way to see this; the client
// call succeeds either way.
func TestNoTokenMeansNoAuthorizationHeader(t *testing.T) {
	var got string
	var seen bool
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, hdr, seen = r.Header.Get("Authorization"), r.Header.Clone(), true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2024-11-05",` +
			`"capabilities":{},"serverInfo":{"name":"t","version":"0"}}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c, err := dialMCP(ctx, srv.URL, "", 5*time.Second); err == nil {
		defer c.Close()
	}
	if !seen {
		t.Fatal("the server was never reached, so this test asserts nothing about the header")
	}
	// ⚠ PRESENCE IN THE MAP, NOT Get() == "". Header.Get returns "" for a header
	// that is ABSENT and for one that is PRESENT AND EMPTY, so the obvious
	// assertion passes for the exact defect this test is named after. Caught in
	// review: the test claimed to prove omission and could not distinguish it.
	if _, present := hdr["Authorization"]; present {
		t.Errorf("with no token the request still CARRIED an Authorization header (%q); an empty "+
			"bearer is a credential offered blank, and no server here is tested against it", got)
	}

	seen = false
	if c, err := dialMCP(ctx, srv.URL, "sk-real", 5*time.Second); err == nil {
		defer c.Close()
	}
	// The other half: without it, "send no header" is satisfied by never sending one.
	if got != "Bearer sk-real" {
		t.Errorf("a configured token did not reach the server verbatim: Authorization=%q", got)
	}
}

// TestALoopbackInstallNeedsNoToken drives the resolution the hooks actually use.
//
// ⚠ IT POINTS --config-dir AT AN EMPTY DIRECTORY ON PURPOSE. The developer's real
// config dir usually holds a token, so without this the check passes because a
// token was FOUND, not because loopback was allowed — measuring something adjacent
// to the path under test.
func TestALoopbackInstallNeedsNoToken(t *testing.T) {
	empty := t.TempDir()
	if entries, err := os.ReadDir(empty); err != nil || len(entries) != 0 {
		t.Fatalf("the isolation directory is not empty (%v, %d entries)", err, len(entries))
	}

	token, source, err := resolveTokenFor(t, empty, "http://localhost:8080/mcp")
	if err != nil {
		t.Fatalf("a --local install populates no token source by design, so refusing here makes "+
			"every hook silent against a server that accepts no credentials: %v", err)
	}
	if token != "" {
		t.Errorf("resolved token %q from an empty config dir", token)
	}
	if !strings.Contains(source, "loopback") {
		t.Errorf("source = %q, want it to name the loopback allowance so the stderr note explains "+
			"why no credential was sent", source)
	}

	if _, _, err := resolveTokenFor(t, empty, "https://aiagentmemory.dev/mcp"); err == nil {
		t.Error("a REMOTE endpoint with no token resolved anyway — the loopback allowance must " +
			"not become a blanket one")
	}

	// A configured token still wins on loopback: a --local server may have been
	// started with one.
	withToken := t.TempDir()
	if err := os.WriteFile(filepath.Join(withToken, tokenFile),
		[]byte("AGENTSMEMORY_TOKEN=sk_local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, err := resolveTokenFor(t, withToken, "http://localhost:8080/mcp")
	if err != nil || got != "sk_local" {
		t.Errorf("resolveWorkspaceToken on loopback = (%q, %v), want sk_local — a configured "+
			"token must still win", got, err)
	}
}

// resolveTokenFor drives resolveWorkspaceToken through a real command with real
// flag parsing, rather than fabricating a cli.Command. --config-dir pins the
// search to one directory so the developer's own install cannot decide the result.
func resolveTokenFor(t *testing.T, configDir, mcpURL string) (token, source string, err error) {
	t.Helper()
	// The env vars these flags declare as Sources would otherwise leak the
	// developer's real token into the run.
	t.Setenv(tokenEnvVar, "")
	t.Setenv(mcpURLEnvVar, "")
	cmd := &cli.Command{
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "token", Sources: cli.EnvVars(tokenEnvVar)},
			&cli.StringFlag{Name: "mcp-url", Sources: cli.EnvVars(mcpURLEnvVar)},
			&cli.StringFlag{Name: "config-dir"},
			&cli.StringFlag{Name: "sandbox"},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			token, source, err = resolveWorkspaceToken(c)
			return nil
		},
	}
	if runErr := cmd.Run(context.Background(), []string{"x",
		"--config-dir", configDir, "--mcp-url", mcpURL}); runErr != nil {
		t.Fatalf("command run: %v", runErr)
	}
	return token, source, err
}

// TestAnInstallSaysSoWhenItRepointsYourHooks pins the warning that was missing.
//
// ⚠ THE SILENT VERSION COST A WHOLE SESSION. `install --agent claude` with no
// --mcp-url takes the hosted default, and the default wins over what is already
// configured: on 2026-08-28 that repointed five working hooks from a local server
// to the hosted one, every hook went mute because the local credential did not
// authenticate there, and nothing said a word. The symptom read as broken hooks;
// the cause was a re-install.
func TestAnInstallSaysSoWhenItRepointsYourHooks(t *testing.T) {
	write := func(t *testing.T, dir, url string) {
		t.Helper()
		body := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":` +
			`"` + mcpURLEnvVar + `='` + url + `' bash -- '/x/agentsmemory-verify-hook.sh'"}]}]}}`
		if err := os.WriteFile(filepath.Join(dir, claudeKit.hooksFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	warn := func(t *testing.T, existing, installing string) string {
		t.Helper()
		inst, _, dir := newTestInstaller(t, false)
		write(t, dir, existing)
		inst.mcpURL = installing
		buf := &bytes.Buffer{}
		inst.out = buf
		inst.warnIfRepointing(filepath.Join(dir, claudeKit.hooksFile))
		return buf.String()
	}

	// ⚠ ALSO EXERCISED IN THE CODEX SHAPE. The first version unmarshalled JSON, so
	// it silently warned nobody on codex, whose hooks live in config.toml — and the
	// test only ever supplied Claude JSON, so it could not see that. Found in review.
	t.Run("codex TOML is not invisible to it", func(t *testing.T) {
		inst, _, dir := newTestInstallerFor(t, codexKit, false)
		body := "[[hooks]]\ncommand = \"" + mcpURLEnvVar + "='http://localhost:8080/mcp' bash -- '/x/agentsmemory-stop-hook.sh'\"\n"
		if err := os.WriteFile(filepath.Join(dir, codexKit.hooksFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		inst.mcpURL = "https://aiagentmemory.dev/mcp"
		buf := &bytes.Buffer{}
		inst.out = buf
		inst.warnIfRepointing(filepath.Join(dir, codexKit.hooksFile))
		if !strings.Contains(buf.String(), "REPOINTS") {
			t.Errorf("no repoint warning for a codex install: %q\n"+
				"A warning that reaches one agent and silently not another is the "+
				"reachability defect this repo is named for.", buf.String())
		}
	})

	// ⚠ AN ENDPOINT CAN CARRY A CREDENTIAL, and this message goes to terminals,
	// logs and pasted bug reports. Found in review.
	t.Run("a credential in the endpoint is not printed", func(t *testing.T) {
		got := warn(t, "https://user:hunter2@example.invalid/mcp?sig=SECRETVALUE", "https://aiagentmemory.dev/mcp")
		for _, secret := range []string{"hunter2", "SECRETVALUE"} {
			if strings.Contains(got, secret) {
				t.Errorf("the warning printed %q from the endpoint: %q", secret, got)
			}
		}
		if !strings.Contains(got, "example.invalid") {
			t.Errorf("redaction removed the host too, leaving nothing actionable: %q", got)
		}
	})

	got := warn(t, "http://localhost:8080/mcp", "https://aiagentmemory.dev/mcp")
	for _, want := range []string{"http://localhost:8080/mcp", "https://aiagentmemory.dev/mcp"} {
		if !strings.Contains(got, want) {
			t.Errorf("the repoint warning does not name %s: %q\n"+
				"Both URLs have to appear — being told the hooks changed server is useless "+
				"without knowing which one they left.", want, got)
		}
	}

	// The other half, and without it "warns" is satisfied by warning always.
	if quiet := warn(t, "http://localhost:8080/mcp", "http://localhost:8080/mcp"); strings.Contains(quiet, "REPOINT") {
		t.Errorf("re-installing against the SAME endpoint warned anyway: %q\n"+
			"A warning that fires on every install is one people stop reading.", quiet)
	}
}

// TestAWaivedCredentialDoesNotTravelThroughARedirect closes the hole the
// loopback waiver opened.
//
// ⚠ THE AUTH DECISION IS MADE ABOUT AN ENDPOINT; A REDIRECT CHANGES THE
// ENDPOINT. resolveWorkspaceToken waives the credential because the URL is on
// this machine, but mcp-go builds a bare &http.Client{} with no CheckRedirect,
// so Go follows redirects by default — and a 307 replays the MCP request BODY to
// whatever host the redirect names. The waiver was for this machine; without a
// CheckRedirect it silently extends to any host a loopback server cares to pick.
//
// Found in review, not by the tests above: every one of them dials a server that
// answers directly.
func TestAWaivedCredentialDoesNotTravelThroughARedirect(t *testing.T) {
	var remoteHits int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()

	// httptest listens on 127.0.0.1, which IS loopback — so the redirect target
	// has to be spelled as a non-loopback host for this to test anything. It never
	// resolves; refusing before the hop is the behaviour under test.
	for _, code := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://not-this-machine.example.invalid/mcp", code)
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		before := remoteHits
		c, err := dialMCP(ctx, redirector.URL, "", 5*time.Second)
		if err == nil {
			c.Close()
			t.Errorf("a %d redirect off this machine was followed with no credential; the "+
				"loopback waiver must not travel to another host", code)
		} else if !strings.Contains(err.Error(), "refusing a redirect") {
			// ⚠ THIS MUST BE AN ASSERTION, NOT A LOG. It was a t.Logf first, and the
			// mutant SURVIVED: the redirect target is a .invalid host that never
			// resolves, so removing CheckRedirect entirely still fails the dial — with
			// a DNS error instead of a refusal. "It failed somehow" is satisfied by the
			// guard being absent. The refusal has to be the reason.
			t.Errorf("a %d redirect failed for the wrong reason (%v); without the redirect guard "+
				"this dial fails on DNS anyway, so only the refusal proves the guard ran", code, err)
		}
		if remoteHits != before {
			t.Errorf("the redirect target was contacted on a %d", code)
		}
		cancel()
		redirector.Close()
	}
}
