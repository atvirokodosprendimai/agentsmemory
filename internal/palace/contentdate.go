package palace

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Content-date extraction, ported from the frozen miner's _extract_content_date
// for a TEXT payload. The "content date" is the date a memory is *about* (as
// opposed to filed_at, when it was ingested) — extracted from the text itself so
// recall can order memories by their real chronology. A server-side text payload
// has no filename or file mtime to consult, so only two of the frozen sources
// apply: YAML frontmatter, then the body's first lines. When neither yields a
// date the result is empty and the caller falls back to filed_at.

const contentDateBodyLines = 10 // frozen scans the body's first ~10 lines

// monthNumbers maps English month names and common abbreviations to their number.
var monthNumbers = map[string]int{
	"january": 1, "jan": 1, "february": 2, "feb": 2, "march": 3, "mar": 3,
	"april": 4, "apr": 4, "may": 5, "june": 6, "jun": 6, "july": 7, "jul": 7,
	"august": 8, "aug": 8, "september": 9, "sep": 9, "sept": 9, "october": 10,
	"oct": 10, "november": 11, "nov": 11, "december": 12, "dec": 12,
}

var monthAlt = `jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t)?(?:ember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?`

var (
	// isoDateRE matches YYYY-MM-DD with -, / or . separators (frozen ISO shape).
	isoDateRE = regexp.MustCompile(`\b(\d{4})[-/.](\d{1,2})[-/.](\d{1,2})\b`)
	// monthFirstRE: "Nov 8 2024" / "November 8th, 2024".
	monthFirstRE = regexp.MustCompile(`(?i)\b(` + monthAlt + `)\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+(\d{4})\b`)
	// dayFirstRE: "8 November 2024" / "8th Nov, 2024".
	dayFirstRE = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)?\s+(` + monthAlt + `)\.?,?\s+(\d{4})\b`)
	// slashRE: ambiguous numeric M/D/Y or D/M/Y with a 4-digit year (year last).
	slashRE = regexp.MustCompile(`\b(\d{1,2})[/-](\d{1,2})[/-](\d{4})\b`)
	// frontmatterKeyRE pulls a date/created/published value out of a YAML block.
	frontmatterKeyRE = regexp.MustCompile(`(?mi)^\s*(?:date|created|published)\s*:\s*(.+?)\s*$`)
)

// extractContentDate returns the content's date as YYYY-MM-DD, or "" when none is
// found. Precedence matches the frozen tool (minus the unavailable filename/mtime
// sources): YAML frontmatter first, then the body's first lines.
func extractContentDate(content string) string {
	body := content
	if fm, rest, ok := splitFrontmatter(content); ok {
		// Only a date/created/published field is trusted in frontmatter — not any
		// date that happens to appear in another key's value.
		if m := frontmatterKeyRE.FindStringSubmatch(fm); m != nil {
			if d := findDate(strings.TrimSpace(m[1])); d != "" {
				return d
			}
		}
		body = rest
	}
	// Only the first lines of the body are trusted to hold the memory's own date;
	// scanning the whole document would catch dates mentioned in passing.
	lines := strings.SplitN(body, "\n", contentDateBodyLines+1)
	if len(lines) > contentDateBodyLines {
		lines = lines[:contentDateBodyLines]
	}
	return findDate(strings.Join(lines, "\n"))
}

// splitFrontmatter returns the YAML frontmatter block and the body that follows
// when content opens with a `---` fence, else ok=false. The fence must be the very
// first thing in the content (frozen checks content.startswith("---")).
func splitFrontmatter(content string) (frontmatter, body string, ok bool) {
	if !strings.HasPrefix(content, "---\n") && content != "---" {
		return "", "", false
	}
	rest := content[len("---"):]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return "", "", false
	}
	frontmatter = rest[:idx]
	body = rest[idx+len("\n---"):]
	return frontmatter, body, true
}

// findDate searches text for the first date it can recognise and returns it as
// YYYY-MM-DD, or "" if none. ISO is tried first (most specific and unambiguous),
// then month-name forms, then an ambiguous numeric slash form. In frontmatter the
// text is a single field value; in the body it is the joined first lines.
func findDate(text string) string {
	if m := isoDateRE.FindStringSubmatch(text); m != nil {
		if d, ok := normalizeYMD(m[1], m[2], m[3]); ok {
			return d
		}
	}
	if m := monthFirstRE.FindStringSubmatch(text); m != nil {
		if d, ok := normalizeMonthName(m[3], m[1], m[2]); ok {
			return d
		}
	}
	if m := dayFirstRE.FindStringSubmatch(text); m != nil {
		if d, ok := normalizeMonthName(m[3], m[2], m[1]); ok {
			return d
		}
	}
	if m := slashRE.FindStringSubmatch(text); m != nil {
		// Ambiguous order: if the first field exceeds 12 it must be the day, so the
		// locale is D/M/Y; otherwise assume M/D/Y (the frozen disambiguation rule).
		a, _ := strconv.Atoi(m[1])
		if a > 12 {
			if d, ok := normalizeYMD(m[3], m[2], m[1]); ok {
				return d
			}
		} else if d, ok := normalizeYMD(m[3], m[1], m[2]); ok {
			return d
		}
	}
	return ""
}

// normalizeYMD validates numeric year/month/day strings and renders YYYY-MM-DD.
// It rejects impossible dates (e.g. month 13, day 32) by round-tripping through
// time.Date and confirming the components survived normalization unchanged.
func normalizeYMD(yStr, mStr, dStr string) (string, bool) {
	y, _ := strconv.Atoi(yStr)
	m, _ := strconv.Atoi(mStr)
	d, _ := strconv.Atoi(dStr)
	return validDate(y, m, d)
}

// normalizeMonthName resolves a month name to its number, then validates as YMD.
func normalizeMonthName(yStr, monthName, dStr string) (string, bool) {
	m, ok := monthNumbers[strings.ToLower(monthName)]
	if !ok {
		return "", false
	}
	y, _ := strconv.Atoi(yStr)
	d, _ := strconv.Atoi(dStr)
	return validDate(y, m, d)
}

// validDate confirms (y, m, d) is a real calendar date and returns it formatted.
// time.Date normalizes overflow (Feb 30 -> Mar 2), so a date is valid only if the
// constructed time reports back the same year, month and day it was given.
func validDate(y, m, d int) (string, bool) {
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return "", false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != m || t.Day() != d {
		return "", false
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d), true
}
