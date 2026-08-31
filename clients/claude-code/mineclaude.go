// mineclaude.go implements `aiagentmemory mine-claude`: turn the Claude Code
// transcripts already sitting on this machine into palace corpus.
//
// The transcripts under ~/.claude/projects are months of real engineering
// conversation — decisions, dead ends, fixes — which is exactly what a memory
// palace is for and exactly what its evals are starved of. But mining them RAW
// would poison the palace: measured on a large session, ~90% of the bytes are
// tool traffic (commands, file dumps, results), and a memory system filled with
// tool noise recalls noise.
//
// So the miner is an extractor first: it keeps what the humans and the model
// SAID and drops everything the harness did. Each session becomes one document,
// filed via am_mine with a stable source id, so re-running replaces rather than
// duplicates — the whole command is idempotent.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/urfave/cli/v3"
)

// Extraction limits. The point of each: a turn cap keeps one pasted log from
// dominating a session's embedding; a document cap keeps one marathon session
// from becoming a thousand chunks; a session floor drops the "hi, wrong window"
// sessions that would only dilute recall.
const (
	mineUserTurnCap = 2000 // runes kept per user turn
	mineAsstTurnCap = 1500 // runes kept per assistant turn
	// mineDocCap stays under the server's own 100k-character bound on a mine
	// call, with headroom for the header — the cap was learned the honest way, by
	// the server rejecting a 120k part.
	mineDocCap       = 90000
	mineSessionFloor = 500 // sessions with less extracted text than this are skipped
)

// transcriptLine is the slice of a Claude Code transcript line the miner reads.
type transcriptLine struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	// IsCompactSummary marks the harness's own compaction of earlier
	// conversation: machine-written recap, not speech, and mostly duplicate.
	IsCompactSummary bool   `json:"isCompactSummary"`
	Cwd              string `json:"cwd"`
	GitBranch        string `json:"gitBranch"`
	Timestamp        string `json:"timestamp"`
	Message          struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// contentBlock is one entry of a structured message content array.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// sessionDoc is what one transcript file distils to.
type sessionDoc struct {
	Cwd       string
	Branch    string
	Started   string // first timestamp seen
	Turns     []string
	TurnChars int
	BadLines  int // lines that failed to parse and were skipped, not swallowed
}

// extractSession reads one transcript and keeps only the conversation.
//
// What is dropped, and why:
//   - sidechains: subagent traffic, not the user's conversation;
//   - meta lines and harness wrappers (<local-command…>, <system-reminder>,
//     <command-…>): the harness talking to itself;
//   - tool_use / tool_result / thinking blocks: the ~90% that is not speech;
//   - everything that is not a user or assistant line at all.
func extractSession(r io.Reader) sessionDoc {
	doc := sessionDoc{}
	// Line-based, not stream-decoded: json.Decoder stops at the first malformed
	// line, and a truncated session filed as if complete is corpus that lies. A
	// bad line is SKIPPED and counted; only the reader ending ends the file.
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		raw, err := br.ReadBytes('\n')
		if len(raw) == 0 && err != nil {
			break
		}
		var line transcriptLine
		if jerr := json.Unmarshal(raw, &line); jerr != nil {
			if len(strings.TrimSpace(string(raw))) > 0 {
				doc.BadLines++
			}
			if err != nil {
				break
			}
			continue
		}
		if line.IsSidechain || line.IsMeta || line.IsCompactSummary {
			continue
		}
		if line.Type != "user" && line.Type != "assistant" {
			continue
		}
		if doc.Cwd == "" && line.Cwd != "" {
			doc.Cwd, doc.Branch, doc.Started = line.Cwd, line.GitBranch, line.Timestamp
		}

		text := extractText(line.Message.Content)
		text = stripHarnessNoise(text)
		if strings.TrimSpace(text) == "" {
			continue
		}
		cap, label := mineAsstTurnCap, "A"
		if line.Type == "user" {
			cap, label = mineUserTurnCap, "U"
		}
		if r := []rune(text); len(r) > cap {
			text = string(r[:cap]) + "…"
		}
		turn := label + ": " + text
		doc.Turns = append(doc.Turns, turn)
		doc.TurnChars += len([]rune(turn))
	}
	return doc
}

// extractText pulls the spoken text out of a message content field, which is
// either a bare string or an array of typed blocks.
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		// Only "text" speaks. thinking is unpolished internal monologue and huge;
		// tool_use/tool_result are the noise this extractor exists to drop.
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// stripHarnessNoise removes the wrappers the harness injects into user turns —
// command caveats, system reminders, command transcripts. They read as the user
// speaking and are not.
func stripHarnessNoise(text string) string {
	for _, tag := range []string{"system-reminder", "local-command-caveat", "command-name", "command-message", "command-args", "local-command-stdout"} {
		open, close := "<"+tag+">", "</"+tag+">"
		for {
			i := strings.Index(text, open)
			if i < 0 {
				break
			}
			j := strings.Index(text[i:], close)
			if j < 0 {
				text = text[:i]
				break
			}
			text = text[:i] + text[i+j+len(close):]
		}
	}
	if strings.HasPrefix(strings.TrimSpace(text), "Caveat:") {
		return ""
	}
	return text
}

// render packs a session into mineable documents, splitting when a marathon
// session would otherwise become one enormous source.
func (d sessionDoc) render(project, sessionID string) []minePart {
	header := fmt.Sprintf("Claude Code session %s\nproject: %s\ndate: %s", sessionID, project, d.Started)
	if d.Branch != "" {
		header += "\nbranch: " + d.Branch
	}

	var parts []minePart
	var b strings.Builder
	b.WriteString(header)
	// Every part carries an explicit #pN, including the first: mixed naming meant
	// a session crossing the one-part boundary changed its own source id.
	//
	// KNOWN LIMIT: if an extraction-rule change ever makes a session render FEWER
	// parts than a previous run, the surplus #pN documents persist (the server
	// purges per exact source). Transcripts themselves only append, so this
	// arises only when the miner's rules change — re-mine into a fresh --room
	// after such a change.
	flush := func() {
		if b.Len() > len(header) {
			parts = append(parts, minePart{
				Source:  fmt.Sprintf("claude-session/%s/%s#p%d", project, sessionID, len(parts)+1),
				Content: b.String(),
			})
		}
		b.Reset()
		b.WriteString(header)
	}
	for _, t := range d.Turns {
		if b.Len()+len(t) > mineDocCap {
			flush()
		}
		b.WriteString("\n\n")
		b.WriteString(t)
	}
	flush()
	return parts
}

// minePart is one document headed for am_mine.
type minePart struct {
	Source  string
	Content string
}

// mineClaudeCommand builds the `mine-claude` subcommand.
func mineClaudeCommand() *cli.Command {
	return &cli.Command{
		Name:  "mine-claude",
		Usage: "mine this machine's Claude Code transcripts into the palace as per-project corpus",
		Description: "Walks ~/.claude/projects, extracts what was actually SAID in each session —\n" +
			"tool traffic, subagent sidechains and harness wrappers are dropped, which is\n" +
			"~90% of the bytes — and files one document per session via am_mine.\n\n" +
			"Idempotent: each session files under a stable source id, so re-running\n" +
			"replaces rather than duplicates. The wing comes from each session's own\n" +
			"working directory, resolved exactly as `load` resolves it, so sessions land\n" +
			"in the project they belong to.\n\n" +
			"A part that cannot be filed is reported and skipped, not fatal: the rest of\n" +
			"the run is kept and the command exits non-zero so a partial seed is visible.\n" +
			"Re-running is the recovery, and is safe for the same reason.\n\n" +
			"⚠ On a CPU-only host, seeding is bounded by the SERVER's embed budget, not\n" +
			"by this command. One batch of 64 chunks was measured at 121s with bge-m3 on\n" +
			"a 16GB laptop, so a server started with a low EMBED_TIMEOUT (or an older\n" +
			"build, where HTTP_TIMEOUT covered embedding at 30s) fails part after part\n" +
			"with `context deadline exceeded`. Raise it on the SERVER and restart it.\n\n" +
			"Start with --dry-run to see what would be filed, and --limit to go gradually:\n" +
			"  aiagentmemory mine-claude --dry-run\n" +
			"  aiagentmemory mine-claude --project acme --limit 20",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "dir", Value: filepath.Join(homeDir(), ".claude", "projects"), Usage: "transcripts root"},
			&cli.StringFlag{Name: "project", Usage: "only sessions whose project path contains this substring"},
			&cli.IntFlag{Name: "limit", Value: 50, Usage: "max sessions to mine this run, newest first (0 = all)"},
			&cli.StringFlag{Name: "wing", Usage: "file everything into this wing instead of resolving per session"},
			&cli.StringFlag{Name: "room", Value: "sessions", Usage: "room the mined sessions land in"},
			&cli.IntFlag{Name: "min-chars", Value: mineSessionFloor, Usage: "skip sessions with less extracted text than this"},
			&cli.BoolFlag{Name: "dry-run", Usage: "report what would be mined without writing anything"},
			// The default is the LOCAL server, deliberately diverging from the rest
			// of the CLI: this command uploads months of private conversation, and
			// "wherever the global default points" — the hosted SaaS — is the wrong
			// place for that to land because nobody typed a URL. Mining to a hosted
			// workspace is legitimate, but only as an explicit choice.
			&cli.StringFlag{Name: "mcp-url", Sources: cli.EnvVars(mcpURLEnvVar), Value: localMCPURL, Usage: "agentsmemory MCP endpoint (defaults to the local server — this uploads private transcripts, so the hosted service must be asked for by name)"},
			&cli.StringFlag{Name: "token", Sources: cli.EnvVars(tokenEnvVar), Usage: "workspace token (a --local server needs none)"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runMineClaude(ctx, c, os.Stdout)
		},
	}
}

// runMineClaude is the whole flow: find sessions, extract, file.
func runMineClaude(ctx context.Context, c *cli.Command, out io.Writer) error {
	root := c.String("dir")
	pattern := c.String("project")
	files, err := findTranscripts(root, pattern)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintf(out, "no transcripts under %s", root)
		if pattern != "" {
			fmt.Fprintf(out, " matching %q", pattern)
		}
		fmt.Fprintln(out)
		return nil
	}
	if limit := c.Int("limit"); limit > 0 && len(files) > limit {
		files = files[:limit]
	}

	var client mineClient
	if !c.Bool("dry-run") {
		mcpClient, err := dialMCP(ctx, c.String("mcp-url"), c.String("token"), 120*time.Second)
		if err != nil {
			return err
		}
		defer mcpClient.Close()
		client = mcpClient
	}
	return mineFiles(ctx, c, out, client, files)
}

// mineFiles walks the chosen transcripts and files each one, and is the half a
// test can drive: it takes the client rather than dialling one, which is what
// mineClient was declared for.
func mineFiles(ctx context.Context, c *cli.Command, out io.Writer, client mineClient, files []string) error {
	mined, skipped, parts, failed := 0, 0, 0, 0
	byWing := map[string]int{}
	for _, f := range files {
		doc, project, sessionID, err := loadSession(f)
		if err != nil {
			fmt.Fprintf(out, "  skip %s: %v\n", filepath.Base(f), err)
			skipped++
			continue
		}
		if doc.TurnChars < c.Int("min-chars") {
			skipped++
			continue
		}
		wing := c.String("wing")
		if wing == "" {
			wing = wingForSession(doc.Cwd, project)
		}
		docs := doc.render(project, sessionID)
		if c.Bool("dry-run") {
			fmt.Fprintf(out, "  would mine %-46s → %-24s %3d turn(s), %5.1fKB in %d part(s)\n",
				project+"/"+shortSessionID(sessionID), wing, len(doc.Turns), float64(doc.TurnChars)/1000, len(docs))
		} else {
			// ⚠ A FAILED PART IS REPORTED AND SKIPPED, NOT FATAL TO THE RUN. This
			// loop already makes that call twenty lines up, where an unreadable
			// session is skipped rather than ending the walk — the abort was the
			// same decision taken differently for the other failure class, and
			// nothing argued for the difference. It cost the whole run: reported
			// 2026-08-31, a 249-session seed on a CPU-only host died on the first
			// part of the first session because one embed batch exceeded the
			// server's budget, and filed nothing.
			//
			// Keeping the partial result was already safe, because this command is
			// idempotent by source id — re-running replaces rather than duplicates,
			// which is what its own Description promises — so re-running IS the
			// recovery and aborting bought nothing.
			partFailed := 0
			for _, part := range docs {
				if err := mineOne(ctx, client, wing, c.String("room"), part); err != nil {
					fmt.Fprintf(out, "  FAILED %s: %v\n", part.Source, mineFailureHint(err))
					failed++
					partFailed++
					continue
				}
			}
			if partFailed > 0 {
				// Counted as skipped rather than mined: a session whose parts did not
				// all land is not a session that was filed, and reporting it as mined
				// is how a partial corpus reads as a complete one.
				skipped++
				continue
			}
			note := ""
			if doc.BadLines > 0 {
				note = fmt.Sprintf("  (%d unparseable line(s) skipped)", doc.BadLines)
			}
			fmt.Fprintf(out, "  mined %-46s → %-24s %3d turn(s), %d part(s)%s\n",
				project+"/"+shortSessionID(sessionID), wing, len(doc.Turns), len(docs), note)
		}
		mined++
		parts += len(docs)
		byWing[wing]++
	}

	verb := "mined"
	if c.Bool("dry-run") {
		verb = "would mine"
	}
	fmt.Fprintf(out, "\n%s %d session(s) (%d part(s)), skipped %d\n", verb, mined, parts, skipped)
	if failed > 0 {
		fmt.Fprintf(out, "  %d part(s) FAILED and were not filed\n", failed)
	}
	wings := make([]string, 0, len(byWing))
	for w := range byWing {
		wings = append(wings, w)
	}
	sort.Strings(wings)
	for _, w := range wings {
		fmt.Fprintf(out, "  %-26s %d session(s)\n", w, byWing[w])
	}
	if c.Bool("dry-run") {
		fmt.Fprintf(out, "run again without --dry-run to file these\n")
	}
	// A partial run must not exit 0. The parts that landed are kept and the
	// summary above says which did not, but a script or an operator reading only
	// the status has to be able to tell a complete seed from a partial one.
	if failed > 0 {
		return fmt.Errorf("%d part(s) could not be filed; the rest were kept, and re-running "+
			"is safe — this command replaces by source id rather than duplicating", failed)
	}
	return nil
}

// mineFailureHint annotates the one failure a first-time operator actually hits
// with the knob that fixes it.
//
// ⚠ THE ERROR NAMED A SYMPTOM AND NOT A REMEDY. `context deadline exceeded` on
// /api/embed is the server's embed budget, not a hang and not a broken Ollama,
// and nothing in the message said so — measured 2026-08-31, a CPU-only host took
// 121s for one batch of 64 against a 30s default. The budget now has its own knob
// (EMBED_TIMEOUT, default 5m), so this points at it for anyone running a server
// older than that change or a value too low for their hardware.
func mineFailureHint(err error) error {
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		return err
	}
	return fmt.Errorf("%w\n      (the SERVER's embed budget, not a hang: raise EMBED_TIMEOUT — "+
		"CPU-only embedding routinely needs minutes for one batch)", err)
}

// findTranscripts lists session files newest-first, so a --limit takes the
// sessions most likely to still matter.
func findTranscripts(root, pattern string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	type dated struct {
		path string
		mod  time.Time
	}
	var files []dated
	for _, m := range matches {
		if pattern != "" && !strings.Contains(m, pattern) {
			continue
		}
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		files = append(files, dated{m, info.ModTime()})
	}
	sort.Slice(files, func(a, b int) bool { return files[a].mod.After(files[b].mod) })
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out, nil
}

// loadSession extracts one transcript file.
func loadSession(path string) (sessionDoc, string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return sessionDoc{}, "", "", err
	}
	defer f.Close()
	doc := extractSession(f)
	project := filepath.Base(filepath.Dir(path))
	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if len(doc.Turns) == 0 {
		return doc, project, sessionID, fmt.Errorf("no conversation extracted")
	}
	return doc, project, sessionID, nil
}

// shortSessionID abbreviates a session id for the progress line. The id comes
// from a FILENAME under the transcripts root, not from Claude Code, so it is not
// guaranteed to be a UUID — a stray short *.jsonl there would otherwise panic a
// whole mining run on a cosmetic slice.
func shortSessionID(id string) string {
	if r := []rune(id); len(r) > 8 {
		return string(r[:8])
	}
	return id
}

// wingForSession resolves where a session's memories belong, most authoritative
// source first:
//
//  1. the wing this project's MCP REGISTRATION carries (~/.claude.json,
//     X-Agentsmemory-Wing) — the map the user actually chose, which is not
//     derivable: their infrastructure repo files into the product's wing, and no
//     directory name says so;
//  2. the project-config ladder `load` climbs ($AGENTSMEMORY_WING, .aiagentmemory);
//  3. a wing derived from the directory name — an old project whose directory is
//     gone still deserves its own wing rather than a shared pile.
func wingForSession(cwd, project string) string {
	if cwd != "" {
		if w := registeredWingFor(cwd); w != "" {
			return w
		}
		// The project-config files, but NOT $AGENTSMEMORY_WING: a process-wide
		// variable meant for one launched session would file EVERY project's
		// history into a single wing — the exact mixing the miner exists to avoid.
		shared, local, _ := findProjectConfig(cwd)
		if w := firstNonEmpty(local.wing, shared.wing); w != "" {
			return w
		}
		// The protocol's git-remote rung: the remote basename names the project
		// more stably than whatever the directory happens to be called.
		if out, err := exec.Command("git", "-C", cwd, "remote", "get-url", "origin").Output(); err == nil {
			if base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(string(out))), ".git"); base != "" && base != "." {
				return palace.DeriveWingName(base)
			}
		}
		return palace.DeriveWingName(filepath.Base(cwd))
	}
	return palace.DeriveWingName(strings.TrimPrefix(project, "-"))
}

// registeredWings caches the project→wing map read from the Claude config, keyed
// by project directory. Loaded once per run; the file is small and ours to read.
var registeredWings map[string]string

// registeredWingFor returns the wing the nearest enclosing registered project
// carries, walking upward so a session started in a subdirectory still lands in
// its project's wing.
func registeredWingFor(cwd string) string {
	if registeredWings == nil {
		registeredWings = loadRegisteredWings(filepath.Join(homeDir(), ".claude.json"))
	}
	for d := cwd; ; {
		if w, ok := registeredWings[d]; ok {
			return w
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// loadRegisteredWings extracts every project's X-Agentsmemory-Wing header from a
// Claude config file. Any read or shape problem yields an empty map — the miner
// then falls back down the ladder rather than failing a corpus run over config
// parsing.
func loadRegisteredWings(path string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var cfg struct {
		Projects map[string]struct {
			McpServers map[string]struct {
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return out
	}
	for dir, p := range cfg.Projects {
		if srv, ok := p.McpServers["agentsmemory"]; ok {
			if w := strings.TrimSpace(srv.Headers[mcpprotocol.WingHeader]); w != "" {
				out[dir] = w
			}
		}
	}
	return out
}

// mineClient is the one MCP call this command makes, as an interface so the flow
// tests without a server.
type mineClient interface {
	CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// mineOne files one document via am_mine.
func mineOne(ctx context.Context, client mineClient, wing, room string, part minePart) error {
	result, err := mcpcli.Call(ctx, client.CallTool, "mine", map[string]any{
		"wing":    wing,
		"room":    room,
		"source":  part.Source,
		"content": part.Content,
	})
	if err != nil {
		return err
	}
	return mcpcli.Failed("mine", result)
}
