package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pinnedOnPurpose lists the documented variables a shipped overlay may set to a
// literal, keyed by "<compose file>::<VAR>", with the reason it must not be
// operator-settable. The reason is the review: an entry without one is refused
// by TestAPinnedOverlayValueIsJustified, the same way notOperatorFacing works
// one file over.
var pinnedOnPurpose = map[string]string{
	"docker-compose.full.yml::agentsmemory::VECTOR_BACKEND": "the overlay RUNS the qdrant it names; pointing the " +
		"server at another backend while this file starts qdrant is not a configuration, it is two stacks",
	"docker-compose.full.yml::agentsmemory::QDRANT_URL": "the address of the container this file defines; an " +
		"operator who wants a different qdrant is not using this overlay",
	"docker-compose.host.yml::agentsmemory::QDRANT_URL": "the whole point of the host overlay is reaching the " +
		"host's qdrant through its published port",
	"docker-compose.host.yml::agentsmemory::OLLAMA_URL":   "same: the overlay exists to name the host's ollama",
	"docker-compose.ollama.yml::agentsmemory::OLLAMA_URL": "the address of the ollama container this file defines",
}

// TestADocumentedKnobIsNotPinnedByAnOverlay catches the class of defect where a
// variable is read, documented, and still unusable in the configuration an
// operator is running.
//
// TestDocumentedEnvVarsAreRead asks whether anything reads the variable, and
// RERANK_URL passed it: the server reads it, .env.docker.example documents it as
// the way to turn reranking off, and the disable route did nothing. An
// `environment:` entry beats `env_file:`, so the shipped overlay overrode the
// documented file — the operator edited .env.docker, restarted, and the stack
// went on paying a 10s rerank timeout on every search (#154, measured 17.25s for
// 10 documents against RERANK_TIMEOUT=10s, identical scores to four decimals once
// disabled).
//
// This is ADR-006's "read in the MODE THAT IS RUNNING" applied to Compose rather
// than to flags. A literal is still allowed where the overlay's own identity
// depends on it — the address of a container it starts — but it has to be
// declared, because the difference between "pinned deliberately" and "pinned by
// accident" is not visible in the YAML.
func TestADocumentedKnobIsNotPinnedByAnOverlay(t *testing.T) {
	root := repoRoot(t)
	documented := documentedEnvVars(t, root)
	if len(documented) == 0 {
		t.Fatal("no documented variables were found, so this gate examined nothing — the shape " +
			"that makes a green run meaningless")
	}

	checked := 0
	for _, a := range composeEnvAssignments(t, root) {
		if !documented[a.Name] || a.interpolatesItself() {
			continue
		}
		checked++
		if _, ok := pinnedOnPurpose[a.key()]; ok {
			continue
		}
		t.Errorf("%s pins %s=%s in service %q, and %s is documented as an operator knob. "+
			"environment: beats env_file:, so editing it in the documented file changes nothing. "+
			"Interpolate it (${%s-%s}) so the shipped default still applies with no env file while "+
			"an operator can override it, or add %q to pinnedOnPurpose with the reason it must not move.",
			a.File, a.Name, a.Value, a.Service, a.Name, a.Name, strings.Trim(a.Value, `"`), a.key())
	}
	t.Logf("examined %d literal assignment(s) of documented variables", checked)
}

// TestAPinnedOverlayValueIsJustified refuses an exemption with no reason and one
// that has stopped earning its place, because a list that only ever grows turns
// the gate above into paperwork.
//
// "Stopped earning it" has two shapes and the first version caught neither
// properly: the assignment is no longer a literal (someone interpolated it), and
// the variable is no longer DOCUMENTED, in which case the gate would not have
// fired on it and the entry is cover for nothing.
func TestAPinnedOverlayValueIsJustified(t *testing.T) {
	root := repoRoot(t)
	documented := documentedEnvVars(t, root)
	needsCover := map[string]bool{}
	for _, a := range composeEnvAssignments(t, root) {
		if documented[a.Name] && !a.interpolatesItself() {
			needsCover[a.key()] = true
		}
	}
	for key, reason := range pinnedOnPurpose {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s is exempt with no reason. The reason is the review — without it the "+
				"exemption list is a way to silence the gate rather than to record a decision", key)
		}
		if !needsCover[key] {
			t.Errorf("%s is exempt but the gate would not fire on it: it is either no longer "+
				"pinned to a literal, no longer documented as an operator knob, or gone. An "+
				"exemption that covers nothing outlives what it described", key)
		}
	}
}

// documentedEnvVars is the set of variables the shipped env examples present to
// an operator as settable.
func documentedEnvVars(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, rel := range []string{".env.example", ".env.docker.example"} {
		for _, v := range envVarsIn(t, filepath.Join(root, rel)) {
			out[v] = true
		}
	}
	return out
}

// composeAssignment is one environment entry, qualified by the service that
// carries it. The service is part of the identity because two services in one
// file can set the same variable for opposite reasons — the server's OLLAMA_URL
// names where to reach ollama, and a keying scheme that dropped the service would
// let one of them stand in for the other.
type composeAssignment struct {
	File, Service, Name, Value string
}

// key is the exemption identity: file, service and variable.
func (a composeAssignment) key() string { return a.File + "::" + a.Service + "::" + a.Name }

// interpolatesItself reports whether the value lets an operator override THIS
// variable, which is narrower than "contains a ${".
//
// ⚠ NARROWER ON PURPOSE, and the first version was not. It asked whether the raw
// value contained "${" anywhere, so `RERANK_URL: http://x # see ${OTHER}` and a
// value interpolating a DIFFERENT variable both read as overrideable — the gate
// would then pass over exactly the pinned knob it exists to catch. Reported by
// review before this shipped.
func (a composeAssignment) interpolatesItself() bool {
	v := stripInlineComment(a.Value)
	return strings.Contains(v, "${"+a.Name)
}

// stripInlineComment removes a trailing YAML comment, respecting quotes so a "#"
// inside a quoted value is kept.
func stripInlineComment(v string) string {
	inSingle, inDouble := false, false
	for i, r := range v {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && i > 0 && v[i-1] == ' ' {
				return strings.TrimSpace(v[:i])
			}
		}
	}
	return strings.TrimSpace(v)
}

// composeEnvAssignments returns every environment assignment in the shipped
// compose files, qualified by service.
//
// It reads VALUES and not just keys, which is what separates it from
// composeEnvVarsIn: the defect is the value being a literal rather than an
// interpolation. It accepts BOTH Compose forms — the `KEY: value` mapping and the
// `- KEY=value` list — because both are valid and a gate that reads one of them
// is a gate the next author walks around without knowing it. envreach_test.go's
// own parser already handles both, which is where the omission showed.
func composeEnvAssignments(t *testing.T, root string) []composeAssignment {
	t.Helper()
	mapForm := regexp.MustCompile(`^([A-Z][A-Z0-9_]*)\s*:\s*(\S.*)$`)
	listForm := regexp.MustCompile(`^-\s*([A-Z][A-Z0-9_]*)=(.*)$`)
	serviceLine := regexp.MustCompile(`^([a-z0-9][a-z0-9_-]*):\s*$`)

	files, _ := filepath.Glob(filepath.Join(root, "docker-compose*.yml"))
	var out []composeAssignment
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		service := ""
		inEnv, depth := false, 0
		for _, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " "))
			// A service name sits at one indent level under `services:`; tracking
			// it is what makes an assignment's identity include WHERE it lives.
			if m := serviceLine.FindStringSubmatch(trimmed); m != nil && indent == 2 {
				service, inEnv = m[1], false
				continue
			}
			if strings.HasSuffix(trimmed, "environment:") {
				inEnv, depth = true, indent
				continue
			}
			if inEnv && trimmed != "" && indent <= depth {
				inEnv = false
			}
			if !inEnv || trimmed == "" {
				continue
			}
			if m := listForm.FindStringSubmatch(trimmed); m != nil {
				out = append(out, composeAssignment{rel, service, m[1], strings.TrimSpace(m[2])})
				continue
			}
			if m := mapForm.FindStringSubmatch(trimmed); m != nil {
				out = append(out, composeAssignment{rel, service, m[1], strings.TrimSpace(m[2])})
			}
		}
	}
	return out
}

// TestTheComposeParserSeesBothFormsAndNeitherComment drives the parser over a
// fixture that IS the shape it used to miss.
//
// The shipped files happen to use only the mapping form today, so the corpus
// cannot exercise list-form parsing at all — a gate that is right only about the
// files it happens to face is a gate that goes wrong on the commit that changes
// them. Every case below was a real hole in the first version: list entries were
// invisible, two services collapsed into one, and an inline comment mentioning
// any ${...} made a pinned literal read as overrideable.
func TestTheComposeParserSeesBothFormsAndNeitherComment(t *testing.T) {
	dir := t.TempDir()
	fixture := `services:
  alpha:
    environment:
      PINNED_MAP: literal-value
      COMMENTED: literal # see ${SOMETHING_ELSE}
      REAL_INTERP: "${REAL_INTERP-fallback}"
      OTHER_INTERP: "${SOMEONE_ELSE:-2}"
  beta:
    environment:
      - PINNED_LIST=from-a-list
      - SHARED=beta-value
    volumes:
      - ./x:/x
`
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.fixture.yml"), []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := map[string]composeAssignment{}
	for _, a := range composeEnvAssignments(t, dir) {
		got[a.Service+"::"+a.Name] = a
	}
	for _, want := range []string{"alpha::PINNED_MAP", "alpha::COMMENTED", "beta::PINNED_LIST", "beta::SHARED"} {
		if _, ok := got[want]; !ok {
			t.Errorf("the parser did not see %s, so a pinned knob in that form is invisible to "+
				"the gate: %v", want, got)
		}
	}
	if _, ok := got["beta::x"]; ok {
		t.Error("a volumes entry was read as an environment assignment")
	}
	for name, wantInterp := range map[string]bool{
		"alpha::PINNED_MAP":   false,
		"alpha::COMMENTED":    false, // the ${...} is in a COMMENT, not the value
		"alpha::REAL_INTERP":  true,
		"alpha::OTHER_INTERP": false, // interpolates a DIFFERENT variable
		"beta::PINNED_LIST":   false,
	} {
		a, ok := got[name]
		if !ok {
			t.Errorf("%s is missing from the parse", name)
			continue
		}
		if a.interpolatesItself() != wantInterp {
			t.Errorf("%s: interpolatesItself()=%v, want %v (value %q). Reading a foreign ${...} "+
				"as an override is how a pinned knob passes the gate",
				name, a.interpolatesItself(), wantInterp, a.Value)
		}
	}
}
