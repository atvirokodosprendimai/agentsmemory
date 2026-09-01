package main

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/longmemeval"
)

// TestLongmemevalIsRegistered covers the rung the command's own tests cannot
// see: they build their own root, so every one of them would pass with the
// registration line deleted from main.go. This drives the REAL root.
func TestLongmemevalIsRegistered(t *testing.T) {
	root := rootCommand(config.Default())
	var names []string
	for _, c := range root.Commands {
		names = append(names, c.Name)
	}
	for _, n := range names {
		if n == "longmemeval" {
			return
		}
	}
	t.Errorf("the CLI registers %v and not \"longmemeval\" — the grid exists and cannot be run", names)
}

// TestLongmemevalHelpListsEveryRegisteredPolicy closes rung 3 at the command
// level: --help is documentation, and it must be DERIVED from the registries
// rather than typed. T2's TestEveryDeclaredPolicyIsSelectable gates the
// rendering; this gates that the flag actually uses it.
func TestLongmemevalHelpListsEveryRegisteredPolicy(t *testing.T) {
	cmd := longmemevalCommand(config.Default())
	var usage string
	for _, f := range cmd.Flags {
		for _, n := range f.Names() {
			if n == "write" || n == "query" {
				usage += f.String()
			}
		}
	}
	for _, p := range longmemeval.WritePolicies() {
		if !strings.Contains(usage, p.Name) {
			t.Errorf("--help does not name write policy %q, so an operator cannot discover it: %s",
				p.Name, usage)
		}
	}
	for _, p := range longmemeval.QueryPolicies() {
		if !strings.Contains(usage, p.Name) {
			t.Errorf("--help does not name query policy %q: %s", p.Name, usage)
		}
	}
}
