package palace

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestEveryPoolArmHasServiceForArm(t *testing.T) {
	svc := NewService(nil, nil, nil, 0).WithReranker(&fakeReranker{}, DefaultRerankPool)
	for _, arm := range evalArms(EvalOptions{Contextual: true}, true) {
		got := svc.serviceForArm(arm)
		switch arm {
		case ArmProduction, ArmProductionDeep, ArmProductionRetrieve, ArmContextual, ArmFactRetrieval:
			if got != nil {
				t.Errorf("%s must retrieve on its own path, serviceForArm = %v", arm, got)
			}
		default:
			if got == nil {
				t.Errorf("%s has no serviceForArm; evalCase would score it as a miss", arm)
			}
		}
	}
}

func TestEvalCaseDoesNotReimplementRanking(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "eval.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	banned := map[string]bool{
		"rankHybrid": true, "rankRRF": true, "BlendRerank": true,
		"rankHybridWeighted": true, "rankHybridAdaptive": true,
		"rankHybridAdaptiveIDF": true, "rankHybridWeightedNorm": true,
		"rankHybridAdaptiveNorm": true, "rankHybridAdaptiveIDFNorm": true,
		"reorderByRecency": true, "fusionRankerFor": true, "fusionRanker": true,
	}
	required := map[string]bool{"rankRetrieved": false, "Search": false, "serviceForArm": false, "searchCandidates": false}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "evalCaseResult" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			if banned[ident.Name] {
				t.Errorf("evalCaseResult calls %s; pool arms must rank through rankRetrieved", ident.Name)
			}
			if _, ok := required[ident.Name]; ok {
				required[ident.Name] = true
			}
			return true
		})
		for name, seen := range required {
			if !seen {
				t.Errorf("evalCaseResult never calls %s", name)
			}
		}
		return
	}
	t.Fatal("evalCaseResult not found")
}

func TestCandidateUnionRetrievesThroughSearchCandidates(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "eval.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "CandidateUnion" || fn.Body == nil {
			continue
		}
		var sawSearchCandidates, sawVectorsSearch bool
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok && ident.Name == "searchCandidates" {
				sawSearchCandidates = true
			}
			sel, ok := node.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Search" {
				return true
			}
			recv, ok := sel.X.(*ast.SelectorExpr)
			if ok && recv.Sel.Name == "vectors" {
				sawVectorsSearch = true
			}
			return true
		})
		if !sawSearchCandidates {
			t.Error("CandidateUnion never calls searchCandidates; eval pooling is a second retrieval route")
		}
		if sawVectorsSearch {
			t.Error("CandidateUnion still one-shots vectors.Search; pool retrieval must go through searchCandidates")
		}
		return
	}
	t.Fatal("CandidateUnion not found")
}
