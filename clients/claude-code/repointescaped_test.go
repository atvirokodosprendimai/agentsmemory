package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// escapedHooksFile writes a hooks file whose commands carry the endpoint with JSON's
// optional `\/` escape, which is what several writers emit and what this machine's
// settings.json actually contained when the defect was found.
func escapedHooksFile(t *testing.T, endpoint string) string {
	t.Helper()
	dir := t.TempDir()
	cmd := mcpURLEnvVar + "='" + endpoint + "'" + " bash -- '/tmp/agentsmemory-stop-hook.sh'"
	doc := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": cmd}},
	}}}}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Escape every forward slash, exactly as the writer that produced the real file
	// did. This is legal JSON and parses back to the same string.
	escaped := strings.ReplaceAll(string(body), "/", `\/`)
	if !json.Valid([]byte(escaped)) {
		t.Fatalf("the fixture is not valid JSON, so it does not reproduce anything:\n%s", escaped)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(escaped), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRepointWarningReadsAnEscapedEndpoint pins that the REPOINT warning compares
// endpoints rather than the escaping around them.
//
// ⚠ THE WARNING FIRED ON AN INSTALL THAT REPOINTED NOTHING. warnIfRepointing matches
// the RAW TEXT of the hooks file — deliberately, because codex keeps its hooks in
// config.toml and the JSON unmarshal this replaced warned nobody there. The cost is
// that JSON's optional `\/` escape reaches the comparison intact, so a file saying
// `http:\/\/localhost:8080\/mcp` never equals `http://localhost:8080/mcp`.
//
// Measured 2026-09-02 against a healthy install: the warning fired, and because a
// backslash is not legal in a host both endpoints in the message rendered as
// "(an endpoint that does not parse)" — so it named no URL, and its own advice read
// `re-run with --mcp-url (an endpoint that does not parse)`. A false alarm on a
// healthy install is how a check earns being switched off, which is the thing this
// warning can least afford.
func TestRepointWarningReadsAnEscapedEndpoint(t *testing.T) {
	const endpoint = "http://localhost:8080/mcp"

	t.Run("the same endpoint, escaped, is not a repoint", func(t *testing.T) {
		var out bytes.Buffer
		i := &Installer{mcpURL: endpoint, out: &out}
		i.warnIfRepointing(escapedHooksFile(t, endpoint))
		if strings.Contains(out.String(), "REPOINTS") {
			t.Errorf("warned about a repoint that is not happening:\n%s", out.String())
		}
	})

	// The half that keeps the fix honest. Decoding must not make every endpoint
	// compare equal -- a decoder that returned a constant would pass the case above.
	t.Run("a genuinely different endpoint still warns, and names itself", func(t *testing.T) {
		var out bytes.Buffer
		i := &Installer{mcpURL: endpoint, out: &out}
		i.warnIfRepointing(escapedHooksFile(t, "https://aiagentmemory.dev/mcp"))
		got := out.String()
		if !strings.Contains(got, "REPOINTS") {
			t.Fatalf("a real repoint went unwarned:\n%s", got)
		}
		if !strings.Contains(got, "https://aiagentmemory.dev/mcp") {
			t.Errorf("the warning does not name the endpoint the hooks currently talk to, "+
				"so a reader cannot act on it:\n%s", got)
		}
		if strings.Contains(got, "does not parse") {
			t.Errorf("the warning still renders an endpoint it could not parse:\n%s", got)
		}
	})
}
