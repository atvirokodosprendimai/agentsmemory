package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/urfave/cli/v3"
)

// eventmap answers a question no other command in this kit asks: what is the
// SHAPE of the hook system as installed, rather than as designed?
//
// It exists because every gate in this repository checks the installer's PLAN.
// `TestEveryInjectingHookIsOnAnInjectingEvent` reads the manifest; the hook
// channel tests read the scripts; `doctor` runs what it can parse. None of them
// can answer "is anything registered twice", "does anything write a state file
// nothing reads", or "is there a registration this kit cannot even parse" —
// and the answer to all three was yes on the machine this was written on
// (issue #416), over a `doctor` run that exited 0.
//
// ⚠ IT IS READ-ONLY, AND THAT IS WHAT LETS IT PARSE TOLERANTLY. The installer's
// own parser is deliberately strict, because a command it can parse is a command
// it may DROP or reproduce the environment of, and mis-parsing either is worse
// than not recognising the entry at all. A mapper decides nothing and writes
// nothing, so it can afford to read shapes the installer must refuse — which is
// precisely how it sees the registrations the installer is blind to.

// hookScript is one shipped script: what it declares about itself, and what a
// read of its body says it touches.
type hookScript struct {
	Name       string   `json:"name"`
	Lines      int      `json:"lines"`
	Channel    string   `json:"channel"`
	ChannelWhy string   `json:"channel_why,omitempty"`
	Events     []string `json:"events"`
	PalaceMCP  []string `json:"palace_calls,omitempty"`
	StateWrite []string `json:"state_writes,omitempty"`
	StateRead  []string `json:"state_reads,omitempty"`
	// ExternalConsumers maps a state family to the written reason its reader
	// lives outside this kit.
	ExternalConsumers map[string]string `json:"external_consumers,omitempty"`
}

// registration is one entry found in an agent's real settings file.
type registration struct {
	Event  string `json:"event"`
	Script string `json:"script"` // basename, "" when the command could not be parsed
	Env    string `json:"env"`    // the assignment prefix, verbatim
	Raw    string `json:"raw"`
	Parsed bool   `json:"parsed_by_installer"`
}

// finding is one thing wrong with the shape.
type finding struct {
	Class  string `json:"class"` // duplicate | unparseable | orphan-state | unreachable-channel
	Detail string `json:"detail"`
}

type eventMap struct {
	KitDir        string         `json:"kit_dir"`
	SettingsPath  string         `json:"settings_path"`
	Scripts       []hookScript   `json:"scripts"`
	Registrations []registration `json:"registrations"`
	Findings      []finding      `json:"findings"`
}

// stateRef matches a path under the state directory, which is how every hook in
// this kit hands something to another hook. Deliberately loose: the point is to
// find the file names, and a reference this misses is a dependency the map does
// not draw.
var stateRef = regexp.MustCompile(`agentsmemory-(?:touched|precompact|last-turn|reground|status)\b`)

// stateConsumerDecl is the `# state-consumer: <family> <reason>` line a script
// writes when the thing that reads its state file lives OUTSIDE this kit.
//
// ⚠ IT EXISTS SO THE ORPHAN CHECK CAN PASS ON A HEALTHY INSTALL. The re-ground
// marker is written by the recall hook and read by a persistent Monitor the
// session arms (ADR-062 T3) — correct by design, and indistinguishable from a
// genuine orphan by reading the kit alone. Without a declaration this command
// reports a finding on every correct install and can therefore gate nothing,
// which is how #393 shipped: `doctor` exited 1 on a freshly installed v0.0.125
// and an operator found it, not the suite.
//
// The declaration is the same shape as `# hook-output:` and carries the same
// obligation: a reason, checked, so the escape hatch cannot become the dodge.
var stateConsumerDecl = regexp.MustCompile(`(?m)^# state-consumer:[ \t]*([a-z0-9-]+)[ \t]+(.+)$`)

// palaceCall matches an `am_*` tool name in a script body.
var palaceCall = regexp.MustCompile(`\bam_[a-z_]+`)

// tolerantHookPath extracts the script a registration runs, accepting the
// assignment shapes the installer's own parser refuses.
//
// ⚠ THE SHAPES IT ACCEPTS AND `splitLeadingAssignment` DOES NOT ARE THE WHOLE
// REASON THIS EXISTS. That parser takes single-quoted values only, so a command
// carrying `VAR="$(some command)"` — which is what a hand-written per-project
// wing resolution looks like — returns ok=false, and every identity check in the
// kit then treats the entry as a stranger's: `foreignHookPredicate` spares it,
// `hookPresent` does not find it, a second entry is appended beside it, and
// `registeredHookEvents` skips it so `doctor` reports a clean file. Measured on
// one machine 2026-09-07, issue #416: eleven such registrations, every hook
// doubled, `doctor` exit 0.
//
// It returns the environment prefix verbatim rather than parsed, because a
// mapper must never imply it could REPRODUCE that environment. `$(…)` is
// evaluated by the shell at hook time and by nothing here.
func tolerantHookPath(cmd string) (script, env string, ok bool) {
	rest := cmd
	for {
		trimmed := strings.TrimSpace(rest)
		eq := strings.IndexByte(trimmed, '=')
		if eq <= 0 || strings.IndexByte(trimmed[:eq], ' ') != -1 {
			break
		}
		name := trimmed[:eq]
		valid := true
		for i := 0; i < len(name); i++ {
			c := name[i]
			if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (i > 0 && c >= '0' && c <= '9')) {
				valid = false
				break
			}
		}
		if !valid {
			break
		}
		body := trimmed[eq+1:]
		var consumed int
		switch {
		case strings.HasPrefix(body, "'"):
			// Single-quoted: the installer's own shape. A `'"'"'` sequence is an
			// embedded quote and does not close the value.
			i := 1
			for i < len(body) {
				if body[i] == '\'' {
					if strings.HasPrefix(body[i:], `'"'"'`) {
						i += 5
						continue
					}
					break
				}
				i++
			}
			if i >= len(body) {
				return "", "", false
			}
			consumed = i + 1
		case strings.HasPrefix(body, `"`):
			// Double-quoted, which may hold a command substitution. Nested quotes
			// inside `$( … )` do not close it, so track the substitution depth.
			i, depth := 1, 0
			for i < len(body) {
				if body[i] == '\\' {
					i += 2
					continue
				}
				if strings.HasPrefix(body[i:], "$(") {
					depth++
					i += 2
					continue
				}
				if body[i] == ')' && depth > 0 {
					depth--
					i++
					continue
				}
				if body[i] == '"' && depth == 0 {
					break
				}
				i++
			}
			if i >= len(body) {
				return "", "", false
			}
			consumed = i + 1
		default:
			// Unquoted: ends at the first space.
			sp := strings.IndexByte(body, ' ')
			if sp < 0 {
				return "", "", false
			}
			consumed = sp
		}
		env = strings.TrimSpace(env + " " + trimmed[:eq+1+consumed])
		rest = body[consumed:]
	}

	rest = strings.TrimSpace(rest)
	const quoted = "bash -- "
	if strings.HasPrefix(rest, quoted) {
		enc := strings.TrimPrefix(rest, quoted)
		if len(enc) >= 2 && enc[0] == '\'' && enc[len(enc)-1] == '\'' {
			return strings.ReplaceAll(enc[1:len(enc)-1], `'"'"'`, "'"), env, true
		}
		return "", env, false
	}
	if f := strings.Fields(rest); len(f) == 2 && f[0] == "bash" {
		return f[1], env, true
	}
	return "", env, false
}

// scanKit reads every shipped script and the manifest that registers them.
func scanKit(kitDir string) ([]hookScript, error) {
	paths, err := filepath.Glob(filepath.Join(kitDir, "*.sh"))
	if err != nil {
		return nil, err
	}
	events := map[string][]string{}
	if raw, err := os.ReadFile(filepath.Join(kitDir, "hooks.json")); err == nil {
		var doc struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(raw, &doc); err == nil {
			for event, groups := range doc.Hooks {
				for _, g := range groups {
					for _, h := range g.Hooks {
						events[filepath.Base(h.Command)] = append(events[filepath.Base(h.Command)], event)
					}
				}
			}
		}
	}

	var out []hookScript
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		s := hookScript{
			Name:   filepath.Base(p),
			Lines:  strings.Count(string(body), "\n"),
			Events: events[filepath.Base(p)],
		}
		if m := hookOutputDecl.FindSubmatch(body); m != nil {
			s.Channel = string(m[1])
			s.ChannelWhy = strings.TrimSpace(strings.TrimLeft(string(m[2]), "— "))
		}
		s.PalaceMCP = uniqueMatches(palaceCall, body)
		s.StateWrite, s.StateRead = stateDirection(string(body))
		for _, m := range stateConsumerDecl.FindAllSubmatch(body, -1) {
			if s.ExternalConsumers == nil {
				s.ExternalConsumers = map[string]string{}
			}
			s.ExternalConsumers[string(m[1])] = strings.TrimSpace(string(m[2]))
		}
		sort.Strings(s.Events)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// stateDirection decides, for one script, which state families it writes and
// which it reads.
//
// ⚠ THE PATH IS ALMOST NEVER ON THE SAME LINE AS THE REDIRECTION, AND THAT IS
// THE WHOLE DIFFICULTY. Every hook here binds the path to a variable first
// (`REGROUND_DIR="$STATE/agentsmemory-reground"`) and redirects into it many
// lines later. A first version judged each line that mentioned the family name
// and got three of six rows backwards — it called the re-ground marker's write a
// read, which suppressed the single orphan-state finding this command was
// written to produce. A map that names the wrong direction is worse than one
// that names none, because the dependency arrows are the map.
//
// So: bind variables to families first, then classify by how each variable is
// USED. A redirection into it is a write; anything else is a read.
func stateDirection(body string) (writes, reads []string) {
	varFamily := map[string]string{}
	assign := regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)=.*?(agentsmemory-(?:touched|precompact|last-turn|reground|status))`)
	for _, m := range assign.FindAllStringSubmatch(body, -1) {
		varFamily[m[1]] = m[2]
	}

	w, r := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // a comment describes the mechanism; it does not perform it
		}
		for name, family := range varFamily {
			// The assignment itself is neither a read nor a write of the file.
			if strings.HasPrefix(trimmed, name+"=") {
				continue
			}
			ref := "$" + name
			if !strings.Contains(trimmed, ref) {
				continue
			}
			if redirectsInto(trimmed, name) || strings.Contains(trimmed, "mkdir -p \""+ref) {
				w[family] = true
			} else {
				r[family] = true
			}
		}
		// ⚠ NO LITERAL-PATH RULE. A first version also called a line a write when it
		// mentioned a family literally AND contained ">" anywhere — and every such
		// line in this kit is an ASSIGNMENT carrying `2>/dev/null`, so the status
		// cache's reader was reported as its writer. Every hook here binds the path
		// to a variable first, so the variable pass above covers them all; a script
		// that redirected straight into a literal path would be missed, and that is
		// the deliberate trade — a missed edge over a reversed one.
	}
	// A family both written and read by one script is reported as a write only:
	// the interesting relation is who ELSE reads it, and a script re-reading its
	// own file tells a reader nothing about the dependency graph.
	for f := range w {
		delete(r, f)
	}
	return sortedFamilies(w), sortedFamilies(r)
}

// redirectsInto reports whether a line sends output into $name.
func redirectsInto(line, name string) bool {
	for _, form := range []string{`> "$` + name, `>"$` + name, `> $` + name, `>> "$` + name, `>>"$` + name} {
		if strings.Contains(line, form) {
			return true
		}
	}
	return false
}

func sortedFamilies(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func uniqueMatches(re *regexp.Regexp, body []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAll(body, -1) {
		s := string(m)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// scanSettings reads an agent's real settings file into a flat registration list.
func scanSettings(path string) ([]registration, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	events := make([]string, 0, len(doc.Hooks))
	for e := range doc.Hooks {
		events = append(events, e)
	}
	sort.Strings(events)

	var out []registration
	for _, event := range events {
		for _, g := range doc.Hooks[event] {
			for _, h := range g.Hooks {
				script, env, ok := tolerantHookPath(h.Command)
				if !ok || !strings.Contains(filepath.Base(script), "agentsmemory") {
					continue
				}
				_, installerOK := installerHookPath(h.Command)
				out = append(out, registration{
					Event:  event,
					Script: filepath.Base(script),
					Env:    env,
					Raw:    h.Command,
					Parsed: installerOK,
				})
			}
		}
	}
	return out, nil
}

// judge derives the findings from the two scans.
//
// The classes are the ones §Reachability keeps producing, expressed as questions
// about shape rather than about behaviour: is one thing registered as two, is a
// registration invisible to the tool that maintains it, does a hook hand
// something to nobody, does a hook that means to speak speak into a log.
func judge(m *eventMap) []finding {
	var out []finding

	// One script on one event, registered more than once, is one hook running
	// twice — whatever the environment prefixes say. Identity is the pair, and
	// the prefix is that hook's configuration.
	type ident struct{ event, script string }
	byIdent := map[ident][]registration{}
	for _, r := range m.Registrations {
		byIdent[ident{r.Event, r.Script}] = append(byIdent[ident{r.Event, r.Script}], r)
	}
	idents := make([]ident, 0, len(byIdent))
	for k := range byIdent {
		idents = append(idents, k)
	}
	sort.Slice(idents, func(i, j int) bool {
		if idents[i].event != idents[j].event {
			return idents[i].event < idents[j].event
		}
		return idents[i].script < idents[j].script
	})
	for _, id := range idents {
		regs := byIdent[id]
		if len(regs) < 2 {
			continue
		}
		envs := make([]string, 0, len(regs))
		for _, r := range regs {
			envs = append(envs, r.Env)
		}
		out = append(out, finding{
			Class: "duplicate",
			Detail: fmt.Sprintf("%s on %s is registered %d times; it runs %d times per trigger. "+
				"The entries differ only in configuration: %s",
				id.script, id.event, len(regs), len(regs), strings.Join(envs, "  ||  ")),
		})
	}

	// A registration this kit cannot parse is one it cannot clean, report, or
	// run with the right environment — and it is silent about all three.
	for _, r := range m.Registrations {
		if r.Parsed {
			continue
		}
		out = append(out, finding{
			Class: "unparseable",
			Detail: fmt.Sprintf("%s on %s carries an assignment installerHookPath cannot read, so "+
				"the installer treats it as foreign and doctor skips it: %s",
				r.Script, r.Event, r.Env),
		})
	}

	// A state file with a writer and no reader is a handover to nobody.
	writers, readers := map[string][]string{}, map[string][]string{}
	for _, s := range m.Scripts {
		for _, w := range s.StateWrite {
			writers[w] = append(writers[w], s.Name)
		}
		for _, r := range s.StateRead {
			readers[r] = append(readers[r], s.Name)
		}
	}
	keys := make([]string, 0, len(writers))
	for k := range writers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	declared := map[string]string{}
	for _, s := range m.Scripts {
		for family, why := range s.ExternalConsumers {
			declared[family] = why
		}
	}
	for _, k := range keys {
		if len(readers[k]) > 0 {
			continue
		}
		// A declared external consumer is the answer, not an exemption: the
		// reason is what a reader judges, exactly as `# hook-output:` works.
		if why, ok := declared[k]; ok {
			if strings.TrimSpace(why) != "" {
				continue
			}
			out = append(out, finding{
				Class: "orphan-state",
				Detail: fmt.Sprintf("%s declares an external consumer with no reason; the "+
					"declaration is the escape hatch and a reason is what stops it becoming the dodge", k),
			})
			continue
		}
		out = append(out, finding{
			Class: "orphan-state",
			Detail: fmt.Sprintf("%s is written by %s and read by no hook in this kit, and no script "+
				"declares `# state-consumer: %s <reason>` — so either the consumer was never written, "+
				"or it exists outside the kit and nobody said so",
				k, strings.Join(writers[k], ", "), k),
		})
	}

	// A hook that declares it speaks, registered where stdout is discarded.
	for _, s := range m.Scripts {
		if s.Channel != channelStdoutInjected {
			continue
		}
		for _, e := range s.Events {
			if hookEventChannel(e) != channelInjected {
				out = append(out, finding{
					Class:  "unreachable-channel",
					Detail: fmt.Sprintf("%s declares %s but is registered on %s, whose stdout does not reach the model", s.Name, s.Channel, e),
				})
			}
		}
	}
	return out
}

// stateKey reduces a matched reference to the file family it names, so a write
// spelled with `$SESSION` and a read spelled with `${SID}` land on one row.
func stateKey(ref string) string {
	if i := strings.IndexAny(ref, "/$"); i > 0 {
		return ref[:i]
	}
	return ref
}

func renderEventMap(out io.Writer, m *eventMap) {
	fmt.Fprintf(out, "kit:      %s\n", m.KitDir)
	fmt.Fprintf(out, "settings: %s\n\n", m.SettingsPath)

	fmt.Fprintln(out, "SCRIPTS")
	for _, s := range m.Scripts {
		ev := strings.Join(s.Events, ",")
		if ev == "" {
			ev = "(no event)"
		}
		fmt.Fprintf(out, "  %-40s %-24s %-16s %4d lines\n", s.Name, ev, s.Channel, s.Lines)
		if len(s.PalaceMCP) > 0 {
			fmt.Fprintf(out, "      palace: %s\n", strings.Join(s.PalaceMCP, " "))
		}
		if len(s.StateWrite) > 0 {
			fmt.Fprintf(out, "      writes: %s\n", strings.Join(s.StateWrite, " "))
		}
		if len(s.StateRead) > 0 {
			fmt.Fprintf(out, "      reads:  %s\n", strings.Join(s.StateRead, " "))
		}
	}

	fmt.Fprintf(out, "\nREGISTRATIONS (%d found in the real settings file)\n", len(m.Registrations))
	for _, r := range m.Registrations {
		mark := " "
		if !r.Parsed {
			mark = "!"
		}
		fmt.Fprintf(out, " %s %-24s %-40s %s\n", mark, r.Event, r.Script, r.Env)
	}
	if len(m.Registrations) > 0 {
		fmt.Fprintln(out, "   (! = this kit's own parser cannot read the command)")
	}

	fmt.Fprintf(out, "\nFINDINGS (%d)\n", len(m.Findings))
	if len(m.Findings) == 0 {
		fmt.Fprintln(out, "  none")
		return
	}
	for _, f := range m.Findings {
		fmt.Fprintf(out, "  [%s] %s\n", f.Class, f.Detail)
	}
}

// attachRegisteredEvents fills in each script's events from the real settings
// when the kit directory carries no manifest, which is every installed kit:
// hooks.json ships in the repository and is not copied to the config directory.
func attachRegisteredEvents(scripts []hookScript, regs []registration) {
	byScript := map[string]map[string]bool{}
	for _, r := range regs {
		if byScript[r.Script] == nil {
			byScript[r.Script] = map[string]bool{}
		}
		byScript[r.Script][r.Event] = true
	}
	for i := range scripts {
		if len(scripts[i].Events) > 0 {
			continue
		}
		for e := range byScript[scripts[i].Name] {
			scripts[i].Events = append(scripts[i].Events, e)
		}
		sort.Strings(scripts[i].Events)
	}
}

// eventMapCommand is the map: what fires, what it touches, and what is wrong
// with the shape.
func eventMapCommand() *cli.Command {
	return &cli.Command{
		Name:  "eventmap",
		Usage: "map every hook event, what each script touches, and what is registered on this machine",
		Description: "Reads the shipped hook scripts and their manifest, then the agent's REAL\n" +
			"settings file, and reports the shape: which script runs on which event, what\n" +
			"each writes and reads, and four classes of defect — a hook registered more\n" +
			"than once, a registration this kit's own parser cannot read, a state file\n" +
			"written and read by nobody, and a hook that means to speak registered where\n" +
			"stdout is discarded.\n\n" +
			"It changes nothing. Every other check in this repository reads the\n" +
			"installer's PLAN; this one reads what is actually on disk, which is where\n" +
			"issue #416 lived — eleven unparseable registrations, every hook doubled, and\n" +
			"`doctor` exiting 0 over it.\n\n" +
			"Exits non-zero when there is a finding, so it can gate.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "kit", Usage: "directory holding the hook scripts and hooks.json (default: the installed one)"},
			&cli.StringFlag{Name: "settings", Usage: "the agent's settings file (default: the installed agent's)"},
			&cli.StringFlag{Name: "agent", Value: "claude", Usage: "which agent's install to read"},
			&cli.BoolFlag{Name: "json", Usage: "emit the map as JSON instead of a report"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			out := io.Writer(os.Stdout)
			if w := c.Root().Writer; w != nil {
				out = w
			}
			// The installed directory, resolved exactly the way doctor resolves it,
			// so the two commands never disagree about which install they read.
			dir := ""
			if c.String("kit") == "" || c.String("settings") == "" {
				kits, err := resolveAgentKits(c.String("agent"))
				if err != nil {
					return err
				}
				if len(kits) != 1 {
					return fmt.Errorf("--agent %q names %d agents; map one at a time so a finding "+
						"names the install it belongs to", c.String("agent"), len(kits))
				}
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("resolve home: %w", err)
				}
				d, _, _, err := resolveInstallTarget(kits[0], true, false, "", "", home)
				if err != nil {
					return err
				}
				dir = d
			}
			kit := c.String("kit")
			if kit == "" {
				kit = dir
			}
			settings := c.String("settings")
			if settings == "" {
				settings = filepath.Join(dir, "settings.json")
			}

			scripts, err := scanKit(kit)
			if err != nil {
				return err
			}
			regs, err := scanSettings(settings)
			if err != nil {
				return err
			}
			// An INSTALLED kit ships no hooks.json — the manifest is a repository
			// artefact — so without this the report says "(no event)" for every
			// script on exactly the install it was written to examine.
			attachRegisteredEvents(scripts, regs)
			m := &eventMap{KitDir: kit, SettingsPath: settings, Scripts: scripts, Registrations: regs}
			m.Findings = judge(m)

			if c.Bool("json") {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(m); err != nil {
					return err
				}
			} else {
				renderEventMap(out, m)
			}
			if len(m.Findings) > 0 {
				return fmt.Errorf("%d finding(s) in the installed hook shape", len(m.Findings))
			}
			return nil
		},
	}
}
