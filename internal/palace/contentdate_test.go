package palace

import "testing"

func TestExtractContentDate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"frontmatter date", "---\ntitle: x\ndate: 2024-11-08\n---\nbody here", "2024-11-08"},
		{"frontmatter created wins over body", "---\ncreated: 2023-01-02\n---\nwritten 2024-12-31", "2023-01-02"},
		{"iso in body", "# Notes\nlogged on 2024/06/15 today", "2024-06-15"},
		{"dotted iso", "stamp 2024.03.09 ok", "2024-03-09"},
		{"month name first", "Met on November 8, 2024 downtown", "2024-11-08"},
		{"abbrev month with ordinal", "due Apr 6th 2011 sharp", "2011-04-06"},
		{"day first", "filed 8 November 2024 late", "2024-11-08"},
		{"slash M/D/Y", "on 6/15/2024 we shipped", "2024-06-15"},
		{"slash D/M/Y when first>12", "on 15/06/2024 we shipped", "2024-06-15"},
		{"invalid month rejected", "not a date 2024-13-40 here", ""},
		{"no date", "just some prose with no dates", ""},
		{"only beyond first lines ignored", "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11 2024-11-08", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractContentDate(tc.in); got != tc.want {
				t.Fatalf("extractContentDate(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
