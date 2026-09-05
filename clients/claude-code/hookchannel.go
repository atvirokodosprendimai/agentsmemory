package main

import "regexp"

// eventChannel is what Claude Code does with a hook's plain stdout on a given
// event: inject it into the model's context, or write it to the debug log where
// nothing reads it.
type eventChannel int

const (
	// channelUnknown is an event this build has never heard of. It is a distinct
	// answer from "does not inject", and that distinction is the whole reason this
	// type exists — see hookEventChannel.
	channelUnknown eventChannel = iota
	// channelInjected: plain stdout is added to the model's context.
	channelInjected
	// channelDebugLog: plain stdout goes to the debug log and the model never
	// sees a character of it.
	channelDebugLog
)

// injectingEvents are the hook events whose plain stdout Claude Code adds to the
// model's context as text the model can read and act on.
//
// Source: Claude Code hooks reference, https://code.claude.com/docs/en/hooks,
// read 2026-09-04. The page names four, in one exhaustive sentence: "For most
// events, Claude Code writes stdout to the debug log and doesn't show it in the
// transcript. The exceptions are UserPromptSubmit, UserPromptExpansion,
// SessionStart, and PostModelSwitch, where Claude Code adds plain-text stdout as
// context that Claude can see and act on."
//
// ⚠ THIS LIST HAS GONE STALE TWICE, AND THE SECOND TIME IT WAS THIS COMMENT THAT
// WAS WRONG. It first read SessionStart, UserPromptSubmit and UserPromptExpansion
// (2026-08-28). It was then changed to drop UserPromptExpansion and add
// PostModelSwitch, carrying a paragraph asserting the reference had MOVED
// UserPromptExpansion to the debug-log side. It had not. Re-read 2026-09-04 the
// page names all four, and the intervening version was a set of three that
// forbade a channel that works.
//
// The direction of that failure is what makes it expensive, and it is the reason
// this correction is recorded rather than quietly applied. A missing entry does
// not merely fail to describe a capability — `doctor` labels a hook registered on
// the absent event DISCARDED and exits non-zero, and
// TestEveryInjectingHookIsOnAnInjectingEvent rejects an install plan carrying one.
// The table REFUSES the channel. That is this repository's reachability defect
// inverted: not a feature nothing selects, but a gate refusing a feature that does.
//
// Two things follow, and both are in the code rather than in this comment: the
// retrieval date is recorded above so a reader knows how old the claim is, and
// every other documented event is listed below so an event MISSING from both sets
// is reported as unknown rather than assumed harmless.
//
// ⚠ NO TEST FETCHES THIS PAGE, deliberately. A gate that makes a network call
// fails when the network does and turns an upstream edit into a red build on an
// unrelated branch. TestTheInjectingSetIsTheDocumentedFour pins the membership;
// the date above is the honesty mechanism; `doctor` is where an operator learns
// the claim is old.
//
// ⚠ IT LIVES IN PRODUCTION CODE BECAUSE TWO THINGS READ IT. It began in
// hookchannel_test.go, where TestEveryInjectingHookIsOnAnInjectingEvent checks the
// installer's PLAN. `doctor` asks the same question of the install in front of an
// operator, and a test file cannot be imported — so the second reader would have
// needed a second copy, and two copies of "which events inject" is exactly the
// drift this repository keeps gating against.
var injectingEvents = map[string]bool{
	"UserPromptSubmit":    true,
	"UserPromptExpansion": true,
	"SessionStart":        true,
	"PostModelSwitch":     true,
}

// debugLogEvents are the documented events whose plain stdout does NOT reach the
// model. They are listed for one reason: so that an event in neither map is a
// KNOWN UNKNOWN rather than a silent "no".
//
// ⚠ THE CLOSED-WORLD ASSUMPTION IS THE DEFECT THIS REPLACES. The previous shape
// was one map and a lookup: `injectingEvents[e]` answered false for
// UserPromptExpansion, for PostModelSwitch, and for an event Claude Code ships
// next month, and those are three different facts. Reporting an unknown as a
// definite "does not inject" is the same failure this project already recorded
// against code anchors, where an unlabelled anchor was checked against whatever
// tree was open — an unknown that reads as an answer.
//
// Same source and date as injectingEvents above.
var debugLogEvents = map[string]bool{
	"Setup": true, "PreToolUse": true,
	"PermissionRequest": true, "PermissionDenied": true, "PostToolUse": true,
	"PostToolUseFailure": true, "PostToolBatch": true, "Notification": true,
	"MessageDisplay": true, "SubagentStart": true, "SubagentStop": true,
	"TaskCreated": true, "TaskCompleted": true, "Stop": true, "StopFailure": true,
	"TeammateIdle": true, "InstructionsLoaded": true, "ConfigChange": true,
	"CwdChanged": true, "DirectoryAdded": true, "FileChanged": true,
	"WorktreeCreate": true, "WorktreeRemove": true, "PreCompact": true,
	"PostCompact": true, "PreModelSwitch": true, "Elicitation": true,
	"ElicitationResult": true, "SessionEnd": true,
}

// hookEventChannel says what happens to a hook's stdout on this event, and admits
// when it does not know.
//
// The third answer is the point. A caller that only ever hears "injects" or "does
// not inject" cannot distinguish a documented debug-log event from one nobody has
// classified, and will report a confident verdict about an event this build has
// never seen.
func hookEventChannel(event string) eventChannel {
	switch {
	case injectingEvents[event]:
		return channelInjected
	case debugLogEvents[event]:
		return channelDebugLog
	default:
		return channelUnknown
	}
}

// hookOutputDecl is the `# hook-output: <channel>` line every script in hooks/
// carries. The channel is the first token; anything after an em dash is the
// author's reason for not using the injecting channel.
var hookOutputDecl = regexp.MustCompile(`(?m)^# hook-output:[ \t]*([a-z-]+)[ \t]*(.*)$`)

// channelStdoutInjected is the declaration a hook carries when its whole purpose is
// to put text in front of the model. Only these are worth running from `doctor`:
// every other channel reports somewhere a human already looks.
const channelStdoutInjected = "stdout-injected"

// channelNotAHook is the declaration a file in hooks/ carries when no event runs
// it at all: the `/stats` helper the Stop and SessionEnd hooks source, and the
// statusLine command. They live beside the hooks and are not hooks, so the
// wiring check must not report them as unregistered — the declaration is where
// they say which they are.
const channelNotAHook = "not-a-hook"
