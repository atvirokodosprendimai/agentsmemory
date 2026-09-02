package palace

import "testing"

// TestIsEntityNamespaceAgreesWithTheMint is a drift guard, not a tautology test.
//
// The predicate exists so a caller outside this package can tell an entity
// namespace from a drawer one without re-deriving the naming rule (the boot
// payload check is that caller — issue #164). Its whole value is that it stays
// true of what entityNamespace actually writes, so this asserts the two agree
// rather than asserting either one's spelling. Both read the same constant today;
// this is what fails on the day somebody gives one of them its own.
func TestIsEntityNamespaceAgreesWithTheMint(t *testing.T) {
	for _, teamID := range []string{
		"af063b4c-e118-4faa-9507-32de9fdad5ed",
		"5081d107-3616-4f90-a50f-19c2713da599",
		"t",
	} {
		if ns := entityNamespace(teamID); !IsEntityNamespace(ns) {
			t.Errorf("entityNamespace(%q) = %q, which IsEntityNamespace does not recognise", teamID, ns)
		}
	}
}

// TestADrawerNamespaceIsNotAnEntityNamespace is the half that keeps the skip from
// swallowing the finding the boot check exists for: a predicate that answered
// true for a plain team id would silence every genuine unlabelled-payload report
// while every test about entities still passed.
func TestADrawerNamespaceIsNotAnEntityNamespace(t *testing.T) {
	for _, ns := range []string{
		"af063b4c-e118-4faa-9507-32de9fdad5ed", // a team id, which is what a drawer namespace is
		"",
		"kg_entities",                    // the suffix without the separator
		"team::kg_entities_but_not_this", // a suffix match must be a suffix
	} {
		if IsEntityNamespace(ns) {
			t.Errorf("IsEntityNamespace(%q) = true, so the boot check would skip a drawer namespace", ns)
		}
	}
}
