package palace

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string // expected cleaned value when ok
		wantErr bool
	}{
		{"simple", "effective-go", "effective-go", false},
		{"single char", "x", "x", false},
		{"interior underscore (default wing)", "wing_claude", "wing_claude", false},
		{"spaces and dots", "Project One v1.2", "Project One v1.2", false},
		{"trims surrounding space", "  claude  ", "claude", false},
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"path traversal dotdot", "../etc", "", true},
		{"forward slash", "a/b", "", true},
		{"backslash", `a\b`, "", true},
		{"null byte", "a\x00b", "", true},
		{"leading underscore", "_lead", "", true},
		{"trailing hyphen", "trail-", "", true},
		{"too long", strings.Repeat("a", MaxNameLength+1), "", true},
		{"at limit", strings.Repeat("a", MaxNameLength), strings.Repeat("a", MaxNameLength), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SanitizeName(tc.in, "field")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got value %q", got)
				}
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("error should wrap ErrInvalidInput, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeContent(t *testing.T) {
	// Verbatim content is preserved exactly — surrounding whitespace is NOT
	// trimmed, only validated, because a drawer stores the memory as written.
	if got, err := SanitizeContent("  keep me as-is  "); err != nil || got != "  keep me as-is  " {
		t.Fatalf("content should be returned verbatim: got %q err %v", got, err)
	}
	for _, bad := range []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"null byte", "has\x00null"},
		{"too long", strings.Repeat("a", MaxContentLength+1)},
	} {
		t.Run(bad.name, func(t *testing.T) {
			if _, err := SanitizeContent(bad.in); err == nil {
				t.Fatalf("want error for %s", bad.name)
			} else if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error should wrap ErrInvalidInput, got %v", err)
			}
		})
	}
}
