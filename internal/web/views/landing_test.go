package views

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestLandingPageLinksClaudeGuide guards the two additions the landing page must
// carry: a visible link to /claude-guide, and the copy-paste "let Claude install
// it" prompt (with its independent _copiedPrompt copy signal). Rendering the whole
// page verifies the markup an anonymous visitor actually receives.
func TestLandingPageLinksClaudeGuide(t *testing.T) {
	var buf bytes.Buffer
	if err := LandingPage(LandingData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, `href="/claude-guide"`) {
		t.Error("landing page has no link to /claude-guide")
	}
	if !strings.Contains(html, "prompt for Claude") {
		t.Error("landing page is missing the copy-paste prompt block")
	}
	// The prompt itself must point the agent at the guide.
	if !strings.Contains(html, claudeGuideURL) {
		t.Errorf("prompt does not reference the guide URL %q", claudeGuideURL)
	}
	// The prompt's Copy button must use its own signal, not the install one-liner's.
	if !strings.Contains(html, "_copiedPrompt") {
		t.Error("prompt copy button is missing its independent _copiedPrompt signal")
	}
}

// TestLandingPageArguesCost guards the pricing-transparency band: the page must
// argue *why* it costs (real GPU compute + electricity for embeddings), and the
// same argument must also reach the schema.org FAQ so AI answer engines can cite
// it. Rendering the whole page verifies what an anonymous visitor receives.
func TestLandingPageArguesCost(t *testing.T) {
	var buf bytes.Buffer
	if err := LandingPage(LandingData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	// The cost-rationale band and its argument must be on the page.
	if !strings.Contains(html, "Why it costs") {
		t.Error("landing page is missing the 'Why it costs' band")
	}
	if !strings.Contains(html, "GPU") {
		t.Error("cost argument does not mention the GPU compute cost")
	}
	if !strings.Contains(html, "bge-m3") {
		t.Error("cost argument does not name the embedding model that drives the cost")
	}

	// The same argument must feed the FAQ — which double-feeds the JSON-LD — so
	// the reasoning is citable, not just visible.
	if !strings.Contains(html, "Why does agent memory cost money?") {
		t.Error("landing FAQ is missing the 'why it costs' question (GEO surface)")
	}
	// json.Marshal HTML-escapes the ampersand in the FAQ JSON-LD, proving the same
	// question also rendered inside the schema.org <script>.
	if !strings.Contains(html, "application/ld+json") ||
		!strings.Contains(html, "Why does agent memory cost money?") {
		t.Error("why-it-costs FAQ did not reach the schema.org JSON-LD")
	}
}
