package palace

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// recordingVectors notes the k every vector search asks for, so a test can see
// how deep an arm actually fetched rather than how deep it says it does.
type recordingVectors struct {
	store.VectorStore
	ks []int
}

func (r *recordingVectors) Search(ctx context.Context, ns string, vec []float32, k int, f store.Filter) (store.SearchResult, error) {
	r.ks = append(r.ks, k)
	return r.VectorStore.Search(ctx, ns, vec, k, f)
}

// TestProductionDeepArmActuallyFetchesDeeper is the SELECTION test for the depth
// arm, and it exists because the obvious test does not catch the obvious bug.
//
// TestProductionArmsAskForDifferentDepths drives productionLimit and passes
// whether or not the Search call site uses it. Replacing `Limit:
// productionLimit(arm)` with `Limit: DefaultSearchLimit` — which is what the code
// said before this arm existed, so it is exactly what a careless revert
// reintroduces — left the whole package green: the helper was correct and
// unreached, and the table grew a second production row identical to the first
// under a name claiming a depth it never requested.
//
// So this follows the depth all the way through. Service.Search derives its
// candidate pool from the requested limit (candidateK = limit*multiplier with no
// reranker configured), so a recording vector store sees one k per arm, and the
// two arms must produce two different ones.
func TestProductionDeepArmActuallyFetchesDeeper(t *testing.T) {
	svc := newTestService(t)
	const team = "team-depth"

	// Enough drawers that a page of ten is a different page from a page of five.
	var gold string
	for i := range 12 {
		created := mustAddOne(t, svc, team, AddInput{
			Wing: "w", Room: "r",
			Content: fmt.Sprintf("memory number %d about retrieval depth and paging", i),
		})
		if i == 0 {
			gold = created.ID
		}
	}

	rec := &recordingVectors{VectorStore: svc.vectors}
	svc.vectors = rec

	if _, err := svc.Evaluate(t.Context(), team,
		[]EvalCase{{Query: "retrieval depth and paging", Expect: gold}}, 50, nil); err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	want := map[int]bool{
		DefaultSearchLimit * hybridCandidateMultiplier:  false,
		productionDeepLimit * hybridCandidateMultiplier: false,
	}
	for _, k := range rec.ks {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for k, saw := range want {
		if !saw {
			t.Errorf("no vector search asked for k=%d; the production arms fetched %v. An arm that "+
				"asks for the same depth as its sibling is a duplicate row under a misleading name",
				k, rec.ks)
		}
	}
}

// TestAbstentionCalibrationComesFromTheDefaultPage.
//
// The abstention gate (ADR-001) calibrates on the production path's top-1 rerank
// score, and production serves DefaultSearchLimit. The deeper arm runs the same
// pipeline over a wider candidate pool, so its top-1 can be a document the
// default page never contained — calibrating on it would set a threshold for a
// search nobody runs.
//
// The structural half of this is already impossible to get wrong by accident:
// the per-arm values are keyed by arm rather than written to a shared pair of
// variables, so the deeper arm cannot overwrite the default's number and no
// reordering of the arms list can change which one wins. What remains is the
// deliberate choice of WHICH key to read, and this pins it.
//
// It is a source check and not a behavioural one, which is worth stating rather
// than hiding: making the two arms disagree on their top-1 needs a contrived
// corpus (a document ranked highest by the cross-encoder that sits outside the
// shallower arm's candidate pool but inside the deeper one), and a test that
// usually compares two equal numbers is a test that usually cannot fail.
func TestAbstentionCalibrationComesFromTheDefaultPage(t *testing.T) {
	src, err := os.ReadFile("eval.go")
	if err != nil {
		t.Fatalf("read eval.go: %v", err)
	}
	body := string(src)

	// EVERY assignment, not just the first. The original check read only the
	// first occurrence, which silently assumed there would only ever be one —
	// and the day a second appeared, the check began inspecting the new line and
	// ignoring the one it was written to protect. A gate that stops watching its
	// target the moment the file grows is worse than no gate, because it still
	// reports green.
	var lines []string
	for off := 0; ; {
		i := strings.Index(body[off:], "TopRerank:")
		if i < 0 {
			break
		}
		i += off
		line := body[i:]
		if j := strings.IndexByte(line, '\n'); j >= 0 {
			line = line[:j]
		}
		lines = append(lines, strings.TrimSpace(line))
		off = i + len("TopRerank:")
	}
	if len(lines) == 0 {
		t.Fatal("no TopRerank assignment in eval.go — this check has stopped checking anything")
	}
	for _, line := range lines {
		// Two forms are legitimate: reading the served arm directly, or forwarding
		// the value already taken from it. Anything else names another arm.
		direct := strings.Contains(line, "prodTops[ArmProduction]")
		forwarded := strings.Contains(line, "oc.TopRerank") || strings.Contains(line, "topRerank")
		if !direct && !forwarded {
			t.Errorf("the abstention gate is calibrated from %q. It must read prodTops[ArmProduction] — "+
				"the page production actually serves — or forward the value taken from it, not any other arm", line)
		}
		if strings.Contains(line, "ArmProductionDeep") {
			t.Error("calibrating the abstention gate on the deeper arm sets a threshold for a page " +
				"size production never returns")
		}
	}
	// The forwarding forms above are only safe while the value they forward is
	// itself taken from the served arm, so that origin is pinned separately —
	// otherwise "forwarded" would be a hole any arm could be poured through.
	if !strings.Contains(body, "TopRerank:   prodTops[ArmProduction].rerank") &&
		!strings.Contains(body, "TopRerank:  prodTops[ArmProduction].rerank") &&
		!strings.Contains(body, "TopRerank: prodTops[ArmProduction].rerank") {
		t.Error("no assignment takes TopRerank from prodTops[ArmProduction].rerank — the forwarded " +
			"values above now have no verified origin")
	}
}
