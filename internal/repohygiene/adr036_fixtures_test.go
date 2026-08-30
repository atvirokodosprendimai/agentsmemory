package repohygiene

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// drawerIDPattern matches a palace drawer id: 64 lowercase hex characters. A
// triple id, a wing id and a memory id share the shape, which is convenient —
// one pattern covers every identifier that would let a reader join a committed
// fixture back to a private palace.
var drawerIDPattern = regexp.MustCompile(`\b[0-9a-f]{64}\b`)

// hashValuedKeys name the keys whose VALUE is legitimately 64 hex characters.
//
// A sha256 digest and a palace drawer id are the same shape, so a scan that
// cannot tell them apart either rejects the digest the manifest exists to carry
// or accepts the id it exists to exclude. This gate found that flaw in itself on
// its first real run — it reported its own placeholder digest as a leak — which
// is the useful kind of failure: shape is not identity, and the KEY is what
// settles which of the two a 64-hex token is.
var hashValuedKeys = map[string]bool{"corpus_sha256": true}

// manifestAllowedKeys is the closed set a redacted manifest may carry. It is a
// whitelist rather than a blacklist deliberately: a blacklist passes any key
// nobody thought to forbid, and the failure being prevented is precisely the one
// nobody thought of.
var manifestAllowedKeys = map[string]bool{
	"cases": true, "corpus_sha256": true, "provenance": true, "date": true,
	"calls": true, "output_tokens": true, "bytes": true, "tokenizer": true, "model_build": true,
}

// TestADR036FixturesCarryNoPrivatePalaceContent is ADR-036 T1's privacy gate.
//
// ADR-003 T2 carries a PERMANENT boundary: "Committing case files or full
// results JSON to the repo — they carry queries and drawer ids from a private
// palace; the cells file is the redacted record the evidence directory holds."
//
// ADR-036's first draft proposed committing a frozen case set whose questions
// came from real search_events rows, with gold triple ids, plus a real client
// transcript. That was written while FIXING an unrelated finding — a hermetic
// gate over a real-data requirement — and it would have walked straight through
// a boundary an accepted ADR had already closed. Nothing sweeps a `permanent`,
// so nothing would have resurfaced it, and in isolation the change read as an
// improvement in rigour.
//
// This test is the mechanical version of that boundary. It checks the files as
// GIT SEES THEM, not as the filesystem does: an untracked real corpus sitting in
// the working tree is exactly the intended state, and a gate that walked the
// directory would fail on the correct arrangement while passing on a staged one.
func TestADR036FixturesCarryNoPrivatePalaceContent(t *testing.T) {
	root := repoRoot(t)

	out, err := exec.Command("git", "-C", root, "ls-files", "internal/palace/testdata").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var fixtures []string
	for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		base := filepath.Base(p)
		if strings.HasPrefix(base, "factcases-") || strings.HasPrefix(base, "bootstrap-baseline-") {
			fixtures = append(fixtures, p)
		}
	}
	if len(fixtures) == 0 {
		t.Fatal("no tracked ADR-036 fixtures found — this gate has stopped checking anything")
	}

	sawSynthetic, sawManifest := false, false
	for _, rel := range fixtures {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}

		switch {
		case strings.HasSuffix(rel, ".jsonl"):
			sawSynthetic = true
			// A case file carries no legitimate 64-hex token at all, so the whole
			// body is scanned.
			if drawerIDPattern.MatchString(string(body)) {
				// Reported without the match: printing it would put the very
				// identifier this test exists to keep out of the repo into CI logs.
				t.Errorf("%s carries a 64-hex palace identifier — tracked fixtures must carry only redacted aggregates", rel)
			}
			checkEveryCaseIsMarkedSynthetic(t, rel, string(body))
		case strings.HasSuffix(rel, ".json"):
			sawManifest = true
			checkManifestCarriesOnlyAggregates(t, rel, body)
		}
	}
	if !sawSynthetic {
		t.Error("no tracked .jsonl fixture — the synthetic case set the hermetic fence runs against is missing")
	}
	if !sawManifest {
		t.Error("no tracked manifest — without it the real run has no auditable record at all")
	}
}

// checkEveryCaseIsMarkedSynthetic requires the marker on every row. A file whose
// first line is synthetic and whose twentieth is not is the realistic failure:
// someone appends real cases to a fixture that already passed.
func checkEveryCaseIsMarkedSynthetic(t *testing.T, rel, body string) {
	t.Helper()
	rows := 0
	for i, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		rows++
		var row struct {
			Synthetic bool `json:"synthetic"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Errorf("%s:%d: not valid JSON: %v", rel, i+1, err)
			continue
		}
		if !row.Synthetic {
			t.Errorf(`%s:%d: case is not marked "synthetic": true — a committed case set must be invented, not drawn from a palace`, rel, i+1)
		}
	}
	if rows == 0 {
		t.Errorf("%s: no case rows — an empty fixture satisfies every assertion above vacuously", rel)
	}
}

// checkManifestCarriesOnlyAggregates enforces the closed key set.
func checkManifestCarriesOnlyAggregates(t *testing.T, rel string, body []byte) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Errorf("%s: not valid JSON: %v", rel, err)
		return
	}
	if len(raw) == 0 {
		t.Errorf("%s: manifest is empty", rel)
		return
	}
	for k, v := range raw {
		if !manifestAllowedKeys[k] {
			t.Errorf("%s: key %q is not in the redacted-aggregate whitelist; a manifest carries counts, hashes, provenance and a tokenizer, never content", rel, k)
			continue
		}
		// Scan the VALUE for a palace identifier, exempting only the keys whose
		// value is meant to be a digest. Exempting by key rather than by shape is
		// what keeps "it looks like a hash" from becoming a way through.
		if hashValuedKeys[k] {
			continue
		}
		if drawerIDPattern.Match(v) {
			t.Errorf("%s: value of %q carries a 64-hex palace identifier", rel, k)
		}
	}
}
