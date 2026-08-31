package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/urfave/cli/v3"
)

// fixture builds a transcript with every kind of line the extractor must judge.
const mineFixture = `{"type":"user","cwd":"/home/u/proj","gitBranch":"main","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"why does the deploy fail on app2?"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","text":"let me think about this at great length"},{"type":"text","text":"The health check races the migration."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"kubectl get pods"}}]}}
{"type":"user","isMeta":true,"message":{"role":"user","content":"meta housekeeping line"}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","text":"pod-1 Running"}]}}
{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent chatter that is not the conversation"}]}}
{"type":"user","message":{"role":"user","content":"Caveat: the messages below were generated while running local commands."}}
{"type":"user","message":{"role":"user","content":"<system-reminder>injected context</system-reminder>so fix the race?"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Yes — gate the check on the migration job."}]}}
{"type":"file-history-snapshot","messageId":"x"}
`

// TestExtractSessionKeepsSpeechDropsMachinery is the miner's whole contract:
// ~90% of a transcript is tool traffic, and a palace mined without this filter
// recalls noise.
func TestExtractSessionKeepsSpeechDropsMachinery(t *testing.T) {
	doc := extractSession(strings.NewReader(mineFixture))

	if len(doc.Turns) != 4 {
		t.Fatalf("extracted %d turns, want 4: %q", len(doc.Turns), doc.Turns)
	}
	joined := strings.Join(doc.Turns, "\n")
	for _, want := range []string{"deploy fail on app2", "health check races", "fix the race", "gate the check"} {
		if !strings.Contains(joined, want) {
			t.Errorf("conversation lost %q", want)
		}
	}
	for _, banned := range []string{"thinking about this", "kubectl", "pod-1", "subagent chatter", "Caveat:", "injected context", "meta housekeeping"} {
		if strings.Contains(joined, banned) {
			t.Errorf("machinery leaked into the corpus: %q", banned)
		}
	}
	if doc.Cwd != "/home/u/proj" || doc.Branch != "main" {
		t.Errorf("session origin lost: cwd=%q branch=%q", doc.Cwd, doc.Branch)
	}
}

// TestRenderSplitsMarathonSessions: one enormous session must become several
// parts with distinct source ids, or idempotent re-mining stops being true for
// exactly the sessions with the most in them.
func TestRenderSplitsMarathonSessions(t *testing.T) {
	doc := sessionDoc{Started: "2026-08-01"}
	turn := "A: " + strings.Repeat("insight ", 400) // ~3.2KB per turn
	for i := 0; i < 60; i++ {                       // ~190KB total, over the cap
		doc.Turns = append(doc.Turns, turn)
	}
	parts := doc.render("proj", "session-1")
	if len(parts) < 2 {
		t.Fatalf("a %dKB session rendered as %d part(s), want a split", 60*len(turn)/1000, len(parts))
	}
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p.Source] {
			t.Errorf("duplicate source id %q — re-mining would clobber a sibling part", p.Source)
		}
		seen[p.Source] = true
		if len(p.Content) > mineDocCap+4000 {
			t.Errorf("part %q is %d runes, cap is %d", p.Source, len(p.Content), mineDocCap)
		}
	}
}

// TestShortSessionIDToleratesShortNames: the session id is a FILENAME under the
// transcripts root, so nothing guarantees it is a UUID. Abbreviating it for a
// progress line must not be able to end a mining run.
func TestShortSessionIDToleratesShortNames(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"01234567", "01234567"},
		{"0123456789abcdef", "01234567"},
		{"ąčęėįšųū-session", "ąčęėįšųū"}, // runes, not bytes: a byte slice would split one
	} {
		if got := shortSessionID(tc.in); got != tc.want {
			t.Errorf("shortSessionID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestWingForSessionSurvivesDeletedProjects: an old project's directory may be
// gone; its sessions still deserve their own wing rather than one shared pile.
func TestWingForSessionSurvivesDeletedProjects(t *testing.T) {
	if w := wingForSession("/home/u/very-old-project", "-home-u-very-old-project"); w != "wing_very-old-project" {
		t.Errorf("wing from a vanished cwd = %q", w)
	}
	if w := wingForSession("", "-home-u-ancient"); !strings.HasPrefix(w, "wing_") {
		t.Errorf("wing without any cwd = %q", w)
	}
}

// stubMineClient answers am_mine calls, failing the failAt'th one (1-based, 0
// never fails), and records every source it was asked for.
//
// It fails by CALL INDEX rather than by source substring: a source is built from
// the transcript's own directory, so matching on one silently matched nothing and
// the first draft of this test passed against the unfixed loop.
type stubMineClient struct {
	failAt int
	seen   []string
}

func (s *stubMineClient) CallTool(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	source, _ := args["source"].(string)
	s.seen = append(s.seen, source)
	if s.failAt > 0 && len(s.seen) == s.failAt {
		// The exact failure the report hit: the server's embed budget, surfaced to
		// the client as a context deadline.
		return nil, errors.New(`Post "http://ollama:11434/api/embed": context deadline exceeded`)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "ok"}}}, nil
}

// mineFixtureDir writes n transcripts big enough to clear the min-chars floor,
// each rendering to turnsPerSession parts, and returns their paths.
//
// ⚠ turnsPerSession EXISTS BECAUSE ONE PART PER SESSION LEFT A BRANCH UNTESTED.
// A session whose parts PARTLY land takes the partFailed path — counted skipped
// rather than mined — and a fixture that renders one part each can never reach
// it. Found by review, 2026-08-31.
func mineFixtureDir(t *testing.T, n, partsPerSession int) []string {
	t.Helper()
	dir := t.TempDir()
	// ⚠ SIZED OFF THE REAL CAPS, because the obvious fixture does not split. A
	// turn is trimmed to mineUserTurnCap first, so one enormous turn still yields
	// ONE part however long it is — the first version of this helper wrote a
	// 45000-rune turn and got a single part. Parts come from turn COUNT: each
	// full turn contributes mineUserTurnCap runes toward mineDocCap.
	turnsPerPart := mineDocCap/mineUserTurnCap + 1
	body := strings.Repeat("the deploy races the migration and the health check wins. ", 40)
	full := strings.Repeat("x", mineUserTurnCap)
	var files []string
	for i := 0; i < n; i++ {
		var lines []string
		turns := 1
		text := body
		if partsPerSession > 1 {
			turns = turnsPerPart * partsPerSession
			text = full
		}
		for turn := 0; turn < turns; turn++ {
			lines = append(lines, fmt.Sprintf(
				`{"type":"user","cwd":"/home/u/proj%d","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":%q}}`,
				i, text))
		}
		path := filepath.Join(dir, fmt.Sprintf("session-%d.jsonl", i))
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	return files
}

// mineFlags builds a cli.Command carrying the flags mineFiles reads.
func mineFlags(t *testing.T) *cli.Command {
	t.Helper()
	cmd := mineClaudeCommand()
	// Parse an empty argument list so every flag holds its declared default; the
	// command's own definition is the source of those, not a copy here.
	if err := cmd.Set("wing", "wing_alpha"); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// TestMineContinuesPastAFailedPart pins the consistency this loop already
// half-implemented.
//
// ⚠ ONE FAILED PART ABORTED THE WHOLE RUN, while an unreadable session twenty
// lines up was reported and skipped — the same decision taken differently for the
// other failure class, with nothing arguing for the difference. Reported
// 2026-08-31: a 249-session seed on a CPU-only host died on the first part of the
// first session, because one embed batch exceeded the server's budget, and filed
// NOTHING. Aborting was never the safe choice either: the command is idempotent
// by source id, so the partial result was always keepable and re-running was
// always the recovery.
func TestMineContinuesPastAFailedPart(t *testing.T) {
	files := mineFixtureDir(t, 4, 1)
	client := &stubMineClient{failAt: 2}
	var out bytes.Buffer

	err := mineFiles(context.Background(), mineFlags(t), &out, client, files)

	if err == nil {
		t.Error("a run with a failed part exited 0 — an operator reading only the status " +
			"cannot tell a complete seed from a partial one")
	}
	if len(client.seen) != len(files) {
		t.Errorf("the run attempted %d of %d sessions; a failure must not end the walk:\n%s",
			len(client.seen), len(files), out.String())
	}
	report := out.String()
	if !strings.Contains(report, "FAILED") {
		t.Errorf("the failed part is not reported:\n%s", report)
	}
	if !strings.Contains(report, "EMBED_TIMEOUT") {
		t.Errorf("a context-deadline failure does not name the knob that fixes it — the error "+
			"the operator sees says `context deadline exceeded` and nothing about the remedy:\n%s",
			report)
	}
	// The sessions after the failure must be FILED, not merely attempted.
	if !strings.Contains(report, "mined") {
		t.Errorf("nothing was reported as mined, so the run may have continued and filed "+
			"nothing:\n%s", report)
	}
}

// TestMineReportsNoFailuresWhenEverythingLands is the falsifiability half: a
// counter that is hardcoded, or an error returned unconditionally, fails here.
func TestMineReportsNoFailuresWhenEverythingLands(t *testing.T) {
	files := mineFixtureDir(t, 3, 1)
	client := &stubMineClient{}
	var out bytes.Buffer

	if err := mineFiles(context.Background(), mineFlags(t), &out, client, files); err != nil {
		t.Errorf("a clean run returned an error: %v\n%s", err, out.String())
	}
	report := out.String()
	if strings.Contains(report, "FAILED") {
		t.Errorf("a clean run reported a failure:\n%s", report)
	}
	if len(client.seen) != len(files) {
		t.Errorf("filed %d of %d sessions:\n%s", len(client.seen), len(files), report)
	}
}

// TestASessionWhosePartsPartlyLandIsNotCountedMined covers the branch a one-part
// fixture cannot reach.
//
// ⚠ REPORTED BY REVIEW: every mine fixture rendered ONE part per session, so the
// partFailed path — where some parts of a session file and others do not — was
// written and never executed by a test. It is the branch that decides whether a
// half-filed session is reported as mined, and reporting it as mined is how a
// partial corpus reads as a complete one.
func TestASessionWhosePartsPartlyLandIsNotCountedMined(t *testing.T) {
	files := mineFixtureDir(t, 1, 3)
	client := &stubMineClient{failAt: 2} // the middle part of the only session
	var out bytes.Buffer

	err := mineFiles(context.Background(), mineFlags(t), &out, client, files)
	if err == nil {
		t.Error("a session with a failed part exited 0")
	}
	report := out.String()
	if len(client.seen) < 3 {
		t.Fatalf("the run attempted %d part(s); a failed part must not end the SESSION either:\n%s",
			len(client.seen), report)
	}
	if strings.Contains(report, "  mined ") {
		t.Errorf("a session whose parts only partly landed was reported as mined — a partial "+
			"corpus then reads as a complete one:\n%s", report)
	}
	if !strings.Contains(report, "skipped 1") {
		t.Errorf("the half-filed session was not counted as skipped:\n%s", report)
	}
}
