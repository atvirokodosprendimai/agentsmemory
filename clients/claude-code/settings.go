package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	codexStopHookStart = "# >>> agentsmemory managed Stop hook >>>"
	codexStopHookEnd   = "# <<< agentsmemory managed Stop hook <<<"
)

// ensureHook registers hookCmd for a Claude Code hook EVENT ("Stop",
// "SessionStart", …) in the settings
// JSON at path, idempotently. It preserves any existing settings, backs the file
// up (timestamped) before writing, and never adds a duplicate entry for the same
// command. It returns true if it changed the file.
//
// isObsolete, when non-nil, marks commands this install supersedes: any Stop
// entry running one is dropped in the same read-modify-write. Two cases need it,
// both of which would otherwise leave a second entry behind — a relocated hook
// script (the old entry then runs a deleted file), and a settings.json copied in
// from another config dir with --copy (the old entry runs *that* dir's script, so
// the hook fires twice per stop).
//
// This is the Go replacement for the jq block in the old install.sh — same
// behaviour and same on-disk shape, with no external jq dependency.
func ensureHook(path, event, hookCmd string, isObsolete func(cmd string) bool) (bool, error) {
	changed, err := ensureHooks(path, []hookReg{{event: event, cmd: hookCmd, obsolete: isObsolete}}, "")
	return changed[event], err
}

// hookReg is one event → command registration for ensureHooks. A nil obsolete
// supersedes nothing, which is what a caller with no older command to retire
// passes.
type hookReg struct {
	event    string
	cmd      string
	obsolete func(cmd string) bool

	// retire drops the matching registrations and writes none back, which is how
	// an event this kit USED to register stops being registered.
	//
	// Without it, an install could only ever add: ensureHooks walks the events it
	// is given, so an event simply left out of the plan keeps whatever an older
	// install wrote. That is invisible on a fresh machine and wrong on every
	// upgraded one — the Windows SessionEnd hook went on firing and failing after
	// the installer stopped planning it (#150).
	retire bool
}

// ensureHooks registers every entry in regs in ONE read-modify-write of the
// settings JSON at path, and returns the set of events it actually changed.
//
// Batching is not a micro-optimisation, it is the fix for a defect the
// per-event version produced. Every write backs the file up first, so
// registering five events one call at a time left FOUR timestamped backups in
// the user's config dir on every install that added them — the config dir
// filling with copies of itself, and the count growing by one with each hook the
// product gains. One read, one backup, one write.
//
// When every registration is already present and nothing was superseded — the
// common case on a re-install — it writes nothing at all: no file touched, no
// backup, and an empty changed set. That is also why `changed` is a set rather
// than a bool: the caller reports per event, and "which of these five are new"
// is not answerable from a single flag.
//
// A registration that fails to parse or that finds a value of the wrong shape
// aborts the WHOLE batch before anything is written, so the file is never left
// carrying half of an install.
// ensureHooks writes every hook registration, and the statusLine when one is
// given, in a single read-modify-write of settings.json.
func ensureHooks(path string, regs []hookReg, statusLineCmd string) (map[string]bool, error) {
	changed := map[string]bool{}

	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	settings := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			// Refuse to touch a file we can't parse: overwriting a user's
			// hand-edited settings.json would be worse than failing loudly.
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	hooks, err := childObject(settings, "hooks")
	if err != nil {
		return nil, err
	}

	for _, reg := range regs {
		entries, err := childArray(hooks, reg.event)
		if err != nil {
			return nil, err
		}

		pruned, dropped := dropHook(entries, reg.obsolete)
		// A retirement is the prune and nothing else: whatever survived stays, and
		// this kit's own command is not written back.
		if reg.retire {
			if !dropped {
				continue
			}
			// An event with nothing left loses its key rather than keeping an empty
			// array: `"SessionEnd": []` is a visible stub of a hook that is meant to
			// be absent, and the next person to read the file has to work out
			// whether it means "removed" or "never configured".
			if len(pruned) == 0 {
				delete(hooks, reg.event)
			} else {
				hooks[reg.event] = pruned
			}
			changed[reg.event] = true
			continue
		}
		bounded := ensureHookTimeout(pruned, reg.cmd)
		if hookPresent(pruned, reg.cmd) && !dropped && !bounded {
			continue
		}

		if !hookPresent(pruned, reg.cmd) {
			// Append a matcher-less entry carrying our command — the same shape
			// Claude Code writes and the same shape the old install.sh produced,
			// plus the deadline every child this kit starts must carry.
			pruned = append(pruned, map[string]any{
				"hooks": []any{
					map[string]any{"type": "command", "command": reg.cmd, "timeout": hookTimeoutSeconds},
				},
			})
		}
		hooks[reg.event] = pruned
		changed[reg.event] = true
	}

	statusWritten := false
	if statusLineCmd != "" {
		statusWritten = applyStatusLine(settings, statusLineCmd)
		if statusWritten {
			changed["statusLine"] = true
		}
	}

	if len(changed) == 0 {
		return changed, nil
	}
	settings["hooks"] = hooks

	if err := backupConfig(path, raw); err != nil {
		return nil, err
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return nil, err
	}
	return changed, nil
}

// codexStopHookBlock renders the one TOML block the installer owns. Markers let
// a later install replace only these bytes: config.toml also carries models,
// MCPs, features, policies, and hooks from other products, so decoding and
// re-encoding the whole file would turn one registration into an unsolicited
// rewrite of the user's configuration. The closing marker deliberately shares
// the command line: Codex inserts new tables before a trailing standalone
// comment, which would otherwise put foreign config inside our managed range.
func codexStopHookBlock(cmd string) string {
	return codexStopHookStart + `
[[hooks.Stop]]
matcher = "*"

[[hooks.Stop.hooks]]
type = "command"
command = ` + strconv.Quote(cmd) + " " + codexStopHookEnd + "\n"
}

// ensureCodexStopHook registers the agentsmemory Stop hook in Codex's native
// config.toml representation. It appends or replaces one marked block while
// preserving every byte outside that block, writes one backup before a change,
// and is a true no-op when the registration is already current.
func ensureCodexStopHook(path, cmd string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if len(raw) > 0 {
		var existing map[string]any
		if err := toml.Unmarshal(raw, &existing); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	text := string(raw)
	starts := strings.Count(text, codexStopHookStart)
	ends := strings.Count(text, codexStopHookEnd)
	if starts != ends || starts > 1 {
		return false, fmt.Errorf("parse %s: agentsmemory managed Stop-hook markers are unbalanced or duplicated", path)
	}

	block := codexStopHookBlock(cmd)
	var out string
	if starts == 0 {
		out = text
		if out != "" {
			if !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			out += "\n"
		}
		out += block
	} else {
		start := strings.Index(text, codexStopHookStart)
		endMarker := strings.Index(text, codexStopHookEnd)
		if endMarker < start {
			return false, fmt.Errorf("parse %s: agentsmemory managed Stop-hook end marker precedes its start", path)
		}
		end := endMarker + len(codexStopHookEnd)
		// The rendered block owns the line ending after its closing marker. Consume
		// one existing line ending so replacing it cannot accumulate blank lines.
		if end < len(text) && text[end] == '\r' {
			end++
		}
		if end < len(text) && text[end] == '\n' {
			end++
		}
		out = text[:start] + block + text[end:]
	}

	if out == text {
		return false, nil
	}
	// Validating the candidate catches namespace collisions that a textual merge
	// cannot see. In particular, a valid `hooks = { ... }` inline table is
	// immutable in TOML and cannot later be reopened as [[hooks.Stop]]. Refuse
	// before the backup or write rather than leave Codex unable to parse its config.
	var candidate map[string]any
	if err := toml.Unmarshal([]byte(out), &candidate); err != nil {
		return false, fmt.Errorf("register Codex Stop hook in %s: generated TOML would be invalid: %w", path, err)
	}
	if err := backupConfig(path, raw); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// retireLegacyCodexHook removes only agentsmemory's Stop command from Codex's
// previous hooks.json representation. When that was the file's sole content,
// the file itself is removed so Codex no longer loads two hook layers. Foreign
// content is preserved and reported to the caller through remains.
func retireLegacyCodexHook(path string) (changed, remains bool, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}

	settings := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&settings); err != nil {
		return false, true, fmt.Errorf("parse %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return false, true, fmt.Errorf("parse %s: trailing data: %w", path, err)
	}
	hooks, err := childObject(settings, "hooks")
	if err != nil {
		return false, true, fmt.Errorf("parse %s: %w", path, err)
	}
	entries, err := childArray(hooks, "Stop")
	if err != nil {
		return false, true, fmt.Errorf("parse %s: %w", path, err)
	}
	targetDir := filepath.Dir(path)
	currentPath := filepath.Join(targetDir, hookFile)
	preRelocationPath := filepath.Join(targetDir, legacyHookRel)
	pruned, dropped := dropHook(entries, func(cmd string) bool {
		return installerHookCommandMatches(cmd, currentPath) ||
			installerHookCommandMatches(cmd, preRelocationPath)
	})
	if !dropped {
		return false, true, nil
	}

	if len(pruned) == 0 {
		delete(hooks, "Stop")
	} else {
		hooks["Stop"] = pruned
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	if err := backupConfig(path, raw); err != nil {
		return false, true, err
	}
	if len(settings) == 0 {
		if err := os.Remove(path); err != nil {
			return false, true, err
		}
		return true, false, nil
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, true, err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return false, true, err
	}
	return true, true, nil
}

// backupConfig writes the exact pre-change bytes beside a shared config file.
// Nanosecond precision avoids clobbering an earlier backup on a same-second run.
func backupConfig(path string, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s before backup: %w", path, err)
	}
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(backup, raw, info.Mode().Perm()); err != nil {
		return fmt.Errorf("backup %s: %w", path, err)
	}
	return nil
}

// childObject returns settings[key] as a JSON object, creating an empty one if
// the key is absent. It errors if the key exists but holds a non-object, so we
// never silently clobber a value of the wrong shape.
func childObject(m map[string]any, key string) (map[string]any, error) {
	switch v := m[key].(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return v, nil
	default:
		return nil, fmt.Errorf("settings key %q is %T, expected an object", key, v)
	}
}

// childArray returns m[key] as a JSON array, creating an empty one if absent, and
// errors if the key holds a non-array.
func childArray(m map[string]any, key string) ([]any, error) {
	switch v := m[key].(type) {
	case nil:
		return []any{}, nil
	case []any:
		return v, nil
	default:
		return nil, fmt.Errorf("settings key %q is %T, expected an array", key, v)
	}
}

// dropHook returns the event's entries without any hook whose command isObsolete matches,
// and reports whether anything was removed. An entry carrying other hooks
// alongside the matched one keeps those: only the matching hook is taken out, so
// a user's own command sitting beside ours survives. A nil predicate is a no-op,
// which is what callers with nothing to supersede pass.
func dropHook(stop []any, isObsolete func(string) bool) ([]any, bool) {
	if isObsolete == nil {
		return stop, false
	}
	out := make([]any, 0, len(stop))
	dropped := false
	for _, entry := range stop {
		em, ok := entry.(map[string]any)
		if !ok {
			out = append(out, entry)
			continue
		}
		inner, ok := em["hooks"].([]any)
		if !ok {
			out = append(out, entry)
			continue
		}
		kept := make([]any, 0, len(inner))
		for _, h := range inner {
			if hm, ok := h.(map[string]any); ok {
				if c, _ := hm["command"].(string); isObsolete(c) {
					dropped = true
					continue
				}
			}
			kept = append(kept, h)
		}
		if len(kept) == 0 {
			continue // the entry existed only to run cmd
		}
		em["hooks"] = kept
		out = append(out, em)
	}
	return out, dropped
}

// hookPresent reports whether any entry already registers command cmd,
// so re-running the installer never duplicates the hook.
// hookTimeoutSeconds is the deadline written into every hook registration, in
// the seconds Claude Code's `timeout` field takes.
//
// It exists because of the owner's rule of 2026-09-05: every child a hook
// starts carries a timeout, and the runner reaps it. Claude Code applies a
// default when the field is absent, but a default is not a declaration — it
// changes with the harness, and nothing in this tree could say what bound a
// hook actually ran under. Sixty seconds is the recall hooks' own client
// `--timeout` (`aiagentmemory mcp search`), so the harness deadline and the
// one call that can hang agree, and a hook killed here is one whose inner
// call had already given up.
const hookTimeoutSeconds = 60

// ensureHookTimeout sets the deadline on every registration carrying cmd that
// has none, and reports whether it changed anything. Registrations written by
// an older kit have no `timeout`, and an install that only appends would leave
// them unbounded for ever — "already registered" must not mean "left as it was".
func ensureHookTimeout(entries []any, cmd string) bool {
	changed := false
	for _, entry := range entries {
		em, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := em["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range inner {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if c, _ := hm["command"].(string); c != cmd {
				continue
			}
			if _, has := hm["timeout"]; has {
				continue
			}
			hm["timeout"] = hookTimeoutSeconds
			changed = true
		}
	}
	return changed
}

func hookPresent(stop []any, cmd string) bool {
	for _, entry := range stop {
		em, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := em["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range inner {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if c, _ := hm["command"].(string); c == cmd {
				return true
			}
		}
	}
	return false
}

// ensureMCPServer registers an MCP server under "mcpServers" in the JSON file at
// path, idempotently, and reports whether it changed the file.
//
// It exists because Cursor ships no command that registers one: `cursor-agent
// mcp` offers login, list, list-tools, enable and disable, so this is the first
// registration path with no CLI between us and another product's config file.
// Every other agent's `mcp add` merges on our behalf and cannot lose anything.
//
// So it takes the same discipline ensureHooks takes with settings.json — read
// once, merge, back the original up, write once, and write NOTHING when the entry
// is already identical — and refuses a file it cannot parse rather than replacing
// it. mcp.json is shared with every other MCP server the user runs, and a hand
// edit with a trailing comma is common; overwriting it would destroy
// configuration we never read.
func ensureMCPServer(path, name string, entry map[string]any) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}

	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	servers, err := childObject(cfg, "mcpServers")
	if err != nil {
		return false, err
	}
	// Already identical: write nothing. Comparing the MARSHALLED forms rather than
	// the maps is what makes a re-install a true no-op — reflect.DeepEqual on
	// values that came back through json.Unmarshal compares interface types, and
	// an entry we built and an entry we read back are not the same types even when
	// they are the same JSON.
	if existing, ok := servers[name]; ok {
		was, err1 := json.Marshal(existing)
		now, err2 := json.Marshal(entry)
		if err1 == nil && err2 == nil && string(was) == string(now) {
			return false, nil
		}
	}
	servers[name] = entry
	cfg["mcpServers"] = servers

	if err := backupConfig(path, raw); err != nil {
		return false, err
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// applyStatusLine fills settings.json's `statusLine` key, and REFUSES to replace
// one the user already set (ADR-051 T7).
//
// ⚠ THE REFUSAL IS THE DESIGN, NOT A LIMITATION. A status line is the one surface
// a user cannot dismiss, and many people have already put something they care
// about there — a git branch, a model name, a cost counter. Overwriting it would
// be the most visible thing this installer does and the least invited. So an
// existing value is left alone and reported, and only an ABSENT key is filled.
//
// ⚠ IT MUTATES THE MAP AND DOES NO I/O, WHICH A GATE INSISTED ON. Written first as
// its own read-modify-write, it produced a SECOND backup of settings.json in a
// single install and TestOneInstallLeavesAtMostOneBackup failed — correctly: two
// backups from one run means a user cannot tell which one is the state they had
// before. One install, one read, one backup, one write.
func applyStatusLine(settings map[string]any, command string) bool {
	if existing, ok := settings["statusLine"]; ok && existing != nil {
		return false
	}
	settings["statusLine"] = map[string]any{"type": "command", "command": command}
	return true
}
