package skill

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Edit is one anchored replacement in a skill body: find Old, put New in its
// place.
//
// It is anchored by TEXT rather than by a line number, and that is the whole
// design decision. The only copy of a skill a caller holds is whatever
// am_load_skill returned, and that payload carries no line numbers — numbering
// the read would make every caller strip them back off, and a number that has
// gone stale addresses a DIFFERENT line and applies cleanly there. An anchor
// that has gone stale cannot mis-apply; it can only fail to match, which is a
// refusal the caller can read. This repository's recorded defect is the change
// that succeeds while reaching the wrong thing (§Reachability), so the failure
// mode decided the addressing.
type Edit struct {
	// Old is the exact text to replace. It must appear in the body exactly once,
	// unless Count says how many occurrences to expect.
	Old string `json:"old"`
	// New is what replaces it, and may be empty — an edit that deletes is an
	// ordinary edit.
	New string `json:"new"`
	// Count is how many occurrences of Old the caller expects, and replaces all
	// of them. Zero means "exactly one", the safe default: a caller that has not
	// counted is asserting the anchor is unique, and one that has counted is
	// asserting a number the server can check.
	Count int `json:"count,omitempty"`
}

// ErrNoEdits, ErrEmptyAnchor, ErrAnchorNotFound, ErrAnchorAmbiguous,
// ErrAnchorCount, ErrEditsOverlap and ErrVersionMismatch are the ways a patch is
// refused. Every one of them refuses the WHOLE call: a partially-applied edit
// list would leave a skill in a state no caller asked for and no caller could
// predict, and the caller's remedy — load, rebase, resend — is the same whether
// one edit failed or all of them did.
var (
	ErrNoEdits         = errors.New("skill: an edit list must hold at least one edit")
	ErrEmptyAnchor     = errors.New("skill: an edit's \"old\" must be non-empty")
	ErrAnchorNotFound  = errors.New("skill: an edit's \"old\" does not appear in the skill")
	ErrAnchorAmbiguous = errors.New("skill: an edit's \"old\" appears more than once")
	ErrAnchorCount     = errors.New("skill: an edit's \"count\" is not how many times its \"old\" appears")
	ErrEditsOverlap    = errors.New("skill: two edits match overlapping text")
	ErrVersionMismatch = errors.New("skill: the skill changed since the version you read")
)

// ApplyEdits returns body with every edit applied, or an error naming the edit
// that stopped it and why.
//
// Every anchor is located in the ORIGINAL body and all the replacements are then
// made in one pass. That ordering is what lets a caller write its whole edit list
// against the text it actually read: resolving each anchor against the result of
// the previous edit would mean the second anchor has to be written against an
// intermediate the server never showed anyone, which is unauthorable. mrw's plan
// format makes the same promise for the same reason — "every address resolves
// against the ORIGINAL file".
//
// Overlapping matches are refused rather than resolved by order. Two edits whose
// text overlaps express an intention only their author knows, and picking one
// silently is the class of quiet wrong answer this package exists to avoid.
func ApplyEdits(body string, edits []Edit) (string, error) {
	if len(edits) == 0 {
		return "", ErrNoEdits
	}
	// A span is one located replacement. Collecting them all before writing any
	// is what makes the call atomic — nothing is built until every edit is known
	// to apply.
	type span struct {
		start, end int
		with       string
		edit       int
	}
	var spans []span
	for i, e := range edits {
		n := i + 1 // report edits 1-based, the way a caller wrote them down
		if e.Old == "" {
			return "", fmt.Errorf("edit %d: %w", n, ErrEmptyAnchor)
		}
		if e.Count < 0 {
			return "", fmt.Errorf("edit %d: %w: count is negative", n, ErrAnchorCount)
		}
		hits := indexAll(body, e.Old)
		switch {
		case len(hits) == 0:
			return "", fmt.Errorf("edit %d: %w: %s", n, ErrAnchorNotFound, previewAnchor(e.Old))
		case e.Count == 0 && len(hits) > 1:
			return "", fmt.Errorf("edit %d: %w — it matches %d times: %s. Extend it until it is unique, or pass count=%d to replace every occurrence",
				n, ErrAnchorAmbiguous, len(hits), previewAnchor(e.Old), len(hits))
		case e.Count != 0 && e.Count != len(hits):
			return "", fmt.Errorf("edit %d: %w — count=%d but it matches %d times: %s",
				n, ErrAnchorCount, e.Count, len(hits), previewAnchor(e.Old))
		}
		for _, at := range hits {
			spans = append(spans, span{start: at, end: at + len(e.Old), with: e.New, edit: n})
		}
	}
	sort.Slice(spans, func(a, b int) bool { return spans[a].start < spans[b].start })
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return "", fmt.Errorf("edits %d and %d: %w", spans[i-1].edit, spans[i].edit, ErrEditsOverlap)
		}
	}
	var out strings.Builder
	out.Grow(len(body))
	prev := 0
	for _, sp := range spans {
		out.WriteString(body[prev:sp.start])
		out.WriteString(sp.with)
		prev = sp.end
	}
	out.WriteString(body[prev:])
	return out.String(), nil
}

// indexAll returns the start offset of every NON-OVERLAPPING occurrence of
// needle, scanning left to right.
//
// Non-overlapping is what makes the count a caller can verify by eye agree with
// the count reported back: "aa" occurs twice in "aaaa" by this reckoning and
// three times if overlaps are counted, and a caller passing count=2 after
// reading the body should not be told it was wrong.
func indexAll(body, needle string) []int {
	var out []int
	for from := 0; from+len(needle) <= len(body); {
		at := strings.Index(body[from:], needle)
		if at < 0 {
			break
		}
		out = append(out, from+at)
		from += at + len(needle)
	}
	return out
}

// previewAnchor renders an anchor for an error message, shortened so a refusal
// stays readable when the caller anchored on a whole paragraph.
//
// Newlines are escaped rather than printed: a multi-line anchor would otherwise
// break the refusal across lines and bury the edit number that names which one
// failed.
func previewAnchor(s string) string {
	const max = 80
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return strconv.Quote(s)
}

// Patch applies anchored edits to an existing skill, bumping its version like
// any other write.
//
// It exists because Update is a whole-body replace, and a whole-body replace is
// the wrong instrument for changing a paragraph: the caller has to resend a body
// of up to maxSkillContentLen that it can only reproduce from context, so the
// cost scales with the skill rather than the change and every resend is a chance
// to corrupt text nobody meant to touch. Patch sends the change instead.
//
// It never touches the description, because a caller changing one paragraph of a
// body is not saying anything about the summary — and because the alternative,
// an omitted description read as "clear it", is a silent deletion. (Update still
// has that behaviour on its own path; issue filed rather than changed here,
// since whether an omitted field clears or preserves is a decision about a
// shipped tool.)
//
// expectedVersion is the guard a partial edit needs and a whole-body replace does
// not: a replace overwrites whatever is there by definition, while an edit list
// was authored against one particular body and means nothing against another.
// Zero means "no expectation" — versions start at 1, so the sentinel cannot
// collide with a real one.
func (s *Service) Patch(ctx context.Context, t RoleHolder, name string, edits []Edit, expectedVersion int) (Skill, error) {
	if !t.CanWrite() {
		return Skill{}, ErrForbidden
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxSkillNameLen {
		return Skill{}, ErrInvalidName
	}
	// A patch is not a create: there is no body to anchor against, and inventing
	// one from the edits would turn a mistyped name into a new skill nobody asked
	// for. ErrNotFound travels up and the tool reports it.
	current, err := s.repo.GetByName(ctx, t.Team(), name)
	if err != nil {
		return Skill{}, err
	}
	if expectedVersion != 0 && expectedVersion != current.Version {
		return Skill{}, fmt.Errorf("%w: you read v%d and the stored skill is v%d — load it again and rebase your edits",
			ErrVersionMismatch, expectedVersion, current.Version)
	}
	next, err := ApplyEdits(current.Content, edits)
	if err != nil {
		return Skill{}, err
	}
	// The same bounds Update enforces, applied to the RESULT rather than to the
	// input: edits that delete a body down to nothing, or grow it past the limit,
	// are refused for the reason a direct write would be.
	if strings.TrimSpace(next) == "" || len(next) > maxSkillContentLen {
		return Skill{}, ErrInvalidContent
	}
	return s.repo.Upsert(ctx, t.Team(), name, current.Description, next, t.User())
}
