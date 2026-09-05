package mcpcli

import (
	"strings"
	"testing"
)

// The measured page shape (ADR-058 Context, 2026-09-05): hits that are 88k-char
// session transcripts, a header-line identity whose first region duplicates it,
// and facts. The digest must render whole hits inside its budget, prefer the
// first region NOT contained in the identity, render facts one per line, and
// say how many hits it withheld.
func fixturePage() string {
	big := strings.Repeat("x", 88000)
	return `{"count":3,"search_id":"s1","hits":[` +
		`{"identity":"SESSION:2026-08-23|PROJ:agentmemories|TASK:redeploy","wing":"wing_alpha","room":"diary","content_date":"2026-08-23","content":"` + big + `",` +
		`"regions":[{"start":0,"text":"SESSION:2026-08-23|PROJ:agentmemories|TASK:redeploy"},{"start":360,"text":"rolls the host and deploy.yml auto-rolls-back on a failed /healthz"}],` +
		`"score":0.1,"bm25_score":2.1,"rerank_score":0.25,"uri":"agentsmemory://x"},` +
		`{"identity":"WHY DOES doctor RUN THE REGISTRATION'S OWN ENVIRONMENT?","wing":"wing_alpha","room":"decisions","stale":true,"content":"` + big + `",` +
		`"regions":[{"start":626,"text":"doctor RAN A RECONSTRUCTION, NOT THE REGISTRATION"}],"score":0.1},` +
		`{"identity":"a third memory with no regions at all","wing":"wing_beta","room":"gotchas","content":"` + big + `","score":0.05}` +
		`],"facts":[{"subject":"the local stack","predicate":"serves_version","object":"v0.0.118"},{"subject":"ADR-057","predicate":"completed_on","object":"2026-09-05"}]}`
}

func TestTheDigestFitsItsBudget(t *testing.T) {
	page := fixturePage()
	if len(page) < 6000 {
		t.Fatalf("the fixture must be the measured shape (~6k+ of JSON); it is %d bytes", len(page))
	}
	out := RenderDigest([]byte(page), "redeploy the host", 1600)

	if n := len(out); n > 1600 {
		t.Fatalf("digest is %d chars, over the 1600 budget:\n%s", n, out)
	}
	// Hit 1 is whole: identity, wing/room/date, and the region that is NOT the identity.
	for _, want := range []string{
		"SESSION:2026-08-23|PROJ:agentmemories|TASK:redeploy",
		"wing_alpha/diary 2026-08-23",
		"rolls the host and deploy.yml auto-rolls-back",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("digest lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "SESSION:2026-08-23|PROJ:agentmemories|TASK:redeploy") != 1 {
		t.Errorf("the content line reprinted the identity line (regions[0] contained in identity must be skipped):\n%s", out)
	}
	// Hit 2 carries STALE and its only region.
	if !strings.Contains(out, "STALE") || !strings.Contains(out, "doctor RAN A RECONSTRUCTION") {
		t.Errorf("hit 2 is not whole, or STALE is missing:\n%s", out)
	}
	// Facts: one line each.
	for _, want := range []string{"the local stack → serves_version → v0.0.118", "ADR-057 → completed_on → 2026-09-05"} {
		if !strings.Contains(out, want) {
			t.Errorf("fact not rendered as one line %q:\n%s", want, out)
		}
	}
	// No raw JSON leaked.
	for _, leak := range []string{`"rerank_score"`, `"uri"`, `"bm25_score"`, "xxxxxxxxxx"} {
		if strings.Contains(out, leak) {
			t.Errorf("digest leaks raw page content %q", leak)
		}
	}

	// A budget that fits only the first hit withholds the rest and says so.
	small := RenderDigest([]byte(page), "redeploy the host", 300)
	if len(small) > 300 {
		t.Fatalf("small digest is %d chars, over 300:\n%s", len(small), small)
	}
	if !strings.Contains(small, "SESSION:2026-08-23") || strings.Contains(small, "doctor RAN") {
		t.Errorf("the small budget should hold hit 1 whole and withhold hit 2:\n%s", small)
	}
	if !strings.Contains(small, "2 more hit(s) withheld by the budget") || !strings.Contains(small, `am_search "redeploy the host"`) {
		t.Errorf("the withheld line is missing or does not name the count and query:\n%s", small)
	}
	// A hit is never cut mid-line: every line of the small digest is a full line of the large one.
	for _, line := range strings.Split(strings.TrimSpace(small), "\n") {
		if strings.HasPrefix(line, "2 more hit") {
			continue
		}
		if !strings.Contains(out, line) {
			t.Errorf("small digest carries a line the full digest does not — a hit was cut: %q", line)
		}
	}
}
