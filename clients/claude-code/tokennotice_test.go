package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// hookNoticeFilter matches the line in a recall hook that drops the CLI's token
// notice, capturing the literal it greps for.
var hookNoticeFilter = regexp.MustCompile(`grep -v '\^([^']+)' "\$ERRFILE"`)

// TestTheHooksSkipTheNoticeTheCLIActuallyPrints couples the hooks' filter to the
// printf that produces the notice, through the RENDERED line and not through a
// second copy of the text. Review of #284 reworded the notice in mcpcall.go
// and the suite stayed green while both hooks went back to naming it as the
// cause; equality between two literals someone typed pins nothing, so this
// renders tokenNoticeFormat and requires each hook's literal to be a prefix of
// what the CLI writes.
func TestTheHooksSkipTheNoticeTheCLIActuallyPrints(t *testing.T) {
	rendered := strings.TrimSuffix(fmt.Sprintf(tokenNoticeFormat, "a loopback server, which needs none"), "\n")
	for _, name := range []string{"agentsmemory-recall-hook.sh", "agentsmemory-task-recall-hook.sh"} {
		body, err := os.ReadFile(filepath.Join("hooks", name))
		if err != nil {
			t.Fatal(err)
		}
		m := hookNoticeFilter.FindStringSubmatch(string(body))
		if m == nil {
			t.Errorf("%s no longer filters the token notice out of the cause line", name)
			continue
		}
		if !strings.HasPrefix(rendered, m[1]) {
			t.Errorf("%s filters %q, but the CLI prints %q — the notice would be reported as the cause again", name, m[1], rendered)
		}
	}
}
