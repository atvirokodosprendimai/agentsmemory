package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryShippedHookIsCheckedForDrift derives its universe from what a real
// install WRITES, then edits every one of those files and requires doctor's
// drift check to name each.
//
// The map this replaces was hand-kept and listed six of ten scripts: touched,
// anchor-cue, task-recall and precompact were installed, registered, judged for
// their channel — and never compared against the binary's embedded copy, so an
// operator whose `update` refreshed the binary and left those four behind was
// told nothing had drifted. A list kept beside the truth goes stale one script
// at a time; this test reads the truth (the install) and fails on the first
// script the check cannot see. Found 2026-09-05 while adding the tenth hook.
func TestEveryShippedHookIsCheckedForDrift(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "agentsmemory-") || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append([]byte("# drifted\n"), body...), 0o755); err != nil {
			t.Fatal(err)
		}
		want = append(want, e.Name())
	}
	if len(want) < 8 {
		t.Fatalf("the install wrote only %d scripts (%v); this universe is too small to be the real kit", len(want), want)
	}
	sort.Strings(want)
	got := staleHooksIn(dir)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("doctor's drift check sees %v\nbut the install wrote and this test edited %v\n— every script it cannot see is one whose drift an operator is never told about", got, want)
	}
}

// TestEveryVerbatimAssetIsCheckedForDrift is the same gate over the widened
// universe: not only the hooks, but every file a real install writes byte for
// byte — the protocol and the commands included.
//
// ⚠ THE FILES THE HOOK-ONLY CHECK MISSED ARE THE ONES A SESSION READS. A stale
// hook eventually misbehaves and somebody debugs it. A stale
// agentsmemory-bootstrap.md keeps teaching a rule the project retired, to a
// model with no way to know — and `update` refreshes the BINARY in place, so a
// current binary beside a year-old protocol is what the documented upgrade
// produces. PR #334 corrected wording in commands/am.md because the old
// sentence was wrong; every kit installed before it still serves the retired
// one, and nothing said so. Issue #349.
//
// ⚠ WHAT THIS TEST DOES AND DOES NOT ASK. It iterates the DECLARED universe and
// proves the check can see every file in it — which it can by construction, so
// the by-name assertions below are what actually anchor it. It cannot notice a
// file the universe OMITS, because its expectation comes from the same function
// it is testing. That is why the by-name list exists, and why each entry was
// added only after someone found the omission by hand.
//
// The other direction — every file the install WROTE is checked or justified —
// is TestEveryWrittenFileIsCheckedOrJustified, which walks the install rather
// than the declaration. This comment used to claim the walk happened here; it
// did not, and the omissions that claim would have caught were found with a
// throwaway probe three times before the walk was written down.
func TestEveryVerbatimAssetIsCheckedForDrift(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	kit := inst.kit

	var want []string
	for name := range verbatimAssetFiles(kit) {
		p := filepath.Join(dir, name)
		body, err := os.ReadFile(p)
		if err != nil {
			continue // this kit did not install it; not this gate's finding
		}
		if err := os.WriteFile(p, append([]byte("drifted\n"), body...), 0o644); err != nil {
			t.Fatal(err)
		}
		want = append(want, name)
	}
	sort.Strings(want)

	// ⚠ The protocol and at least one command must be IN the universe, asserted
	// by name. Without this the test passes over a universe that quietly shrank
	// back to hooks — which is the state it was written to end.
	var sawProtocol, sawCommand bool
	for _, n := range want {
		if n == bootstrapFile {
			sawProtocol = true
		}
		if kit.commandsDir != "" && strings.HasPrefix(n, kit.commandsDir+string(filepath.Separator)) {
			sawCommand = true
		}
	}
	if !sawProtocol {
		t.Errorf("the drift universe does not include %s, the file a session reads first; it wrote %v",
			bootstrapFile, want)
	}
	if kit.commandsDir != "" && !sawCommand {
		t.Errorf("the drift universe includes no command under %s; it wrote %v", kit.commandsDir, want)
	}
	// Skills are verbatim and Claude-only; asserted by name for the same reason
	// the protocol is — so the universe cannot shrink back without saying so.
	if kit.name == agentClaude && len(nativeSkillAssets()) > 0 {
		var sawSkill bool
		for _, n := range want {
			if strings.HasPrefix(n, "skills"+string(filepath.Separator)) {
				sawSkill = true
			}
		}
		if !sawSkill {
			t.Errorf("the drift universe includes no skill, though this kit installs %d; it wrote %v",
				len(nativeSkillAssets()), want)
		}
	}
	// ⚠ The unattended permission boundary, asserted by name. A stale copy means
	// an unattended run enforcing an older boundary than this repo decided on.
	var sawUnattended bool
	for _, n := range want {
		if n == "agentsmemory-unattended-settings.json" {
			sawUnattended = true
		}
	}
	if !sawUnattended {
		t.Errorf("the drift universe does not include agentsmemory-unattended-settings.json, "+
			"the permission boundary an unattended run is given with --settings; it wrote %v", want)
	}
	// ⚠ AGENT DEFINITIONS MUST STAY OUT. writeAgentDefinitions substitutes the MCP
	// endpoint before writing, so a healthy install never matches the embed and
	// including them would report drift on every correct machine.
	for _, n := range want {
		if strings.HasPrefix(n, "agents"+string(filepath.Separator)) {
			t.Errorf("the drift universe includes %s, but agent definitions are TRANSFORMED at "+
				"install (the MCP endpoint is substituted), so every healthy install would be "+
				"reported as drifted", n)
		}
	}

	got := staleAssetsIn(dir, kit)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("doctor's drift check sees %v\nbut the install wrote and this test edited %v\n"+
			"— every file it cannot see is one whose drift an operator is never told about", got, want)
	}
}

// driftGatedKits are the kits this walk drives. claude-desktop is absent on
// purpose: its whole install is registering an mcp-stdio bridge, which needs the
// server binary on this machine and FETCHES it from GitHub Releases when absent,
// so `inst.run()` fails in a test environment for reasons that have nothing to
// do with drift. Naming the omission here beats a silent four-of-five.
var driftGatedKits = []agentKit{claudeKit, codexKit, piKit, cursorKit}

// notVerbatimOnInstall names, per kit, the files a real install writes that
// CANNOT be byte-compared against this binary's embed, with the reason each one
// is out. It is the escape hatch for the walk below, and every entry earns its
// place through TestEveryDriftExclusionIsJustifiedAndStillNeeded.
//
// ⚠ IT IS KEYED PER KIT BECAUSE THE SAME ASSET IS VERBATIM ON ONE AGENT AND
// TRANSFORMED ON ANOTHER. The operating protocol is written byte for byte as
// agentsmemory-bootstrap.md everywhere AND, on cursor, wrapped in `alwaysApply`
// frontmatter by writeProtocolRule as rules/agentsmemory.mdc. A flat path-keyed
// list would have to call that asset one thing or the other, and both answers
// are wrong for half the kits.
//
// The three ways a written file is legitimately not comparable, which is the
// whole taxonomy: MERGED into a file the operator also owns; TRANSFORMED from an
// asset before writing; GENERATED with no asset behind it at all.
var notVerbatimOnInstall = map[string]map[string]string{
	agentClaude: {
		"CLAUDE.md":     "MERGED: registerMemoryBootstrap merges a managed block into a user-owned memory file, preserving whatever else the operator wrote",
		"settings.json": "MERGED: hook registrations are merged into the agent's own settings, which the operator also edits",
		filepath.Join("agents", "agentsmemory-researcher.md"): "TRANSFORMED: writeAgentDefinitions substitutes mcpURLPlaceholder with this install's endpoint, so a healthy install never matches the embed",
	},
	agentCodex: {
		"AGENTS.md":        "MERGED: codex has no import directive, so the protocol is inlined into a managed block in a user-owned file",
		"config.toml":      "MERGED: the MCP registration is merged into codex's own config, which the operator also edits",
		"agentsmemory.env": "GENERATED: the per-install token and endpoint; there is no asset behind it",
		filepath.Join("agents", "agentsmemory-researcher.toml"): "TRANSFORMED: writeAgentDefinitions substitutes mcpURLPlaceholder with this install's endpoint",
	},
	agentPi: {
		"AGENTS.md":        "MERGED: the protocol is inlined into a managed block in a user-owned file",
		"agentsmemory.env": "GENERATED: the pi extension reads its endpoint and token from the environment, so both are persisted per install; there is no asset behind it",
	},
	agentCursor: {
		"mcp.json": "MERGED: the MCP registration is merged into cursor's own config, which the operator also edits",
		filepath.Join("rules", "agentsmemory.mdc"):            "TRANSFORMED: writeProtocolRule prepends `alwaysApply` frontmatter to the protocol bytes, so the same asset that is verbatim elsewhere is not comparable here",
		filepath.Join("agents", "agentsmemory-researcher.md"): "TRANSFORMED: writeAgentDefinitions substitutes mcpURLPlaceholder with this install's endpoint",
	},
}

// TestEveryWrittenFileIsCheckedOrJustified walks what a real install LANDED and
// requires every file to be either in the drift universe or justified above.
//
// ⚠ THIS IS THE DIRECTION THE SIBLING GATE CANNOT ASK. TestEveryVerbatimAssetIsCheckedForDrift
// takes its expectation from verbatimAssetFiles, so it proves the check sees
// what the universe declares and can never notice what the universe omits — and
// the universe is derived WITHIN each kind but hand-enumerated ACROSS kinds, so
// omitting a whole kind is exactly the mistake available. It has been made
// twice: agentsmemory-unattended-settings.json, whose asset sits at the embed
// root and whose installed name is prefixed, survived two readings of the asset
// list; and the pi bridge extension, which this walk found on its first run and
// which is that agent's entire MCP client.
//
// Reading the asset list finds neither. Driving the install and walking what
// lands finds both in one run, which is why this is a test rather than a
// convention.
//
// ⚠ ONLY THE FORWARD DIRECTION IS ASSERTED. The universe deliberately declares
// files a given kit never installs — codex, pi and cursor each leave ten or more
// hook entries unwritten, and staleAssetsIn skips an absent file by design — so
// "everything declared exists" would be red on three kits out of four and would
// be measuring subsets rather than drift.
func TestEveryWrittenFileIsCheckedOrJustified(t *testing.T) {
	for _, kit := range driftGatedKits {
		t.Run(kit.name, func(t *testing.T) {
			inst, _, dir := newTestInstallerFor(t, kit, false)
			if err := inst.run(); err != nil {
				t.Fatalf("install: %v", err)
			}
			declared := verbatimAssetFiles(inst.kit)
			justified := notVerbatimOnInstall[kit.name]

			written, unexplained := 0, []string{}
			if err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}
				rel, relErr := filepath.Rel(dir, p)
				if relErr != nil {
					return relErr
				}
				written++
				if _, ok := declared[rel]; ok {
					return nil
				}
				if strings.TrimSpace(justified[rel]) != "" {
					return nil
				}
				unexplained = append(unexplained, rel)
				return nil
			}); err != nil {
				t.Fatalf("walk %s: %v", dir, err)
			}

			// An install that wrote nothing would satisfy the loop above without
			// checking anything, and would look exactly like a clean run.
			if written == 0 {
				t.Fatalf("the %s install wrote no files at all into %s; this walk measured nothing",
					kit.name, dir)
			}
			sort.Strings(unexplained)
			if len(unexplained) > 0 {
				t.Errorf("the %s install writes %v, which doctor's drift check neither compares "+
					"against the embed nor justifies as merged, transformed or generated.\n"+
					"  A verbatim file outside the universe is one whose staleness an operator is "+
					"never told about; a non-verbatim one needs a reason in notVerbatimOnInstall, "+
					"because an unexplained exclusion and an oversight look identical from here.",
					kit.name, unexplained)
			}
		})
	}
}

// TestEveryDriftExclusionIsJustifiedAndStillNeeded refuses an exclusion with no
// reason, one naming a file its kit no longer writes, and one that is also
// declared verbatim.
//
// The middle case is the one that matters over time and the one an exclusion
// list never notices about itself: a file that stops being installed, or starts
// being written verbatim, leaves an entry that goes on excusing something real.
// That is the same standard TestNotOperatorFacingIsJustified holds its own
// escape hatch to, and the reason this list is safe to have at all.
func TestEveryDriftExclusionIsJustifiedAndStillNeeded(t *testing.T) {
	for _, kit := range driftGatedKits {
		t.Run(kit.name, func(t *testing.T) {
			entries := notVerbatimOnInstall[kit.name]
			if len(entries) == 0 {
				return // a kit needing no exclusion is not a finding
			}
			inst, _, dir := newTestInstallerFor(t, kit, false)
			if err := inst.run(); err != nil {
				t.Fatalf("install: %v", err)
			}
			declared := verbatimAssetFiles(inst.kit)
			for rel, reason := range entries {
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s excludes %s with no reason; the reason IS the review", kit.name, rel)
				}
				if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
					t.Errorf("%s excludes %s, which its install no longer writes — an exclusion "+
						"nobody removed goes on excusing whatever takes that path next", kit.name, rel)
				}
				if _, ok := declared[rel]; ok {
					t.Errorf("%s is both declared verbatim and excluded for %s; the exclusion is "+
						"dead and reads as a decision", rel, kit.name)
				}
			}
		})
	}
}
