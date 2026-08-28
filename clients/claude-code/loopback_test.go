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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, seen = r.Header.Get("Authorization"), true
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
	if got != "" {
		t.Errorf("with no token the request still carried Authorization=%q; an empty bearer is a "+
			"credential offered blank, and no server here is tested against it", got)
	}

	seen = false
	if c, err := dialMCP(ctx, srv.URL, "sk-real", 5*time.Second); err == nil {
		defer c.Close()
	}
	// The other half: without it, "send no header" is satisfied by never sending one.
	if !strings.Contains(got, "sk-real") {
		t.Errorf("a configured token did not reach the server: Authorization=%q", got)
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
