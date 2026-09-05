package palace

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// gormWriteVerbs are the gorm calls that make a method a writer. Transaction is
// here because a transaction is opened to write; a read-only transaction in
// this package would be a defect of its own.
var gormWriteVerbs = map[string]bool{
	"Create": true, "CreateInBatches": true, "Save": true, "Update": true, "Updates": true,
	"UpdateColumn": true, "UpdateColumns": true, "Exec": true, "Delete": true, "Transaction": true,
	"FirstOrCreate": true, "Clauses": true,
}

// appendOnlyLogs are the Repo writers that do not put their caller on the
// write path, each with the reason. TestAppendOnlyLogsAreJustified refuses an
// entry with no reason, one naming no Repo method, and one whose method has
// stopped being a Create-only insert — the check that keeps this from becoming
// the exemption list the T6 Stop Condition warns against.
//
// The distinction is the record's: a validating read is one whose result
// SELECTS the rows a write will touch or supplies the values it writes. A
// search that afterwards appends its own event to a log, or a fetch that logs
// itself, selects nothing by that read — no later write is bound by it — and
// treating those logs as writes would put the whole recall path, the dominant
// workload the reader exists for, back on the single writer connection.
var appendOnlyLogs = map[string]string{
	"recordSearch": "search_events is an append-only log of what was asked; the search's reads select no row this insert touches",
	"recordFetch":  "drawer_fetches is an append-only log of what was fetched; the fetch's read selects no row this insert touches",
}

// writePathFindings lists every read-model lookup made on the write path, and
// how many Service methods it judged to be on it.
//
// ADR-052's Decision: the write path validates against its own reads, never
// against the read model — a lookup on the pooled reader is a different
// snapshot, so a check taken there is not binding on the write that follows,
// and both halves look correct in review. Review of PR #233 found exactly that
// shape twice (supersedeInto and InvalidateDrawer took the rows a writer
// transaction would touch from the reader) and asked for the class, not the
// instances. This is the class, derived from the source:
//
//   - a Repo method WRITES if its body reaches a gorm write verb;
//   - a Service method is on the write path if its body reaches a gorm write
//     verb, calls a Repo writer other than an append-only log, or reads
//     through the writer view s.writer — and, transitively, if it calls a
//     Service method that is. The closure runs caller-ward because a caller's
//     reads FEED the write it goes on to make (Update's lookup feeds
//     supersedeInto), which is the shape the reviewer traced;
//   - a finding is any s.repo.<read method> call inside such a method, and
//     any call from such a method into a Service helper that itself reads
//     through s.repo — a helper on the write path takes the view it should
//     read through, or names s.writer, rather than deciding on the caller's
//     behalf.
//
// What it cannot see is dataflow: it says WHERE a read ran, not whether the
// write used it. That over-includes a decorative read on a write path (a
// predecessor lookup attached to a record about to be ended), which costs one
// writer round trip and buys the rule being mechanical.
func writePathFindings(fset *token.FileSet, files []*ast.File) (judged int, findings []string) {
	repoWrites := map[string]bool{}
	repoReads := map[string]bool{}
	services := map[string]*ast.FuncDecl{}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 || fd.Body == nil {
				continue
			}
			switch receiverName(fd) {
			case "Repo":
				if bodyReaches(fd.Body, func(sel *ast.SelectorExpr) bool { return gormWriteVerbs[sel.Sel.Name] }) {
					repoWrites[fd.Name.Name] = true
				} else {
					repoReads[fd.Name.Name] = true
				}
			case "Service":
				services[fd.Name.Name] = fd
			}
		}
	}

	onPath := map[string]bool{}
	for name, fd := range services {
		onPath[name] = bodyReaches(fd.Body, func(sel *ast.SelectorExpr) bool {
			if gormWriteVerbs[sel.Sel.Name] {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "s" && sel.Sel.Name == "writer" {
				return true
			}
			return isRepoCall(sel) && repoWrites[sel.Sel.Name] && appendOnlyLogs[sel.Sel.Name] == ""
		})
	}
	for changed := true; changed; {
		changed = false
		for name, fd := range services {
			if onPath[name] {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if onPath[name] {
					return false
				}
				if callee, ok := serviceCallee(n); ok && onPath[callee] {
					onPath[name], changed = true, true
				}
				return true
			})
		}
	}

	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !onPath[name] {
			continue
		}
		judged++
		fd := services[name]
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if callee, ok := serviceCallee(call); ok && !onPath[callee] {
				if h := services[callee]; h != nil && bodyReaches(h.Body, func(sel *ast.SelectorExpr) bool { return isRepoCall(sel) && repoReads[sel.Sel.Name] }) {
					findings = append(findings, fmt.Sprintf("%s: %s is on the write path and calls %s, which reads through s.repo — thread the view into the helper, or have it read through s.writer (ADR-052)",
						fset.Position(call.Pos()), name, callee))
				}
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !isRepoCall(sel) || !repoReads[sel.Sel.Name] {
				return true
			}
			findings = append(findings, fmt.Sprintf("%s: %s is on the write path and reads %s through s.repo — the read model — so the lookup is not binding on the write that follows (ADR-052); read it through s.writer",
				fset.Position(call.Pos()), name, sel.Sel.Name))
			return true
		})
	}
	sort.Strings(findings)
	return judged, findings
}

// serviceCallee returns the method name when n is a call s.<Method>(...).
func serviceCallee(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok || x.Name != "s" {
		return "", false
	}
	return sel.Sel.Name, true
}

// isRepoCall reports whether sel is s.repo.<Name>.
func isRepoCall(sel *ast.SelectorExpr) bool {
	inner, ok := sel.X.(*ast.SelectorExpr)
	if !ok || inner.Sel.Name != "repo" {
		return false
	}
	x, ok := inner.X.(*ast.Ident)
	return ok && x.Name == "s"
}

func receiverName(fd *ast.FuncDecl) string {
	if st, ok := fd.Recv.List[0].Type.(*ast.StarExpr); ok {
		if id, ok := st.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

func bodyReaches(body *ast.BlockStmt, pred func(*ast.SelectorExpr) bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok && pred(sel) {
			found = true
			return false
		}
		return true
	})
	return found
}

// parsePalaceSources parses this package's non-test files.
func parsePalaceSources(t *testing.T, fset *token.FileSet) []*ast.File {
	t.Helper()
	var files []*ast.File
	paths, _ := filepath.Glob("*.go")
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, p, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		files = append(files, f)
	}
	return files
}

// TestNoWritePathReadsTheReadModel is the gate over writePathFindings on this
// package's own source, with the falsifiability case inside the same fence.
func TestNoWritePathReadsTheReadModel(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	judged, findings := writePathFindings(fset, parsePalaceSources(t, fset))
	if judged < 10 {
		t.Fatalf("judged %d write-path Service methods, want at least 10 — the gate is looking at the wrong tree", judged)
	}
	for _, f := range findings {
		t.Error(f)
	}

	t.Run("catches a validating read on the reader", func(t *testing.T) {
		const fixture = `package palace

func (r *Repo) Rows(ctx context.Context) ([]string, error) { var out []string; return out, r.db.Find(&out).Error }
func (r *Repo) EndRows(ctx context.Context, ids []string) error { return r.db.Where("id IN ?", ids).Updates(map[string]any{"valid_to": "now"}).Error }
func (r *Repo) recordSearch(ctx context.Context, ev string) error { return r.db.Create(&ev).Error }

func (s *Service) drift(ctx context.Context) error {
	ids, err := s.repo.Rows(ctx)
	if err != nil {
		return err
	}
	return s.repo.EndRows(ctx, ids)
}

func (s *Service) fine(ctx context.Context) error {
	ids, err := s.writer.Rows(ctx)
	if err != nil {
		return err
	}
	return s.writer.EndRows(ctx, ids)
}

func (s *Service) feeder(ctx context.Context) error {
	if _, err := s.repo.Rows(ctx); err != nil {
		return err
	}
	return s.fine(ctx)
}

func (s *Service) viaHelper(ctx context.Context) error {
	if _, err := s.lookup(ctx); err != nil {
		return err
	}
	return s.writer.EndRows(ctx, nil)
}

func (s *Service) lookup(ctx context.Context) ([]string, error) { return s.repo.Rows(ctx) }

func (s *Service) search(ctx context.Context) ([]string, error) {
	rows, err := s.repo.Rows(ctx)
	_ = s.repo.recordSearch(ctx, "q")
	return rows, err
}
`
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "fixture.go", fixture, 0)
		if err != nil {
			t.Fatal(err)
		}
		judged, got := writePathFindings(fset, []*ast.File{f})
		if judged != 4 {
			t.Errorf("judged %d write-path methods in the fixture, want 4 (drift, fine, feeder, viaHelper; search only logs)", judged)
		}
		joined := strings.Join(got, "\n")
		for _, want := range []string{"drift is on the write path and reads Rows", "feeder is on the write path and reads Rows", "viaHelper is on the write path and calls lookup"} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing finding %q in:\n%s", want, joined)
			}
		}
		if strings.Contains(joined, "search ") || len(got) != 3 {
			t.Errorf("want exactly three findings and none for search (an append-only log is not a write path); got %d:\n%s", len(got), joined)
		}
	})
}

// TestAppendOnlyLogsAreJustified refuses an exemption with no reason, one that
// names no Repo method, and one whose method has stopped being a Create-only
// insert — the moment it updates or deletes, its caller's reads can select
// rows it touches and the exemption is wrong.
func TestAppendOnlyLogsAreJustified(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	verbsOf := map[string]map[string]bool{}
	for _, f := range parsePalaceSources(t, fset) {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || fd.Body == nil || receiverName(fd) != "Repo" {
				continue
			}
			verbs := map[string]bool{}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && gormWriteVerbs[sel.Sel.Name] {
					verbs[sel.Sel.Name] = true
				}
				return true
			})
			verbsOf[fd.Name.Name] = verbs
		}
	}
	for name, reason := range appendOnlyLogs {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("appendOnlyLogs[%q] has no reason; the reason is the review", name)
		}
		verbs, ok := verbsOf[name]
		if !ok {
			t.Errorf("appendOnlyLogs names %q, which is not a Repo method — a pointer to nothing", name)
			continue
		}
		for v := range verbs {
			if v != "Create" && v != "CreateInBatches" {
				t.Errorf("appendOnlyLogs[%q] is no longer append-only: it reaches %s, so a caller's read can select rows it touches", name, v)
			}
		}
	}
}
