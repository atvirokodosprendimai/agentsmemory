// verify.go implements `aiagentmemory verify`: the half of staleness detection
// that has to run where the code is.
//
// A memory that explains code is the most valuable kind — it carries the WHY the
// code itself never states — and the only kind that can go quietly wrong. The
// code gets fixed, the sentence does not, and the next session recalls it with
// full confidence. Nothing in the palace can notice on its own: the server
// usually runs in a container and has never seen the repository.
//
// So the split is deliberate. The server holds the anchors (file + verbatim
// snippet) and the verdicts; this command reads the working tree, decides which
// anchors still match, and posts the answers back. It needs no parser and no
// index: the snippet is either still in the file or it is not.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/anchorcontract"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/urfave/cli/v3"
)

// anchor is one pin as the server hands it out.
type anchor struct {
	ID       string `json:"id"`
	DrawerID string `json:"drawer_id"`
	Repo     string `json:"repo"`
	Path     string `json:"path"`
	Snippet  string `json:"snippet"`
	Status   string `json:"status"`
}

// verdict is what we send back.
type verdict struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Line   int    `json:"line,omitempty"`
}

// Verdict values, mirroring internal/palace's anchor statuses.
const (
	statusVerified = "verified"
	statusDrifted  = "drifted"
	statusMissing  = "missing"
)

// verifyCommand builds `verify`.
func verifyCommand() *cli.Command {
	return &cli.Command{
		Name:  "verify",
		Usage: "check that memories still match the code they were written about",
		Description: "Reads the code anchors filed with this project's memories and checks each\n" +
			"verbatim snippet against the working tree, then records the verdicts.\n\n" +
			"A memory whose snippet has vanished is marked DRIFTED, and search says so\n" +
			"on every later recall — which is the difference between remembering and\n" +
			"misleading. Run it after a refactor, or from a session-start hook.\n\n" +
			"The wing defaults to $" + wingEnvVar + " or the nearest " + projectConfigFile + ".",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "wing", Usage: "verify anchors on drawers in this wing (default: the project's wing)"},
			&cli.StringFlag{Name: "repo", Usage: "verify anchors carrying this repo label"},
			&cli.StringFlag{Name: "root", Usage: "repository root the paths are relative to (default: the working directory)"},
			&cli.StringFlag{Name: "mcp-url", Sources: cli.EnvVars(mcpURLEnvVar), Value: defaultMCPURL, Usage: "agentsmemory MCP endpoint"},
			&cli.StringFlag{Name: "token", Sources: cli.EnvVars(tokenEnvVar), Usage: "workspace token (a --local server needs none)"},
			&cli.BoolFlag{Name: "dry-run", Usage: "report what changed without recording any verdict"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runVerify(ctx, c, os.Stdout)
		},
	}
}

// runVerify is the whole flow: fetch anchors, check them on disk, post verdicts.
func runVerify(ctx context.Context, c *cli.Command, out io.Writer) error {
	root := c.String("root")
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determine the repository root: %w", err)
		}
		root = wd
	}
	wing := c.String("wing")
	if wing == "" {
		wing = resolveProjectWing(root)
	}

	cli, err := dialMCP(ctx, c.String("mcp-url"), c.String("token"), 30*time.Second)
	if err != nil {
		return err
	}
	defer cli.Close()

	anchors, err := listAnchors(ctx, cli, wing, c.String("repo"))
	if err != nil {
		return err
	}
	if len(anchors) == 0 {
		fmt.Fprintf(out, "no code anchors filed%s — nothing to verify.\n", wingLabel(wing))
		fmt.Fprintf(out, "Anchor a memory by passing code_anchors to am_add_drawer: the file and the verbatim lines it is about.\n")
		return nil
	}

	// Read each file once: several memories usually pin the same file, and a
	// re-read per anchor turns a fast check into a slow one on a large palace.
	verdicts, counts := verifyAnchors(root, anchors, out)
	here := currentRepoLabel(root)
	drifted, missing, verified, elsewhere := counts.drifted, counts.missing, counts.verified, counts.elsewhere
	unattributable := counts.unattributable
	_ = verified

	if elsewhere > 0 {
		if here == "" {
			fmt.Fprintf(out, "  %d anchor(s) name a repository and this tree does not name itself (no git remote), "+
				"so a file not found here is not evidence the memory is stale — left unrecorded\n", elsewhere)
		} else {
			fmt.Fprintf(out, "  %d anchor(s) belong to another repository and were not checked from here (this tree is %q)\n", elsewhere, here)
		}
	}
	if c.Bool("dry-run") {
		fmt.Fprintf(out, "\n%d anchor(s)%s: %d verified, %d drifted, %d missing, %d elsewhere, %d unlabelled (dry run — nothing recorded)\n",
			len(anchors), wingLabel(wing), verified, drifted, missing, elsewhere, unattributable)
		return nil
	}
	marked, err := markAnchors(ctx, cli, verdicts)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d anchor(s)%s: %d verified, %d drifted, %d missing, %d elsewhere, %d unlabelled — %d verdict(s) recorded\n",
		len(anchors), wingLabel(wing), verified, drifted, missing, elsewhere, unattributable, marked)
	if drifted+missing > 0 {
		fmt.Fprintf(out, "Search now flags those memories as STALE. Re-read the code and re-file whichever are wrong.\n")
	}
	return nil
}

// sourceFile is one file's contents, read once and reused across the anchors that
// point at it.
type sourceFile struct {
	exists     bool
	lines      []string
	normalized []string // whitespace-collapsed, for matching
}

// readSource loads a file, tolerating absence — a deleted file is a verdict, not
// an error.
func readSource(path string) *sourceFile {
	raw, err := os.ReadFile(path)
	if err != nil {
		return &sourceFile{}
	}
	lines := strings.Split(string(raw), "\n")
	norm := make([]string, len(lines))
	for i, l := range lines {
		norm[i] = anchorcontract.NormalizeSnippet(l)
	}
	return &sourceFile{exists: true, lines: lines, normalized: norm}
}

// find reports whether the snippet is still in the file, and on which line it
// starts (1-based).
//
// Matching is whitespace-normalized and line-by-line so a re-indent, a gofmt, or
// a wrapped argument list does not read as drift — the flag is worthless if it
// fires on formatting. A single-line snippet matches a substring of a line, which
// is what makes an anchor on one distinctive expression survive edits around it.
func (s *sourceFile) find(snippet string) (int, bool) {
	want := strings.Split(strings.TrimSpace(snippet), "\n")
	var norm []string
	for _, w := range want {
		if n := anchorcontract.NormalizeSnippet(w); n != "" {
			norm = append(norm, n)
		}
	}
	if len(norm) == 0 {
		return 0, false
	}
	for i := range s.normalized {
		if !strings.Contains(s.normalized[i], norm[0]) {
			continue
		}
		if len(norm) == 1 {
			return i + 1, true
		}
		// Multi-line: the remaining lines must follow, in order, allowing blank
		// lines between them so an inserted newline is not drift.
		j, matched := i+1, 1
		for j < len(s.normalized) && matched < len(norm) {
			if s.normalized[j] == "" {
				j++
				continue
			}
			if !strings.Contains(s.normalized[j], norm[matched]) {
				break
			}
			matched++
			j++
		}
		if matched == len(norm) {
			return i + 1, true
		}
	}
	return 0, false
}

// listAnchors fetches the anchors to check.
func listAnchors(ctx context.Context, c mcpCaller, wing, repo string) ([]anchor, error) {
	args := map[string]any{"limit": 500}
	if wing != "" {
		args["wing"] = wing
	}
	if repo != "" {
		args["repo"] = repo
	}
	var payload struct {
		Anchors []anchor `json:"anchors"`
	}
	if err := mcpcli.DecodeJSON(ctx, c.CallTool, "list_anchors", args, &payload); err != nil {
		return nil, err
	}
	return payload.Anchors, nil
}

// markAnchors posts the verdicts and returns how many the server recorded.
func markAnchors(ctx context.Context, c mcpCaller, verdicts []verdict) (int, error) {
	items := make([]any, 0, len(verdicts))
	for _, v := range verdicts {
		items = append(items, map[string]any{"id": v.ID, "status": v.Status, "line": v.Line})
	}
	var payload struct {
		Marked int `json:"marked"`
	}
	if err := mcpcli.DecodeJSON(ctx, c.CallTool, "mark_anchors", map[string]any{"verdicts": items}, &payload); err != nil {
		return 0, err
	}
	return payload.Marked, nil
}

// mcpCaller is the slice of the MCP client this file needs, declared here so the
// flow is testable against a fake without a server.
type mcpCaller interface {
	CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// resolveProjectWing finds the wing for a directory the same way `load` does, so
// `verify` checks this project's memories without being told which they are.
func resolveProjectWing(dir string) string {
	if w := strings.TrimSpace(os.Getenv(wingEnvVar)); w != "" {
		return w
	}
	shared, local, _ := findProjectConfig(dir)
	return firstNonEmpty(local.wing, shared.wing)
}

// currentRepoLabel names the repository the working tree belongs to, using the
// same rule anchors are labelled with: the git remote's basename, or the
// directory name when there is no remote. An empty result means "unknown", and
// an unknown repository checks every anchor rather than skipping them — a
// verifier that silently checked nothing would be worse than one that
// occasionally checks too much.
func currentRepoLabel(root string) string {
	if out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output(); err == nil {
		url := strings.TrimSpace(string(out))
		url = strings.TrimSuffix(url, ".git")
		if i := strings.LastIndexAny(url, "/:"); i >= 0 && i+1 < len(url) {
			return url[i+1:]
		}
	}
	// Unknown, deliberately — NOT the directory name.
	//
	// The skip above reads an empty label as "unknown" and checks every anchor
	// rather than skipping them, because a verifier that silently checked
	// nothing would be worse than one that occasionally checks too much. That
	// path was unreachable while this returned filepath.Base(root), which is
	// non-empty for any real path — so in a tree with no origin remote (a
	// tarball, a vendored copy, a clone whose remote is named differently, a
	// worktree in a differently-named folder) the label became the folder name,
	// every anchor from a named repository looked like it belonged elsewhere,
	// and the report read "0 verified, 0 drifted, 0 missing, N elsewhere". A
	// clean-looking report from a verifier that had checked nothing.
	return ""
}

// wingLabel renders the scope for the report, or nothing when unscoped.
func wingLabel(wing string) string {
	if wing == "" {
		return ""
	}
	return " in " + wing
}

// short truncates an id for human output.
func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// anchorCounts is what one verification pass concluded, by kind.
// anchorCounts is what one verification pass concluded, by kind.
//
// unattributable is its own bucket rather than part of elsewhere, because the two
// have DIFFERENT REMEDIES and folding them together hides the one a human can act
// on. "elsewhere" is the system working — that anchor belongs to another
// repository and there is nothing to do. "unattributable" means the anchor carries
// no repo label, so drift can never be reported for it from anywhere, and the fix
// is to label it.
type anchorCounts struct{ verified, drifted, missing, elsewhere, unattributable int }

// verifyAnchors checks every anchor against the tree at root and returns the
// verdicts worth RECORDING plus the counts worth reporting.
//
// Split out of runVerify so the rule can be driven end to end in a test. That
// matters more here than usual: the regression this protects against already
// deleted three memories, and the test that was supposed to guard it recomputed
// a hand-copied duplicate of the condition instead of calling this — so removing
// the guard from the code left the suite green.
func verifyAnchors(root string, anchors []anchor, out io.Writer) ([]verdict, anchorCounts) {
	here := currentRepoLabel(root)
	files := map[string]*sourceFile{}
	var verdicts []verdict
	var drifted, missing, verified, elsewhere, unattributable int
	for _, a := range anchors {
		// An anchor labelled with another repository cannot be checked from
		// here, and calling it MISSING is not a small inaccuracy: the honest
		// response to "the file is gone" is to delete the memory, so a check
		// that cannot see a file destroys the memory pinned to it. A session
		// did exactly that — deleted three chunks whose file lives in a sibling
		// repository — before this existed. Unknown is not absent.
		if a.Repo != "" && here != "" && !strings.EqualFold(a.Repo, here) {
			elsewhere++
			continue
		}

		// ⚠ ONLY A POSITIVE MATCH LICENSES A DESTRUCTIVE VERDICT, and this is the
		// rule the guards above and below could not express.
		//
		// Every one of them was conditioned on the anchor HAVING a label, so an
		// anchor with an EMPTY label in a tree we can name passed all of them and
		// was checked against whatever repository the session happened to be
		// sitting in. Measured 2026-08-29: five sessions in five unrelated
		// repositories were each told that seven of THIS project's Go and
		// TypeScript files were gone, from trees that have never held a .go file —
		// and the verdicts are recorded, so search flags those memories STALE and
		// the session-start hook tells the reader to re-file them. A session that
		// complies rewrites correct records.
		//
		// "I cannot attribute this anchor" and "I am in the wrong tree" are the
		// same epistemic state and now take the same branch.
		attributed := a.Repo != "" && here != "" && strings.EqualFold(a.Repo, here)
		// unchecked accounts for an anchor no verdict may be recorded for, in the
		// bucket that names its remedy.
		unchecked := func() {
			if a.Repo == "" {
				unattributable++
				return
			}
			elsewhere++
		}

		src, ok := files[a.Path]
		if !ok {
			src = readSource(filepath.Join(root, a.Path))
			files[a.Path] = src
		}
		v := verdict{ID: a.ID}
		switch {
		case !src.exists:
			// Absent here. Only a tree that positively claims this anchor can call
			// that a deletion; anywhere else it may simply live somewhere we are not.
			if !attributed {
				unchecked()
				continue
			}
			v.Status, missing = statusMissing, missing+1
			fmt.Fprintf(out, "  MISSING  %s — file is gone (memory %s)\n", a.Path, short(a.DrawerID))
		default:
			if line, ok := src.find(a.Snippet); ok {
				// A MATCH IS TRUSTWORTHY WHEREVER IT IS FOUND, and this asymmetry is
				// deliberate: an unrelated file at the same path is vanishingly
				// unlikely to contain the same verbatim snippet. Refusing to confirm
				// an unlabelled anchor would turn this fix into a check that checks
				// nothing, which is the failure mode currentRepoLabel's own test
				// already names.
				v.Status, v.Line, verified = statusVerified, line, verified+1
			} else if !attributed {
				// The file is present and the snippet is not — which is evidence of
				// drift only if this is the right file. README.md, main.go and go.mod
				// collide across repositories constantly, so without attribution a
				// non-match is not evidence of anything.
				unchecked()
				continue
			} else {
				v.Status, drifted = statusDrifted, drifted+1
				fmt.Fprintf(out, "  DRIFTED  %s — the pinned code is no longer there (memory %s)\n", a.Path, short(a.DrawerID))
				fmt.Fprintf(out, "           was: %s\n", snippetHeadline(a.Snippet, 88))
			}
		}
		verdicts = append(verdicts, v)
	}
	// Said out loud, because the cost of the rule above is real and silent
	// otherwise: these anchors can never report drift from ANY tree until somebody
	// labels them, and a cost nobody can see is a cost nobody fixes.
	if unattributable > 0 {
		fmt.Fprintf(out, "  %d anchor(s) carry no repo label, so this tree cannot be confirmed as "+
			"theirs and no verdict was recorded — pass repo when filing code_anchors so their "+
			"drift can be detected\n", unattributable)
	}
	return verdicts, anchorCounts{verified: verified, drifted: drifted, missing: missing,
		elsewhere: elsewhere, unattributable: unattributable}
}

func snippetHeadline(text string, max int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + "…"
}
