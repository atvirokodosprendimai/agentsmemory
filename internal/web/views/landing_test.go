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
