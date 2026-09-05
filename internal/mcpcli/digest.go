package mcpcli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// digestPage is the slice of an am_search page the digest reads. Every field
// is one the server already emits; the digest selects, it does not compute.
type digestPage struct {
	Hits []struct {
		Identity    string `json:"identity"`
		Wing        string `json:"wing"`
		Room        string `json:"room"`
		ContentDate string `json:"content_date"`
		Stale       bool   `json:"stale"`
		Content     string `json:"content"`
		Regions     []struct {
			Text string `json:"text"`
		} `json:"regions"`
	} `json:"hits"`
	Facts []struct {
		Subject   string `json:"subject"`
		Predicate string `json:"predicate"`
		Object    string `json:"object"`
	} `json:"facts"`
}

// RenderDigest renders an am_search page as bounded plain text for injection
// into a model's context (ADR-058): for each hit in the server's order, three
// lines — identity, wing/room with date and STALE, and one matched region —
// then one line per fact, then, when the budget withheld hits, a line saying
// how many and how to get them. A hit is whole or withheld, never cut, and
// withholding drops from the END, so the server's ranking is kept.
//
// It exists because the hook used to inject the page verbatim: measured
// 2026-09-05, 5,877 bytes of JSON for two hits that were 88k-character
// transcripts carrying 24 keys each, on every prompt. The region chosen is the
// first NOT already contained in the identity line — this palace's diary
// memories open with a SESSION:|PROJ:|TASK: header, and on half the sampled
// hits regions[0] WAS that header, so the obvious choice spent the one content
// line reprinting the line above it (review of #268).
func RenderDigest(page []byte, query string, budget int) string {
	var p digestPage
	if err := json.Unmarshal(page, &p); err != nil {
		// Not a page: hand the text through untouched rather than lose a tool
		// message, bounded the same way.
		return truncateToBudget(string(page), budget)
	}
	var b strings.Builder
	withheld := 0
	for _, h := range p.Hits {
		block := renderHit(h.Identity, h.Wing, h.Room, h.ContentDate, h.Stale, h.Content, regionTexts(h))
		if withheld > 0 || b.Len()+len(block)+withheldLineReserve(query) > budget {
			withheld++
			continue
		}
		b.WriteString(block)
	}
	for _, f := range p.Facts {
		line := f.Subject + " → " + f.Predicate + " → " + f.Object + "\n"
		if b.Len()+len(line)+withheldLineReserve(query) > budget {
			break
		}
		b.WriteString(line)
	}
	if withheld > 0 {
		b.WriteString(withheldLine(withheld, query))
	}
	return b.String()
}

func regionTexts(h struct {
	Identity    string `json:"identity"`
	Wing        string `json:"wing"`
	Room        string `json:"room"`
	ContentDate string `json:"content_date"`
	Stale       bool   `json:"stale"`
	Content     string `json:"content"`
	Regions     []struct {
		Text string `json:"text"`
	} `json:"regions"`
}) []string {
	out := make([]string, 0, len(h.Regions))
	for _, r := range h.Regions {
		out = append(out, r.Text)
	}
	return out
}

// renderHit is one hit's three lines. The content line is the first region
// whose text is not contained in the identity; when every region is, the
// first region; when there are none, the line is omitted rather than filled
// from the raw content, which is what the budget exists to keep out.
func renderHit(identity, wing, room, date string, stale bool, content string, regions []string) string {
	if identity == "" {
		identity = firstLine(content, 120)
	}
	where := wing + "/" + room
	if date != "" {
		where += " " + date
	}
	if stale {
		where += " STALE"
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(identity) + "\n")
	b.WriteString("  " + where + "\n")
	if line := contentLine(identity, regions); line != "" {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

// contentLine picks the region a reader has not already seen in the identity.
func contentLine(identity string, regions []string) string {
	id := strings.TrimSpace(identity)
	for _, r := range regions {
		r = strings.TrimSpace(strings.ReplaceAll(r, "\n", " "))
		if r == "" || strings.Contains(id, r) {
			continue
		}
		return r
	}
	if len(regions) > 0 {
		return strings.TrimSpace(strings.ReplaceAll(regions[0], "\n", " "))
	}
	return ""
}

// withheldLine names the count and the query. The query is CAPPED: the hook
// asks with the whole prompt, and on 2026-09-05 a 280-character prompt came
// back verbatim in this line — the budget line spending the budget. Sixty
// characters is enough to recognise the search and paste it back.
func withheldLine(n int, query string) string {
	q := []rune(strings.TrimSpace(strings.Join(strings.Fields(query), " ")))
	if len(q) > 60 {
		q = append(q[:60], '…')
	}
	return fmt.Sprintf("%d more hit(s) withheld by the budget; am_search %q for the rest\n", n, string(q))
}

// withheldLineReserve keeps room for the trailing line so a hit that fits
// alone cannot push the line that explains the others past the budget.
func withheldLineReserve(query string) int {
	return len(withheldLine(99, query))
}

func truncateToBudget(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	return s[:budget]
}
