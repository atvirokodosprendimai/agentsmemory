package main

import (
	"github.com/atvirokodosprendimai/agentsmemory/internal/repohygiene"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// envVarPattern matches the shape of an environment variable name as this
// project writes them: SCREAMING_SNAKE, at least two segments, so ordinary
// capitalised prose ("HTTP", "NOTE") does not trip the check.
var envVarPattern = regexp.MustCompile(`\b([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)\b`)

// commentPromise matches a variable shown WITH a value in a comment, which is
// what makes it an offer rather than a mention.
var commentPromise = regexp.MustCompile(`\b([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)=\S`)

// TestDocumentedEnvVarsAreRead fails when a variable this project documents to
// operators is read by nothing in the program.
//
// It exists because that is not a hypothetical. `EMBED_BACKEND=tei` was named in
// teiembed's package comment as the way to select a complete, unit-tested,
// live-tested embedding backend — and no code read it, so the sentence described
// a feature that did not exist. The same shape shipped three more times in one
// week: an eval arm that was never registered, an IDF coverage function with no
// branch in Search, a config field nothing consumed. In every case the capability
// was finished and the line making it selectable was missing, and every test
// passed because they exercised the component rather than the selection.
//
// Prose cannot be trusted to stay true; this makes the documentation load-bearing.
// A variable is "documented" if it appears in the operator-facing env examples or
// in a Go comment, and "read" if the program mentions it in a cli.EnvVars
// declaration or an os.Getenv call.
func TestDocumentedEnvVarsAreRead(t *testing.T) {
	root := repoRoot(t)

	documented := map[string][]string{} // var -> where it was promised
	for _, rel := range []string{".env.example", ".env.docker.example"} {
		for _, v := range envVarsIn(t, filepath.Join(root, rel)) {
			documented[v] = append(documented[v], rel)
		}
	}
	// Compose files are the strongest promise of all: an operator does not merely
	// read them, the stack RUNS with those values, so a variable set there and
	// read nowhere means the deployment is configured by a line that does
	// nothing. RERANK_TOP_K sat in docker-compose.full.yml claiming a rerank pool
	// of 20 that the server never read — the .env example happened to carry it
	// too, which is the only reason the first version of this check saw it.
	composeFiles, _ := repohygiene.ComposeFiles(root)
	for _, path := range composeFiles {
		rel, _ := filepath.Rel(root, path)
		for _, v := range composeEnvVarsIn(t, path) {
			documented[v] = append(documented[v], rel)
		}
	}
	// Go comments count too: teiembed's promise lived in a package comment, not
	// in an env file, which is precisely why nobody noticed.
	for _, path := range goFilesUnder(t, root) {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "//") {
				continue
			}
			// A comment counts as a PROMISE only when it shows the variable
			// with a value — "selected with EMBED_BACKEND=tei" is an offer an
			// operator can act on, while "there is deliberately no
			// RERANK_MODEL" or "(frozen CLOSET_CHAR_LIMIT)" is prose ABOUT a
			// variable, including prose explaining why it does not exist.
			// Without this distinction the check flags its own documentation
			// and gets deleted for noise.
			for _, m := range commentPromise.FindAllStringSubmatch(trimmed, -1) {
				if isProjectEnvVar(m[1]) {
					rel, _ := filepath.Rel(root, path)
					documented[m[1]] = append(documented[m[1]], rel)
				}
			}
		}
	}

	read := readEnvVars(t, root)
	var unread []string
	for v, where := range documented {
		if read[v] || !isProjectEnvVar(v) {
			continue
		}
		unread = append(unread, v+" (promised in "+strings.Join(dedupe(where), ", ")+")")
	}
	sort.Strings(unread)
	for _, u := range unread {
		t.Errorf("%s — documented to operators but read by nothing: either wire it or stop promising it", u)
	}
}

// isProjectEnvVar filters the pattern down to variables this project actually
// owns. Without it the check would flag every SCREAMING_SNAKE token in a comment
// — SQL keywords, HTTP headers, acronyms — and a noisy gate gets deleted.
func isProjectEnvVar(v string) bool {
	for _, prefix := range []string{
		"AGENTSMEMORY_", "EMBED_", "OLLAMA_", "QDRANT_", "RERANK_", "BM25_",
		"CLOSET_", "EVAL_", "APP_", "SUPERADMIN_", "VECTOR_", "ABSTAIN_",
	} {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return v == "FUSION" || v == "DB_PATH" || v == "LISTEN_ADDR"
}

// envVarsIn reads the assignments and commented-out assignments in an env
// example file: both `KEY=value` and `# KEY=value` are promises to an operator.
func envVarsIn(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		return nil // an absent example file is not this check's business
	}
	var out []string
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); envVarPattern.MatchString(key) && isProjectEnvVar(key) {
			out = append(out, key)
		}
	}
	return out
}

// composeEnvVarsIn reads the `KEY: value` entries of an `environment:` block.
// Compose also supports the `- KEY=value` list form; both are matched, because
// which one a file uses is a style choice and the promise is identical.
func composeEnvVarsIn(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	inEnv := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasSuffix(trimmed, "environment:") {
			inEnv = true
			continue
		}
		// Any non-indented key ends the block; so does a sibling key at the same
		// depth as `environment:` itself.
		if inEnv && trimmed != "" && !strings.HasPrefix(line, " ") {
			inEnv = false
		}
		if !inEnv || trimmed == "" {
			continue
		}
		entry := strings.TrimPrefix(trimmed, "- ")
		key, _, ok := strings.Cut(entry, ":")
		if !ok {
			key, _, ok = strings.Cut(entry, "=")
		}
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); envVarPattern.MatchString(key) && isProjectEnvVar(key) {
			out = append(out, key)
		}
	}
	return out
}

// readEnvVars collects every variable the program actually consults.
func readEnvVars(t *testing.T, root string) map[string]bool {
	t.Helper()
	read := map[string]bool{}
	consult := regexp.MustCompile(`(?:cli\.EnvVars\(|os\.Getenv\(|os\.LookupEnv\(|t\.Setenv\()([^)]*)`)
	// A read through a CONSTANT is still a read. envVarPattern matches the
	// variable's own shape, so cli.EnvVars(mcpURLEnvVar) is invisible to it — and
	// this gate then reported AGENTSMEMORY_MCP_URL as "documented but read by
	// nothing" the moment the reverse gate learned to see it. Both directions have
	// to resolve the indirection or they describe different sets and contradict
	// each other.
	constDecl := regexp.MustCompile(`(?m)^\s*([a-zA-Z_][A-Za-z0-9_]*)\s*=\s*"([A-Z][A-Z0-9_]+)"`)
	aliasDecl := regexp.MustCompile(`(?m)^\s*([a-zA-Z_][A-Za-z0-9_]*)\s*=\s*[a-zA-Z_][A-Za-z0-9_]*\.([A-Za-z0-9_]+)`)
	consts := map[string]string{}
	var aliases [][2]string
	bodies := map[string]string{}
	for _, path := range goFilesUnder(t, root) {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		bodies[path] = string(src)
		for _, m := range constDecl.FindAllStringSubmatch(bodies[path], -1) {
			consts[m[1]] = m[2]
		}
		for _, m := range aliasDecl.FindAllStringSubmatch(bodies[path], -1) {
			aliases = append(aliases, [2]string{m[1], m[2]})
		}
	}
	for _, a := range aliases {
		if name, ok := consts[a[1]]; ok {
			consts[a[0]] = name
		}
	}
	ident := regexp.MustCompile(`[a-zA-Z_][A-Za-z0-9_]*`)
	for _, body := range bodies {
		for _, m := range consult.FindAllStringSubmatch(body, -1) {
			for _, v := range envVarPattern.FindAllString(m[1], -1) {
				read[v] = true
			}
			for _, id := range ident.FindAllString(m[1], -1) {
				if name, ok := consts[id]; ok {
					read[name] = true
				}
			}
		}
	}
	return read
}

func goFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "vendor", "node_modules", "testdata", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

func dedupe(in []string) []string {
	seen, out := map[string]bool{}, []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// TestReadEnvVarsAreDocumented is TestDocumentedEnvVarsAreRead with the arrows
// swapped, and it is the direction that actually bit.
//
// The existing check catches a variable promised and never read — a line in an
// example file that does nothing. The reverse is the same defect and worse: a
// variable the program READS and nobody documents is a knob only its author
// knows exists, and the more consequential the knob the more it matters.
//
// It bit exactly there. SEARCH_SCOPE changes recall's default from every wing to
// the registration's wing — the one BREAKING behaviour change on this branch —
// and it appeared in two places: a cli.StringFlag and one tool description.
// EMBED_BACKEND, EMBED_URL and BM25_WEIGHT=auto-idf, none of which change
// existing behaviour, all got .env.example entries with rationale. The operator
// most likely to need SEARCH_SCOPE is the one whose recall just narrowed, and
// nothing they would read mentions it.
//
// Scoped to variables reachable through the CLI's own EnvVars declarations,
// because those are the ones a user is invited to set.

// notOperatorFacing lists variables the program reads that an operator is not
// expected to set, each with the reason. It is the escape hatch that keeps this
// check usable: without it the gate flags build-time and test affordances, and a
// gate that cries wolf is one people delete. An entry needs a reason, because
// the reason is the review.
var notOperatorFacing = map[string]string{
	"AIAGENTMEMORY_SERVER_BIN": "test/dev override for locating the server binary; not a deployment knob",
	"AIAGENTMEMORY_CODEX_BIN":  "test/dev override for locating the codex binary",
	"AIAGENTMEMORY_PI_BIN":     "test/dev override for locating the pi binary",
	"AIAGENTMEMORY_CLAUDE_BIN": "test/dev override for locating the claude binary",
	"AIAGENTMEMORY_VERSION":    "stamped at build time by the release workflow, not set by hand",
}

// TestNotOperatorFacingIsJustified keeps that list honest: an entry with no
// reason is unreviewed, and one naming a variable nothing reads any more is
// stale cover for whatever replaced it.
func TestNotOperatorFacingIsJustified(t *testing.T) {
	root := repoRoot(t)
	// Non-test sources only. goFilesUnder does not skip _test.go and the
	// notOperatorFacing literal lives in THIS file, so scanning everything made
	// every name match itself: adding a deliberately bogus variable that nothing
	// reads still passed. The half of this check that was supposed to catch a
	// stale entry could never fire.
	var all strings.Builder
	for _, path := range goFilesUnder(t, root) {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		if raw, err := os.ReadFile(path); err == nil {
			all.Write(raw)
		}
	}
	for v, why := range notOperatorFacing {
		if strings.TrimSpace(why) == "" {
			t.Errorf("notOperatorFacing[%q] has no reason", v)
		}
		if !strings.Contains(all.String(), v) {
			t.Errorf("notOperatorFacing names %q, which nothing reads any more", v)
		}
	}
}

func TestReadEnvVarsAreDocumented(t *testing.T) {
	root := repoRoot(t)

	read := map[string]string{} // var -> file that reads it
	// Two forms. A literal is the common one; a CONSTANT is the form that hid a
	// live defect — AGENTSMEMORY_MCP_URL is declared as mcpURLEnvVar, read at four
	// call sites, operator-facing, and documented nowhere. A regex requiring a
	// quote straight after EnvVars( could not see it, which is precisely the
	// "knob only its author knows about" this check exists for.
	envVarsLiteral := regexp.MustCompile(`EnvVars\("([A-Z][A-Z0-9_]+)"`)
	envVarsCall := regexp.MustCompile(`EnvVars\(([^)]*)\)`)
	constDecl := regexp.MustCompile(`(?m)^\s*([a-zA-Z_][A-Za-z0-9_]*)\s*=\s*"([A-Z][A-Z0-9_]+)"`)
	aliasDecl := regexp.MustCompile(`(?m)^\s*([a-zA-Z_][A-Za-z0-9_]*)\s*=\s*[a-zA-Z_][A-Za-z0-9_]*\.([A-Za-z0-9_]+)`)
	ident := regexp.MustCompile(`[a-zA-Z_][A-Za-z0-9_]*`)

	consts := map[string]string{} // identifier -> the variable name it holds
	var aliases [][2]string
	var sources []struct{ rel, body string }
	for _, path := range goFilesUnder(t, root) {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		body := string(src)
		sources = append(sources, struct{ rel, body string }{rel, body})
		for _, m := range constDecl.FindAllStringSubmatch(body, -1) {
			consts[m[1]] = m[2]
		}
		for _, m := range aliasDecl.FindAllStringSubmatch(body, -1) {
			aliases = append(aliases, [2]string{m[1], m[2]})
		}
	}
	for _, a := range aliases {
		if name, ok := consts[a[1]]; ok {
			consts[a[0]] = name
		}
	}
	for _, src := range sources {
		for _, m := range envVarsLiteral.FindAllStringSubmatch(src.body, -1) {
			if _, seen := read[m[1]]; !seen {
				read[m[1]] = src.rel
			}
		}
		for _, m := range envVarsCall.FindAllStringSubmatch(src.body, -1) {
			for _, id := range ident.FindAllString(m[1], -1) {
				name, ok := consts[id]
				if !ok {
					continue
				}
				if _, seen := read[name]; !seen {
					read[name] = src.rel
				}
			}
		}
	}
	if len(read) == 0 {
		t.Fatal("found no EnvVars declarations — this check has stopped checking anything")
	}

	// Where an operator would look. A variable named in any of these is
	// documented; it does not have to be in all of them.
	//
	// INSTALL.md, UPDATE.md and TROUBLESHOOTING.md joined when the install and
	// update prose moved out of the README. The corpus is a FIXED FILE LIST, so a
	// knob documented only in a file this list does not name is documented nowhere
	// as far as the check can tell, and it then demands the knob be re-documented
	// somewhere it already is. That is the §Reachability shape one level up: the
	// gate stays green while what it guards moves out from under it.
	//
	// ⚠ NAMED ONCE, BECAUSE IT WAS THREE COPIES. The slice, the comment and the
	// failure message all carried this set, and when the three files joined, only
	// two of the three moved — the message went on naming the pre-change four,
	// sending the next person to trip this gate to README.md for a knob whose
	// documentation had deliberately moved to INSTALL.md. That routes them into
	// re-creating the duplication the move removed. The message is derived below.
	operatorDocs := []string{
		".env.example", ".env.docker.example", "README.md", "AGENTS.md",
		"INSTALL.md", "UPDATE.md", "TROUBLESHOOTING.md",
	}
	var docs []string
	for _, rel := range operatorDocs {
		docs = append(docs, filepath.Join(root, rel))
	}
	composeFiles, _ := repohygiene.ComposeFiles(root)
	docs = append(docs, composeFiles...)

	var corpus strings.Builder
	for _, path := range docs {
		if raw, err := os.ReadFile(path); err == nil {
			corpus.Write(raw)
			corpus.WriteString("\n")
		}
	}
	text := corpus.String()

	names := make([]string, 0, len(read))
	for v := range read {
		if why, internal := notOperatorFacing[v]; internal {
			_ = why
			continue
		}
		names = append(names, v)
	}
	sort.Strings(names)
	// A promise, not a mention. strings.Contains counted prose ABOUT a variable —
	// including prose explaining that it deliberately does not exist — as
	// documenting it. The sibling direction already guards this by requiring
	// VAR=value; this requires the same, or a compose-style "VAR:" key.
	for _, v := range names {
		promised := regexp.MustCompile(`(?m)^\s*#?\s*` + regexp.QuoteMeta(v) + `\s*[:=]`)
		if !promised.MatchString(text) {
			t.Errorf("%s is read by %s and documented in none of %s, nor any compose file"+
				" — a knob only its author knows about",
				v, read[v], strings.Join(operatorDocs, ", "))
		}
	}
}
