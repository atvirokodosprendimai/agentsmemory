package longmemeval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const samplePath = "testdata/longmemeval_s_sample.json"

func TestDatasetLoadsEverySixQuestionTypes(t *testing.T) {
	ds, err := Load(samplePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]bool{
		TypeSingleSessionUser:       false,
		TypeSingleSessionAssistant:  false,
		TypeSingleSessionPreference: false,
		TypeTemporalReasoning:       false,
		TypeKnowledgeUpdate:         false,
		TypeMultiSession:            false,
	}
	for _, q := range ds.Questions {
		if _, ok := want[q.Type]; !ok {
			t.Fatalf("question %q carries unknown type %q", q.ID, q.Type)
		}
		want[q.Type] = true
	}
	for typ, seen := range want {
		if !seen {
			t.Errorf("the fixture must exercise %q and does not", typ)
		}
	}

	// has_answer is the label the retrieval-only column is computed from, so a
	// loader that drops it silently costs a whole metric.
	var answering int
	for _, q := range ds.Questions {
		for _, s := range q.Haystack {
			for _, turn := range s.Turns {
				if turn.HasAnswer {
					answering++
				}
			}
		}
	}
	if answering == 0 {
		t.Error("no turn survived with HasAnswer set; the retrieval-only column would be blind")
	}
}

func TestDatasetPairsEverySessionWithItsOwnDate(t *testing.T) {
	ds, err := Load(samplePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	q := questionByID(t, ds, "q_update_1")
	if got, want := q.Haystack[0].ID, "s5"; got != want {
		t.Errorf("first session id = %q, want %q", got, want)
	}
	if got, want := q.Haystack[1].Date, "2023-05-20"; got != want {
		t.Errorf("second session date = %q, want %q — the three arrays are zipped by position", got, want)
	}
}

func TestDatasetRejectsMisalignedHaystackArrays(t *testing.T) {
	raw := readSample(t)
	raw[0]["haystack_dates"] = []string{"2023-05-01"} // one date, two sessions

	_, err := Load(writeTemp(t, raw))
	if err == nil {
		t.Fatal("a question whose ids, dates and sessions disagree in length must fail loudly; " +
			"zipping short dates a session from its neighbour and every temporal question is then wrong")
	}
	if !strings.Contains(err.Error(), "q_user_1") {
		t.Errorf("the error must name the offending question, got: %v", err)
	}
}

func TestDatasetRejectsAQuestionWhoseGoldSessionIsNotInItsHaystack(t *testing.T) {
	raw := readSample(t)
	raw[0]["answer_session_ids"] = []string{"s_nowhere"}

	_, err := Load(writeTemp(t, raw))
	if err == nil {
		t.Fatal("a gold session absent from the haystack scores zero for every policy equally, " +
			"which reads as 'no policy helps' rather than as a broken row; it must be an error")
	}
	if !strings.Contains(err.Error(), "s_nowhere") {
		t.Errorf("the error must name the missing session, got: %v", err)
	}
}

func TestDatasetRecordsItsFileDigest(t *testing.T) {
	ds, err := Load(samplePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ds.SHA256) != 64 {
		t.Fatalf("SHA256 = %q, want 64 hex characters — the results file names the exact corpus", ds.SHA256)
	}
	again, err := Load(samplePath)
	if err != nil {
		t.Fatalf("Load (second): %v", err)
	}
	if ds.SHA256 != again.SHA256 {
		t.Error("the digest of one unchanged file must not move between loads")
	}
}

func questionByID(t *testing.T, ds Dataset, id string) Question {
	t.Helper()
	for _, q := range ds.Questions {
		if q.ID == id {
			return q
		}
	}
	t.Fatalf("fixture has no question %q", id)
	return Question{}
}

func readSample(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return raw
}

func writeTemp(t *testing.T, raw []map[string]any) string {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(t.TempDir(), "mutated.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}
