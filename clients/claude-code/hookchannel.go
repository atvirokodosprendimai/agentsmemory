package main

import "regexp"

// injectingEvents are the hook events whose plain stdout Claude Code adds to the
// model's context as text the model can read and act on. Every other event sends a
// hook's stdout to the debug log, where nothing reads it.
//
// The list is exhaustive and it is short on purpose — from Claude Code's hooks
// reference, 2026-08-28: "For most events, stdout is written to the debug log but
// not shown in the transcript. The exceptions are UserPromptSubmit,
// UserPromptExpansion, and SessionStart, where Claude Code adds plain-text stdout
// as context that Claude can see and act on."
//
// ⚠ IT LIVES IN PRODUCTION CODE BECAUSE TWO THINGS READ IT. It began in
// hookchannel_test.go, where TestEveryInjectingHookIsOnAnInjectingEvent checks the
// installer's PLAN. `doctor` asks the same question of the install in front of an
// operator, and a test file cannot be imported — so the second reader would have
// needed a second copy, and two copies of "which events inject" is exactly the
// drift this repository keeps gating against.
var injectingEvents = map[string]bool{
	"SessionStart":        true,
	"UserPromptSubmit":    true,
	"UserPromptExpansion": true,
}

// hookOutputDecl is the `# hook-output: <channel>` line every script in hooks/
// carries. The channel is the first token; anything after an em dash is the
// author's reason for not using the injecting channel.
var hookOutputDecl = regexp.MustCompile(`(?m)^# hook-output:[ \t]*([a-z-]+)[ \t]*(.*)$`)

// channelStdoutInjected is the declaration a hook carries when its whole purpose is
// to put text in front of the model. Only these are worth running from `doctor`:
// every other channel reports somewhere a human already looks.
const channelStdoutInjected = "stdout-injected"
