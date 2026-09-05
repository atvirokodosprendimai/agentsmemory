package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// monitorScript is the bash fence in the `/am` command that a session arms as a
// persistent monitor. It is extracted rather than copied because copying is what
// this test exists to catch: the hook writes a marker into one directory and the
// shipped instruction tells the session to watch another, and nothing else in the
// tree compares them.
var monitorScript = regexp.MustCompile("(?s)```bash\n(.*?agentsmemory-reground.*?)```")

// TestACompactionWakesTheSessionThroughTheMonitor drives the real PreCompact
// hook, the real recall hook, and the real monitor script out of the shipped
// `/am` command, and fails unless the script emits a line naming the task.
//
// It exists because ADR-062 shipped the re-ground as an INSTRUCTION and recorded
// a trigger as impossible — "no hook can invoke a skill, on a timer or
// otherwise, and nothing outside a session can make it take a turn". The first
// clause is true; the conclusion was false, and it was written into the record,
// the hook's comment and this command at once. A persistent monitor's stdout
// line arrives as a notification and a notification makes the session take a
// turn, so the hook does not need to invoke anything: it leaves a marker whose
// APPEARANCE is the event.
//
// The path agreement is proved by running the two halves rather than by
// comparing two string literals, for the reason
// TestTheSocketPlaceholderIsAcceptedByTheGuard already records: equality between
// two things you typed pins nothing, because deleting one side of the coupling
// leaves such a check green while every real client breaks.
func TestACompactionWakesTheSessionThroughTheMonitor(t *testing.T) {
	state := t.TempDir()
	transcript := filepath.Join(state, "t.jsonl")
	line := `{"type":"user","message":{"role":"user","content":"the task that was interrupted"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"wakeprobe","transcript_path":"` + transcript + `","trigger":"auto"}`
	runHook(t, "agentsmemory-precompact-hook.sh", payload, state)
	runHook(t, "agentsmemory-recall-hook.sh", `{"session_id":"wakeprobe","source":"compact"}`, state)

	marker := filepath.Join(state, "agentsmemory-reground", "wakeprobe")
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the recall hook wrote no re-ground marker on a compaction: %v", err)
	}
	if got := strings.TrimSpace(string(body)); got != "the task that was interrupted" {
		t.Errorf("marker carries %q, not the task in flight", got)
	}

	// The shipped script, verbatim, against the marker the hook just wrote.
	doc, err := os.ReadFile(filepath.Join("commands", "am.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := monitorScript.FindSubmatch(doc)
	if m == nil {
		t.Fatal("the /am command carries no monitor script naming agentsmemory-reground; the instruction to arm the wake is gone or renamed")
	}
	cmd := testexec.Command(t, "bash", "-c", string(m[1]))
	cmd.Env = append(os.Environ(), "AGENTSMEMORY_STATE_DIR="+state)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The loop never exits on its own; testexec kills its process group when this
	// test ends, which is the whole reason children go through that package.
	lines := make(chan string, 1)
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			if strings.TrimSpace(s.Text()) != "" {
				lines <- s.Text()
				return
			}
		}
		close(lines)
	}()
	select {
	case got, ok := <-lines:
		if !ok {
			t.Fatal("the monitor script exited without emitting an event for a marker that exists")
		}
		if !strings.Contains(got, "/amm the task that was interrupted") {
			t.Errorf("the monitor woke the session without naming the task: %q", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the monitor script emitted nothing for a marker the hook wrote — the two halves name different directories, which is a trigger that cannot fire")
	}
}

// TestOnlyACompactionArmsTheWake holds the blast radius. A marker written on an
// ordinary start would wake every session into a re-ground it does not need, and
// the failure is silent in the expensive direction: the notification arrives and
// the session obeys it.
func TestOnlyACompactionArmsTheWake(t *testing.T) {
	for _, source := range []string{"startup", "resume", "clear"} {
		state := t.TempDir()
		runHook(t, "agentsmemory-recall-hook.sh", `{"session_id":"wakeprobe","source":"`+source+`"}`, state)
		if _, err := os.Stat(filepath.Join(state, "agentsmemory-reground", "wakeprobe")); err == nil {
			t.Errorf("source=%s wrote a re-ground marker; only a compaction should", source)
		}
	}
}
