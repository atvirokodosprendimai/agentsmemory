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
)

// kitServer stands in for raw.githubusercontent: it serves the kit assets at
// <ref>/clients/claude-code/<name> and records what was requested, so a test can
// assert both the content written and the URLs the fetch actually built.
type kitServer struct {
	body      map[string]string // asset name → contents ("" ⇒ 404)
	requested []string          // paths served, in order
}

func newKitServer(t *testing.T, s *kitServer) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requested = append(s.requested, r.URL.Path)
		name := r.URL.Path
		if i := strings.Index(name, kitAssetDir+"/"); i >= 0 {
			name = name[i+len(kitAssetDir)+1:]
		}
		body, ok := s.body[name]
		if !ok || body == "" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	// rawBaseURL is package-level; restore it so tests stay independent.
	prev := rawBaseURL
	rawBaseURL = srv.URL
	t.Cleanup(func() { rawBaseURL = prev })
	return srv
}

// freshKit is the content the fake origin serves for a normal update. It is
// derived from commandAssets so a command added to the kit is served here
// automatically rather than silently going untested.
func freshKit() map[string]string {
	body := map[string]string{bootstrapAsset: "# new protocol\n"}
	for _, name := range commandAssets {
		body["commands/"+name] = "# new " + name + "\n"
	}
	return body
}

// installedKit seeds a config dir with an older kit, the state update-skill is
// meant to find and replace.
func installedKit(t *testing.T, kit agentKit) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, kit.commandsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range commandAssets {
		write(t, filepath.Join(dir, kit.commandsDir, name), "# old "+name+"\n")
	}
	write(t, filepath.Join(dir, bootstrapFile), "# old protocol\n")
	return dir
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestUpdateSkillWritesFetchedKit is the happy path: every command and the
// protocol are replaced with what the origin served, and Claude's memory file
// gains the @import rather than the protocol text.
func TestUpdateSkillWritesFetchedKit(t *testing.T) {
	newKitServer(t, &kitServer{body: freshKit()})
	dir := installedKit(t, claudeKit)

	var out bytes.Buffer
	err := updateSkills(context.Background(), &out, []agentKit{claudeKit},
		skillUpdate{configDir: dir, ref: "v9.9.9"})
	if err != nil {
		t.Fatalf("updateSkills: %v", err)
	}

	for _, name := range commandAssets {
		if got := read(t, filepath.Join(dir, claudeKit.commandsDir, name)); got != "# new "+name+"\n" {
			t.Errorf("command %s = %q, want the fetched copy", name, got)
		}
	}
	if got := read(t, filepath.Join(dir, bootstrapFile)); got != "# new protocol\n" {
		t.Errorf("%s = %q, want the fetched copy", bootstrapFile, got)
	}
	// Claude resolves @imports, so the managed block names the sibling file
	// instead of carrying a second copy of the protocol.
	claudeMD := read(t, filepath.Join(dir, claudeKit.memoryFile))
	if !strings.Contains(claudeMD, memoryImportLine) {
		t.Errorf("%s missing the import line:\n%s", claudeKit.memoryFile, claudeMD)
	}
	if strings.Contains(claudeMD, "# new protocol") {
		t.Errorf("%s inlined the protocol; Claude should import it:\n%s", claudeKit.memoryFile, claudeMD)
	}
}

// TestUpdateSkillInlinesProtocolForCodex covers the other half of the split:
// AGENTS.md has no import directive, so the protocol text itself must land in
// the managed block. This is the behaviour the shared installer write path
// exists to preserve.
func TestUpdateSkillInlinesProtocolForCodex(t *testing.T) {
	newKitServer(t, &kitServer{body: freshKit()})
	dir := installedKit(t, codexKit)

	var out bytes.Buffer
	if err := updateSkills(context.Background(), &out, []agentKit{codexKit},
		skillUpdate{configDir: dir, ref: "v9.9.9"}); err != nil {
		t.Fatalf("updateSkills: %v", err)
	}

	agentsMD := read(t, filepath.Join(dir, codexKit.memoryFile))
	if !strings.Contains(agentsMD, "# new protocol") {
		t.Errorf("%s should inline the protocol:\n%s", codexKit.memoryFile, agentsMD)
	}
	// Commands land in prompts/, not commands/.
	if _, err := os.Stat(filepath.Join(dir, "prompts", "am.md")); err != nil {
		t.Errorf("codex command not written to prompts/: %v", err)
	}
}

// TestUpdateSkillFailedFetchWritesNothing is the reason the download is
// all-or-nothing: a 404 on the last asset must not leave a config dir carrying
// two new commands and one stale one.
func TestUpdateSkillFailedFetchWritesNothing(t *testing.T) {
	body := freshKit()
	body["commands/load-skill.md"] = "" // 404
	newKitServer(t, &kitServer{body: body})
	dir := installedKit(t, claudeKit)

	var out bytes.Buffer
	err := updateSkills(context.Background(), &out, []agentKit{claudeKit},
		skillUpdate{configDir: dir, ref: "v9.9.9"})
	if err == nil {
		t.Fatal("expected an error when an asset is missing")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should name the missing asset, got: %v", err)
	}
	for _, name := range commandAssets {
		if got := read(t, filepath.Join(dir, claudeKit.commandsDir, name)); got != "# old "+name+"\n" {
			t.Errorf("command %s was modified by a failed update: %q", name, got)
		}
	}
	if got := read(t, filepath.Join(dir, bootstrapFile)); got != "# old protocol\n" {
		t.Errorf("protocol was modified by a failed update: %q", got)
	}
}

// TestUpdateSkillCheckWritesNothing verifies --check reports drift and leaves
// every file alone.
func TestUpdateSkillCheckWritesNothing(t *testing.T) {
	newKitServer(t, &kitServer{body: freshKit()})
	dir := installedKit(t, claudeKit)
	// One file already matches, so the report must distinguish the two states.
	write(t, filepath.Join(dir, claudeKit.commandsDir, "am.md"), "# new am.md\n")

	var out bytes.Buffer
	if err := updateSkills(context.Background(), &out, []agentKit{claudeKit},
		skillUpdate{configDir: dir, ref: "v9.9.9", check: true}); err != nil {
		t.Fatalf("updateSkills --check: %v", err)
	}

	if got := read(t, filepath.Join(dir, bootstrapFile)); got != "# old protocol\n" {
		t.Errorf("--check wrote to %s: %q", bootstrapFile, got)
	}
	if _, err := os.Stat(filepath.Join(dir, claudeKit.memoryFile)); !os.IsNotExist(err) {
		t.Errorf("--check created %s", claudeKit.memoryFile)
	}
	report := out.String()
	if !strings.Contains(report, "would be updated") {
		t.Errorf("--check should report drift:\n%s", report)
	}
	if !strings.Contains(report, "run without --check to apply") {
		t.Errorf("--check should say how to apply:\n%s", report)
	}
}

// TestUpdateSkillRefusesMissingConfigDir: a typo'd --sandbox must fail loudly
// rather than create a directory that looks installed but has no MCP or hook.
func TestUpdateSkillRefusesMissingConfigDir(t *testing.T) {
	newKitServer(t, &kitServer{body: freshKit()})
	missing := filepath.Join(t.TempDir(), "nope")

	err := updateSkills(context.Background(), &bytes.Buffer{}, []agentKit{claudeKit},
		skillUpdate{configDir: missing, ref: "v9.9.9"})
	if err == nil {
		t.Fatal("expected an error for a config dir that does not exist")
	}
	if !strings.Contains(err.Error(), "install it first") {
		t.Errorf("error should point at install, got: %v", err)
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Error("the missing config dir was created anyway")
	}
}

// TestUpdateSkillResolvesSandbox confirms --sandbox targets
// ~/.sandboxes/<name>, which is how this is expected to be used day to day.
func TestUpdateSkillResolvesSandbox(t *testing.T) {
	newKitServer(t, &kitServer{body: freshKit()})
	home := t.TempDir()
	t.Setenv("HOME", home)

	box := filepath.Join(home, ".sandboxes", "aks")
	if err := os.MkdirAll(filepath.Join(box, claudeKit.commandsDir), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := updateSkills(context.Background(), &out, []agentKit{claudeKit},
		skillUpdate{sandbox: "aks", ref: "v9.9.9"}); err != nil {
		t.Fatalf("updateSkills --sandbox: %v", err)
	}
	if got := read(t, filepath.Join(box, "commands", "am.md")); got != "# new am.md\n" {
		t.Errorf("sandbox command = %q, want the fetched copy", got)
	}
	if got := read(t, filepath.Join(box, bootstrapFile)); got != "# new protocol\n" {
		t.Errorf("sandbox protocol = %q, want the fetched copy", got)
	}
}

// TestUpdateSkillFetchesOnceForEveryAgent: the kit is downloaded before any
// target is touched, so refreshing three agents must not triple the requests.
func TestUpdateSkillFetchesOnceForEveryAgent(t *testing.T) {
	srv := &kitServer{body: freshKit()}
	newKitServer(t, srv)

	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, kit := range []agentKit{claudeKit, codexKit, piKit} {
		if err := os.MkdirAll(kit.globalConfigDir(home), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := updateSkills(context.Background(), &out, []agentKit{claudeKit, codexKit, piKit},
		skillUpdate{global: true, ref: "v9.9.9"}); err != nil {
		t.Fatalf("updateSkills: %v", err)
	}
	if len(srv.requested) != len(skillAssets()) {
		t.Errorf("made %d requests for %d assets across 3 agents: %v",
			len(srv.requested), len(skillAssets()), srv.requested)
	}
	// Every agent still got the kit.
	for _, kit := range []agentKit{claudeKit, codexKit, piKit} {
		p := filepath.Join(kit.globalConfigDir(home), kit.commandsDir, "am.md")
		if got := read(t, p); got != "# new am.md\n" {
			t.Errorf("%s: %s = %q", kit.name, p, got)
		}
	}
}

// TestUpdateSkillLeavesHookAndExtensionAlone pins the deliberate scope: the kit
// markdown is refreshed, executable assets are not.
func TestUpdateSkillLeavesHookAndExtensionAlone(t *testing.T) {
	newKitServer(t, &kitServer{body: freshKit()})
	dir := installedKit(t, claudeKit)
	hook := filepath.Join(dir, hookFile)
	write(t, hook, "# installed hook\n")

	if err := updateSkills(context.Background(), &bytes.Buffer{}, []agentKit{claudeKit},
		skillUpdate{configDir: dir, ref: "v9.9.9"}); err != nil {
		t.Fatalf("updateSkills: %v", err)
	}
	if got := read(t, hook); got != "# installed hook\n" {
		t.Errorf("the Stop hook was rewritten: %q", got)
	}
}

// TestFetchAssetRejectsOversized: the cap exists so a wrong URL or a hostile
// mirror cannot stream without end into memory. One byte over must be refused
// rather than truncated into a plausible-looking file.
func TestFetchAssetRejectsOversized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), maxAssetBytes+1))
	}))
	defer srv.Close()

	_, err := fetchAsset(context.Background(), srv.URL+"/x.md")
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("got %v, want a size error", err)
	}
}

// TestFetchAssetRejectsEmptyBody is separate because an empty 200 is a distinct
// failure from a 404: it would install cleanly and silently blank a command.
func TestFetchAssetRejectsEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200 with no body
	}))
	defer srv.Close()

	_, err := fetchAsset(context.Background(), srv.URL+"/x.md")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("got %v, want an empty-file error", err)
	}
}

func TestValidRef(t *testing.T) {
	valid := []string{"v0.0.72", "main", "feat/update-skill", "release_1.2"}
	for _, ref := range valid {
		if err := validRef(ref); err != nil {
			t.Errorf("validRef(%q) = %v, want nil", ref, err)
		}
	}
	// Traversal and URL-splitting characters would change WHICH file is fetched
	// rather than which version of it, so they are rejected outright.
	invalid := []string{"", "..", "../../etc", "/main", ".hidden", "main?x=1", "a b", "main#frag", "a//b"}
	for _, ref := range invalid {
		if err := validRef(ref); err == nil {
			t.Errorf("validRef(%q) = nil, want an error", ref)
		}
	}
}

func TestInstalledPath(t *testing.T) {
	tests := []struct {
		kit   agentKit
		asset string
		want  string
	}{
		{claudeKit, "commands/am.md", filepath.Join("/cfg", "commands", "am.md")},
		{codexKit, "commands/am.md", filepath.Join("/cfg", "prompts", "am.md")},
		{piKit, "commands/load-skill.md", filepath.Join("/cfg", "prompts", "load-skill.md")},
		{claudeKit, bootstrapAsset, filepath.Join("/cfg", bootstrapFile)},
	}
	for _, tc := range tests {
		if got := installedPath(tc.kit, "/cfg", tc.asset); got != tc.want {
			t.Errorf("installedPath(%s, %q) = %q, want %q", tc.kit.name, tc.asset, got, tc.want)
		}
	}
}
